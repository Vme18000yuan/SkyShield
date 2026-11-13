package application

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/net/idna"
)

func Prepare(in Input) (Prepared, error) {
	bad := func(message string) (Prepared, error) { return Prepared{}, &InvalidInput{message} }
	if _, err := uuid.Parse(in.RequestID); err != nil {
		return bad("Invalid requestId")
	}
	if len(in.RawRequest) == 0 || len(in.RawRequest) > MaxRequestBytes || !utf8.ValidString(in.RawRequest) {
		return bad("Raw request must be UTF-8 text, at most 512 KiB")
	}
	u, err := url.Parse(strings.TrimSpace(in.TargetURL))
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" || len(in.TargetURL) > 8192 {
		return bad("Use an HTTP(S) target URL without credentials or fragment")
	}
	host := u.Hostname()
	if strings.ContainsAny(host, "%\x00\r\n") {
		return bad("Invalid destination host")
	}
	if net.ParseIP(host) == nil {
		host, err = idna.Lookup.ToASCII(host)
		if err != nil {
			return bad("Invalid destination hostname")
		}
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return bad("Destination port must be between 1 and 65535")
	}
	u.Host = net.JoinHostPort(host, port)
	timeout := in.TimeoutSeconds
	if timeout == 0 {
		timeout = 15
	}
	if timeout < 1 || timeout > 60 {
		return bad("Timeout must be between 1 and 60 seconds")
	}
	boundary, skip := strings.Index(in.RawRequest, "\r\n\r\n"), 4
	if lf := strings.Index(in.RawRequest, "\n\n"); lf >= 0 && (boundary < 0 || lf < boundary) {
		boundary, skip = lf, 2
	}
	if boundary < 0 || boundary > 64<<10 {
		return bad("Separate headers and body with a blank line; headers must fit in 64 KiB")
	}
	head := strings.ReplaceAll(in.RawRequest[:boundary], "\r\n", "\n")
	if strings.ContainsAny(head, "\r\x00") {
		return bad("Invalid header line ending or NUL byte")
	}
	lines := strings.Split(head, "\n")
	body := in.RawRequest[boundary+skip:]
	first := strings.Split(lines[0], " ")
	if len(first) != 3 || (first[2] != "HTTP/1.1" && first[2] != "HTTP/1.0" && first[2] != "HTTP/2" && first[2] != "HTTP/2.0") || first[0] == "CONNECT" {
		return bad("Use a single HTTP/1.0, HTTP/1.1 or HTTP/2 request; CONNECT is not supported")
	}
	protocol := first[2]
	http2Request := protocol == "HTTP/2" || protocol == "HTTP/2.0"
	if http2Request {
		protocol = "HTTP/2.0"
		lines[0] = first[0] + " " + first[1] + " " + protocol
	}
	lengthAt := -1
	chunked := false
	rawHost := ""
	hostSeen := false
	removeHeaders := map[string]bool{"connection": true, "proxy-connection": true, "keep-alive": true}
	for i, line := range lines[1:] {
		name, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) != name {
			return bad("Invalid HTTP header")
		}
		if strings.EqualFold(name, "Host") {
			if hostSeen {
				return bad("Use a single Host header")
			}
			hostSeen = true
			rawHost = strings.TrimSpace(value)
		}
		if http2Request && strings.EqualFold(name, "Connection") {
			for _, key := range strings.Split(value, ",") {
				key = strings.ToLower(strings.TrimSpace(key))
				if key != "" {
					removeHeaders[key] = true
				}
			}
		}
		if strings.EqualFold(name, "Content-Length") {
			if lengthAt >= 0 {
				return bad("Duplicate Content-Length is not supported")
			}
			lengthAt = i + 1
		}
		if strings.EqualFold(name, "Transfer-Encoding") {
			chunked = true
		}
		if strings.EqualFold(name, "Upgrade") {
			return bad("Protocol upgrade is not supported by single HTTP replay")
		}
	}
	if chunked && lengthAt >= 0 {
		return bad("Content-Length and Transfer-Encoding cannot be combined")
	}
	if http2Request && chunked {
		return bad("HTTP/2 messages must not contain Transfer-Encoding")
	}
	if http2Request && (removeHeaders["host"] || removeHeaders["content-length"]) {
		return bad("Connection cannot nominate Host or Content-Length for HTTP/2")
	}
	auto := in.AutoContentLength == nil || *in.AutoContentLength
	if auto && !chunked {
		if lengthAt >= 0 {
			name, _, _ := strings.Cut(lines[lengthAt], ":")
			lines[lengthAt] = name + ": " + strconv.Itoa(len(body))
		} else if len(body) > 0 || first[0] == "POST" || first[0] == "PUT" || first[0] == "PATCH" {
			lines = append(lines, "Content-Length: "+strconv.Itoa(len(body)))
		}
	}
	normalized := []string{}
	if http2Request {
		kept := []string{lines[0]}
		removed := map[string]bool{}
		for _, line := range lines[1:] {
			name, _, _ := strings.Cut(line, ":")
			key := strings.ToLower(name)
			if removeHeaders[key] {
				removed[key] = true
				continue
			}
			kept = append(kept, line)
		}
		for name := range removed {
			normalized = append(normalized, name)
		}
		sort.Strings(normalized)
		lines = kept
	}
	raw := []byte(strings.Join(lines, "\r\n") + "\r\n\r\n" + body)
	parseRaw := raw
	if http2Request {
		parseLines := append([]string(nil), lines...)
		parseLines[0] = first[0] + " " + first[1] + " HTTP/1.1"
		parseRaw = []byte(strings.Join(parseLines, "\r\n") + "\r\n\r\n" + body)
	}
	reader := bufio.NewReader(bytes.NewReader(parseRaw))
	request, err := http.ReadRequest(reader)
	if err != nil {
		return bad("Raw request is not a valid HTTP message")
	}
	if request.ProtoMajor == 1 && request.ProtoMinor == 1 && request.Host == "" {
		return bad("HTTP/1.1 requires a Host header")
	}
	if request.URL.IsAbs() && (request.URL.User != nil || (request.URL.Scheme != "http" && request.URL.Scheme != "https")) {
		return bad("Invalid absolute request URL")
	}
	decodedBody, err := io.ReadAll(request.Body)
	if err != nil {
		request.Body.Close()
		return bad("Body length or chunk framing is invalid")
	}
	request.Body.Close()
	if _, err = reader.ReadByte(); err != io.EOF {
		return bad("Body length does not match its headers, or multiple messages were supplied")
	}
	if http2Request {
		if hostSeen && rawHost != "" {
			request.Host = rawHost
		}
		request.Proto = "HTTP/2.0"
		request.ProtoMajor = 2
		request.ProtoMinor = 0
		if value := request.Header.Get("Te"); value != "" && !strings.EqualFold(value, "trailers") {
			return bad("HTTP/2 TE header may only contain trailers")
		}
	}
	verify := in.VerifyTLS == nil || *in.VerifyTLS
	return Prepared{Target: u, Raw: raw, Request: request, VerifyTLS: verify, Timeout: time.Duration(timeout) * time.Second, Protocol: protocol, Body: decodedBody, NormalizedHeaders: normalized}, nil
}

func (p Prepared) PolicyURLs() []string {
	u := *p.Target
	u.Path = p.Request.URL.Path
	u.RawPath = p.Request.URL.RawPath
	u.RawQuery = p.Request.URL.RawQuery
	urls := []string{u.String()}
	if p.Request.Host != "" {
		u.Host = p.Request.Host
		urls = append(urls, u.String())
	}
	if p.Request.URL.IsAbs() {
		urls = append(urls, p.Request.URL.String())
	}
	return urls
}

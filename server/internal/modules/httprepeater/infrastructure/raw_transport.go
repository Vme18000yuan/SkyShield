package infrastructure

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	app "github.com/yyhuni/lunafox/server/internal/modules/httprepeater/application"
)

type RawTransport struct {
	Proxy func(*http.Request) (*url.URL, error)
}

func (t *RawTransport) Send(ctx context.Context, p app.Prepared, guard app.AddressGuard) app.Result {
	started := time.Now()
	result := app.Result{RequestRaw: string(p.Raw), RequestProtocol: p.Protocol, Representation: "http1_raw", NormalizedHeaders: p.NormalizedHeaders}
	if p.Protocol == "HTTP/2.0" {
		result.Representation = "http2_decoded"
	}
	finish := func(code string) app.Result {
		result.ErrorCode = code
		result.ElapsedMS = time.Since(started).Milliseconds()
		return result
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, p.Target.Hostname())
	if err != nil || len(addresses) == 0 {
		return finish(networkError(ctx, "dns_failed"))
	}
	for _, ip := range addresses {
		if guard != nil && guard(ip.IP.String()) != nil {
			return finish("blocked")
		}
	}
	var proxyURL *url.URL
	if t.Proxy != nil {
		proxyURL, err = t.Proxy(&http.Request{URL: p.Target})
		if err != nil {
			return finish("proxy_failed")
		}
	}
	var conn net.Conn
	var cleanup func()
	code := "connect_failed"
	for i, address := range addresses {
		if i >= 8 {
			break
		}
		conn, cleanup, code = t.open(ctx, p, net.JoinHostPort(address.IP.String(), p.Target.Port()), proxyURL)
		if conn != nil {
			break
		}
		if code != "connect_failed" {
			return finish(networkError(ctx, code))
		}
	}
	if conn == nil {
		return finish(networkError(ctx, code))
	}
	defer cleanup()
	if p.Protocol == "HTTP/2.0" {
		return t.sendHTTP2(ctx, p, conn, started)
	}
	n, err := io.Copy(conn, bytes.NewReader(p.Raw))
	result.Sent = n > 0
	if err != nil {
		return finish(networkError(ctx, "write_failed"))
	}
	var captured bytes.Buffer
	limited := &io.LimitedReader{R: conn, N: app.MaxResponseBytes + 1}
	reader := bufio.NewReader(io.TeeReader(limited, &captured))
	for interim := 0; interim < 9; interim++ {
		var response *http.Response
		response, err = http.ReadResponse(reader, p.Request)
		if err != nil {
			break
		}
		result.StatusCode = response.StatusCode
		result.ResponseProtocol = response.Proto
		if response.StatusCode >= 100 && response.StatusCode < 200 && response.StatusCode != 101 {
			response.Body.Close()
			if interim == 8 {
				err = errors.New("too many informational responses")
				break
			}
			continue
		}
		if response.StatusCode == 101 {
			break
		}
		if response.StatusCode != 101 {
			_, err = io.Copy(io.Discard, response.Body)
		}
		response.Body.Close()
		break
	}
	data := captured.Bytes()
	result.Truncated = len(data) > app.MaxResponseBytes
	if result.Truncated {
		data = data[:app.MaxResponseBytes]
	}
	setCapturedResponse(&result, data)
	if result.Truncated {
		return finish("response_limit")
	}
	if err != nil {
		var timed net.Error
		if errors.As(err, &timed) && timed.Timeout() {
			return finish("timeout")
		}
		return finish(networkError(ctx, "read_failed"))
	}
	if ctx.Err() != nil {
		return finish(networkError(ctx, "canceled"))
	}
	return finish("")
}

func setCapturedResponse(result *app.Result, data []byte) {
	result.ResponseBytes = append([]byte(nil), data...)
	result.ByteCount = len(data)
	result.Binary = !utf8.Valid(data)
	for _, b := range data {
		if b < 32 && b != '\r' && b != '\n' && b != '\t' {
			result.Binary = true
			break
		}
	}
	result.ResponseRaw = strings.ToValidUTF8(string(data), "�")
	if result.Binary {
		if end := bytes.Index(data, []byte("\r\n\r\n")); end >= 0 {
			result.ResponseRaw = string(data[:end+4])
		} else {
			result.ResponseRaw = ""
		}
	}
}

func networkError(ctx context.Context, fallback string) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	if ctx.Err() != nil {
		return "canceled"
	}
	return fallback
}

func (t *RawTransport) open(ctx context.Context, p app.Prepared, address string, proxyURL *url.URL) (net.Conn, func(), string) {
	destination := address
	if proxyURL != nil {
		if proxyURL.Scheme != "http" && proxyURL.Scheme != "https" {
			return nil, nil, "proxy_failed"
		}
		port := proxyURL.Port()
		if port == "" {
			port = "80"
			if proxyURL.Scheme == "https" {
				port = "443"
			}
		}
		destination = net.JoinHostPort(proxyURL.Hostname(), port)
	}
	raw, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", destination)
	if err != nil {
		return nil, nil, "connect_failed"
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = raw.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
	cleanup := func() { stop(); _ = raw.Close() }
	fail := func(code string) (net.Conn, func(), string) { cleanup(); return nil, nil, code }
	var conn net.Conn = raw
	if proxyURL != nil {
		if proxyURL.Scheme == "https" {
			secure := tls.Client(conn, &tls.Config{ServerName: proxyURL.Hostname(), MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})
			if err = secure.HandshakeContext(ctx); err != nil {
				return fail("proxy_failed")
			}
			conn = secure
		}
		request := &http.Request{Method: "CONNECT", URL: &url.URL{Opaque: address}, Host: address, Header: make(http.Header)}
		if proxyURL.User != nil {
			password, _ := proxyURL.User.Password()
			request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(proxyURL.User.Username()+":"+password)))
		}
		if err = request.Write(conn); err != nil {
			return fail("proxy_failed")
		}
		// CONNECT is a separate proxy handshake; its credentials and response are
		// never included in the target's request or returned as target evidence.
		reader := bufio.NewReader(io.LimitReader(conn, 64<<10))
		response, readErr := http.ReadResponse(reader, request)
		if readErr != nil || response.StatusCode != http.StatusOK || reader.Buffered() != 0 {
			return fail("proxy_failed")
		}
	}
	if p.Target.Scheme == "https" {
		protocols := []string{"http/1.1"}
		if p.Protocol == "HTTP/2.0" {
			protocols = []string{"h2"}
		}
		secure := tls.Client(conn, &tls.Config{ServerName: p.Target.Hostname(), MinVersion: tls.VersionTLS12, NextProtos: protocols, InsecureSkipVerify: !p.VerifyTLS}) // explicit per-request setting
		if err = secure.HandshakeContext(ctx); err != nil {
			if p.Protocol == "HTTP/2.0" && strings.Contains(err.Error(), "no application protocol") {
				return fail("http2_not_negotiated")
			}
			return fail("tls_failed")
		}
		if p.Protocol == "HTTP/2.0" && secure.ConnectionState().NegotiatedProtocol != "h2" {
			return fail("http2_not_negotiated")
		}
		conn = secure
	}
	return conn, cleanup, ""
}

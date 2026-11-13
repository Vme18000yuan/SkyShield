package infrastructure

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	app "github.com/yyhuni/lunafox/server/internal/modules/httprepeater/application"
	"golang.org/x/net/http2"
)

type http2Field struct{ name, value string }

func (t *RawTransport) sendHTTP2(ctx context.Context, p app.Prepared, conn net.Conn, started time.Time) app.Result {
	result := app.Result{RequestRaw: string(p.Raw), RequestProtocol: "HTTP/2.0", Representation: "http2_decoded", NormalizedHeaders: p.NormalizedHeaders}
	finish := func(code string) app.Result {
		result.ErrorCode = code
		result.ElapsedMS = time.Since(started).Milliseconds()
		return result
	}
	failure := func(err error, code string) app.Result {
		var timed net.Error
		if errors.As(err, &timed) && timed.Timeout() {
			code = "timeout"
		}
		return finish(networkError(ctx, code))
	}
	transport := &http2.Transport{AllowHTTP: p.Target.Scheme == "http", DisableCompression: true, MaxHeaderListSize: 64 << 10, MaxReadFrameSize: 64 << 10}
	client, err := transport.NewClientConn(conn)
	if err != nil {
		return failure(err, "http2_failed")
	}
	defer client.Close()
	// A single ClientConn.RoundTrip does not dial, retry, redirect or switch to
	// HTTP/1. It uses the already authorized, IP-pinned connection only.
	request := p.Request.Clone(ctx)
	request.URL.Scheme = p.Target.Scheme
	request.URL.Host = p.Target.Host
	request.RequestURI = ""
	request.Close = false
	request.GetBody = nil
	request.Body = io.NopCloser(bytes.NewReader(p.Body))
	if len(p.Body) == 0 {
		request.Body = http.NoBody
	}
	if _, exists := request.Header["User-Agent"]; !exists {
		request.Header["User-Agent"] = []string{""}
	}
	var mu sync.Mutex
	fields := []http2Field{}
	var sent atomic.Bool
	written := make(chan struct{})
	var writtenOnce sync.Once
	trace := &httptrace.ClientTrace{
		WroteHeaderField: func(name string, values []string) {
			mu.Lock()
			defer mu.Unlock()
			for _, value := range values {
				fields = append(fields, http2Field{name, value})
			}
		},
		WroteHeaders: func() { sent.Store(true) },
		WroteRequest: func(httptrace.WroteRequestInfo) { writtenOnce.Do(func() { close(written) }) },
	}
	request = request.WithContext(httptrace.WithClientTrace(ctx, trace))
	response, err := client.RoundTrip(request)
	if err != nil {
		_ = client.Close()
		// Cancellation may return before the writer's trace callbacks complete.
		// Let that bounded cleanup publish whether headers had already been sent.
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-written:
		case <-timer.C:
		}
		timer.Stop()
	}
	result.Sent = sent.Load()
	mu.Lock()
	if len(fields) > 0 {
		result.RequestRaw = http2RequestText(fields, p.Body)
	}
	mu.Unlock()
	if err != nil {
		return failure(err, "http2_failed")
	}
	defer response.Body.Close()
	result.ResponseProtocol = response.Proto
	result.StatusCode = response.StatusCode
	if response.ProtoMajor != 2 {
		return finish("http2_not_negotiated")
	}
	head := fmt.Sprintf("HTTP/2.0 %d\r\n", response.StatusCode) + http2HeaderText(response.Header) + "\r\n"
	remaining := max(0, app.MaxResponseBytes-len(head))
	body, readErr := io.ReadAll(io.LimitReader(response.Body, int64(remaining)+1))
	result.Truncated = len(body) > remaining || len(head) > app.MaxResponseBytes
	if len(body) > remaining {
		body = body[:remaining]
	}
	data := append([]byte(head), body...)
	if len(data) > app.MaxResponseBytes {
		data = data[:app.MaxResponseBytes]
	}
	setCapturedResponse(&result, data)
	if result.Truncated {
		return finish("response_limit")
	}
	if readErr != nil {
		return failure(readErr, "read_failed")
	}
	if ctx.Err() != nil {
		return finish(networkError(ctx, "canceled"))
	}
	return finish("")
}

// HTTP/2 has no textual start lines. These are decoded message views suitable
// for inspection/replay, not a dump of HPACK blocks or binary HTTP/2 frames.
func http2RequestText(fields []http2Field, body []byte) string {
	pseudo := map[string]string{}
	for _, field := range fields {
		if strings.HasPrefix(field.name, ":") {
			pseudo[field.name] = field.value
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%s %s HTTP/2.0\r\n", pseudo[":method"], pseudo[":path"])
	fmt.Fprintf(&out, "Host: %s\r\n", pseudo[":authority"])
	for _, field := range fields {
		if !strings.HasPrefix(field.name, ":") {
			fmt.Fprintf(&out, "%s: %s\r\n", field.name, field.value)
		}
	}
	out.WriteString("\r\n")
	out.Write(body)
	return out.String()
}

func http2HeaderText(headers http.Header) string {
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out strings.Builder
	for _, key := range keys {
		for _, value := range headers[key] {
			fmt.Fprintf(&out, "%s: %s\r\n", strings.ToLower(key), value)
		}
	}
	return out.String()
}

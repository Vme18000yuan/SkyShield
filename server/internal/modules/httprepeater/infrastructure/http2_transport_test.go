package infrastructure

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	app "github.com/yyhuni/lunafox/server/internal/modules/httprepeater/application"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func h2Prepared(t *testing.T, target, raw string) app.Prepared {
	t.Helper()
	verify := false
	p, err := app.Prepare(app.Input{RequestID: uuid.NewString(), TargetURL: target, RawRequest: raw, VerifyTLS: &verify})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHTTP2ReportedRequestUsesRealTLSHTTP2(t *testing.T) {
	var count atomic.Int32
	seen := make(chan *http.Request, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		seen <- r
		w.Header().Add("Set-Cookie", "a=1")
		w.Header().Add("Set-Cookie", "b=2")
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, "<EnumerationResults/>")
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	raw := "GET /container?restype=container&comp=list HTTP/2.0\nHost: storage.skyshield.example\nAccept: application/xml\n\n"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := (&RawTransport{}).Send(ctx, h2Prepared(t, server.URL, raw), nil)
	if result.ErrorCode != "" || result.ResponseProtocol != "HTTP/2.0" || result.RequestProtocol != "HTTP/2.0" || result.Representation != "http2_decoded" || !result.Sent || count.Load() != 1 {
		t.Fatalf("not real HTTP/2: %#v", result)
	}
	r := <-seen
	if r.ProtoMajor != 2 || r.TLS.NegotiatedProtocol != "h2" || r.Host != "storage.skyshield.example" || r.URL.RequestURI() != "/container?restype=container&comp=list" || r.Header.Get("Accept") != "application/xml" {
		t.Fatalf("request altered: %#v", r)
	}
	if !strings.Contains(result.RequestRaw, "HTTP/2.0\r\nHost: storage.skyshield.example") || !strings.Contains(result.ResponseRaw, "set-cookie: a=1\r\nset-cookie: b=2\r\n") || !strings.HasSuffix(result.ResponseRaw, "<EnumerationResults/>") {
		t.Fatalf("decoded views lost data: %#v", result)
	}
}

func TestHTTP2BodyAndConnectionHeaders(t *testing.T) {
	body := make(chan string, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		body <- string(data)
		if r.ProtoMajor != 2 || r.Header.Get("Connection") != "" || r.Header.Get("X-Hop") != "" {
			t.Error("invalid h2 headers reached target")
		}
		w.WriteHeader(401)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	raw := "POST /api HTTP/2\nHost: example.test\nConnection: keep-alive, x-hop\nKeep-Alive: timeout=30\nX-Hop: remove\nContent-Length: 99\n\n中"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := (&RawTransport{}).Send(ctx, h2Prepared(t, server.URL, raw), nil)
	if result.ErrorCode != "" || result.StatusCode != 401 || len(result.NormalizedHeaders) != 3 || strings.Contains(strings.ToLower(result.RequestRaw), "connection:") || !strings.Contains(result.RequestRaw, "content-length: 3") {
		t.Fatalf("normalization not reflected: %#v", result)
	}
	if <-body != "中" {
		t.Fatal("body changed")
	}
}

func TestHTTP2DoesNotDowngradeToHTTP1(t *testing.T) {
	var count atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1) }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := (&RawTransport{}).Send(ctx, h2Prepared(t, server.URL, "GET / HTTP/2.0\nHost: example.test\n\n"), nil)
	if result.ErrorCode != "http2_not_negotiated" || result.Sent || count.Load() != 0 {
		t.Fatalf("silent downgrade or resend: %#v calls=%d", result, count.Load())
	}
}

func TestCleartextHTTP2AndBoundedResponse(t *testing.T) {
	var version atomic.Int32
	server := httptest.NewServer(h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version.Store(int32(r.ProtoMajor))
		_, _ = io.WriteString(w, strings.Repeat("x", app.MaxResponseBytes+1000))
	}), &http2.Server{}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := (&RawTransport{}).Send(ctx, h2Prepared(t, server.URL, "GET / HTTP/2.0\nHost: example.test\n\n"), nil)
	if version.Load() != 2 || !result.Truncated || result.ByteCount != app.MaxResponseBytes || result.ErrorCode != "response_limit" {
		t.Fatalf("h2c bound failed: %#v", result)
	}
}

func TestHTTP2CancellationClosesTheStream(t *testing.T) {
	started := make(chan struct{})
	released := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(released) }))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan app.Result, 1)
	prepared := h2Prepared(t, server.URL, "GET / HTTP/2.0\nHost: example.test\n\n")
	go func() {
		done <- (&RawTransport{}).Send(ctx, prepared, nil)
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("request did not reach HTTP/2 server")
	}
	cancel()
	select {
	case result := <-done:
		if result.ErrorCode != "canceled" || !result.Sent {
			t.Fatalf("cancel lost: %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not stop client")
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("HTTP/2 stream stayed open")
	}
}

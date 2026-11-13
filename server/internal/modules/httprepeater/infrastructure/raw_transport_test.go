package infrastructure

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	app "github.com/yyhuni/lunafox/server/internal/modules/httprepeater/application"
)

func prepared(t *testing.T, target, raw string, verify bool) app.Prepared {
	t.Helper()
	p, err := app.Prepare(app.Input{RequestID: uuid.NewString(), TargetURL: target, RawRequest: raw, VerifyTLS: &verify})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func get(host string) string {
	return "GET / HTTP/1.1\r\nHost: " + host + "\r\nConnection: close\r\n\r\n"
}

func TestRawWirePreservesDuplicatesChunkingAndVirtualHost(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	wire := "HTTP/1.1 201 Custom reason\r\nX-Proof: one\r\nX-Proof: two\r\nTransfer-Encoding: chunked\r\n\r\n3\r\nabc\r\n0\r\n\r\n"
	received := make(chan string, 1)
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		var raw strings.Builder
		for {
			line, e := reader.ReadString('\n')
			if e != nil {
				return
			}
			raw.WriteString(line)
			if line == "\r\n" {
				break
			}
		}
		received <- raw.String()
		_, _ = io.WriteString(conn, wire)
	}()
	p := prepared(t, "http://"+listener.Addr().String(), get("virtual.test"), true)
	result := (&RawTransport{}).Send(context.Background(), p, nil)
	if !result.Sent || result.StatusCode != 201 || result.ErrorCode != "" || result.ResponseRaw != wire || !bytes.Equal(result.ResponseBytes, []byte(wire)) {
		t.Fatalf("wire changed: %#v", result)
	}
	if actual := <-received; actual != string(p.Raw) {
		t.Fatalf("request rewritten: %q", actual)
	}
}

func TestHTTPErrorAndRedirectAreReturnedOnce(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Location", "/next")
		w.WriteHeader(302)
	}))
	defer target.Close()
	p := prepared(t, target.URL, get("virtual.test"), true)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r := (&RawTransport{}).Send(ctx, p, nil)
	if r.StatusCode != 302 || calls.Load() != 1 || r.ErrorCode != "" {
		t.Fatalf("redirect repeated: %#v calls=%d", r, calls.Load())
	}
	blocked := (&RawTransport{}).Send(ctx, p, func(string) error { return app.ErrBlocked })
	if blocked.Sent || blocked.ErrorCode != "blocked" || calls.Load() != 1 {
		t.Fatal("policy bypass")
	}
}

func TestTLSVerificationTimeoutCancellationAndBoundedBinaryResponse(t *testing.T) {
	tlsTarget := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte{255, 0, 1}) }))
	defer tlsTarget.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r := (&RawTransport{}).Send(ctx, prepared(t, tlsTarget.URL, get("virtual.test"), true), nil)
	if r.Sent || r.ErrorCode != "tls_failed" {
		t.Fatalf("TLS validation bypassed: %#v", r)
	}
	r = (&RawTransport{}).Send(ctx, prepared(t, tlsTarget.URL, get("virtual.test"), false), nil)
	if !r.Binary || !bytes.HasSuffix(r.ResponseBytes, []byte{255, 0, 1}) || !strings.HasSuffix(r.ResponseRaw, "\r\n\r\n") {
		t.Fatalf("binary lost: %#v", r)
	}
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer slow.Close()
	short, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	r = (&RawTransport{}).Send(short, prepared(t, slow.URL, get("slow.test"), true), nil)
	close(release)
	if r.ErrorCode != "timeout" || !r.Sent {
		t.Fatalf("timeout: %#v", r)
	}
	large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(w, strings.NewReader(strings.Repeat("x", app.MaxResponseBytes+5000)))
	}))
	defer large.Close()
	r = (&RawTransport{}).Send(ctx, prepared(t, large.URL, get("large.test"), true), nil)
	if !r.Truncated || r.ByteCount != app.MaxResponseBytes || r.ErrorCode != "response_limit" {
		t.Fatalf("unbounded response: bytes=%d err=%s", r.ByteCount, r.ErrorCode)
	}
}

func TestSystemProxyTunnelKeepsCredentialsOutOfTargetPacket(t *testing.T) {
	received := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.Header.Get("Proxy-Authorization")
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("proxy-user:proxy-secret")) {
			http.Error(w, "bad proxy handshake", 407)
			return
		}
		upstream, e := net.Dial("tcp", r.Host)
		if e != nil {
			http.Error(w, "connect failed", 502)
			return
		}
		defer upstream.Close()
		client, _, e := w.(http.Hijacker).Hijack()
		if e != nil {
			return
		}
		defer client.Close()
		_, _ = io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
		done := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, client); close(done) }()
		_, _ = io.Copy(client, upstream)
		client.Close()
		<-done
	}))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	proxyURL.User = url.UserPassword("proxy-user", "proxy-secret")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r := (&RawTransport{Proxy: func(*http.Request) (*url.URL, error) { return proxyURL, nil }}).Send(ctx, prepared(t, target.URL, get("virtual.test"), true), nil)
	if r.ErrorCode != "" || r.StatusCode != 200 || strings.Contains(r.RequestRaw+r.ResponseRaw, "proxy-secret") {
		t.Fatalf("proxy replay: %#v", r)
	}
	if header := <-received; header != "" {
		t.Fatal("proxy credentials forwarded")
	}
}

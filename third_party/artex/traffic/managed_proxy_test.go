package traffic

import (
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagedProxyAuthenticatesRecordsAndStops(t *testing.T) {
	var leaked atomic.Bool
	target := func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			leaked.Store(true)
		}
		w.Header().Add("X-Audit", "one")
		w.Header().Add("X-Audit", "two")
		_, _ = io.WriteString(w, "managed-response")
	}
	plain := httptest.NewServer(http.HandlerFunc(target))
	defer plain.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(target))
	defer secure.Close()
	recorder, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	const token = "0123456789abcdef0123456789abcdef"
	var resolutions atomic.Int64
	ended := make(chan error, 1)
	go func() {
		ended <- recorder.ServeManaged(listener, token, func(*http.Request) (*url.URL, error) { resolutions.Add(1); return nil, nil })
	}()
	proxyURL, _ := url.Parse("http://" + token + ":x@" + listener.Addr().String())
	cert, err := os.ReadFile(recorder.CACertPath())
	if err != nil {
		t.Fatal(err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(cert) {
		t.Fatal("proxy CA invalid")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	transport.TLSClientConfig = secure.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	transport.TLSClientConfig.RootCAs = pool
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	for _, targetURL := range []string{plain.URL + "/plain", secure.URL + "/secure"} {
		resp, err := client.Get(targetURL)
		if err != nil {
			t.Fatalf("proxied %s: %v", targetURL, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || string(body) != "managed-response" {
			t.Fatalf("proxy response: %d %q", resp.StatusCode, body)
		}
	}
	if leaked.Load() || resolutions.Load() < 2 {
		t.Fatalf("proxy token leaked or upstream skipped: leak=%v resolutions=%d", leaked.Load(), resolutions.Load())
	}
	badProxy, _ := url.Parse("http://" + listener.Addr().String())
	badTransport := http.DefaultTransport.(*http.Transport).Clone()
	badTransport.Proxy = http.ProxyURL(badProxy)
	response, err := (&http.Client{Transport: badTransport, Timeout: 3 * time.Second}).Get(plain.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("unauthenticated proxy request: %d", response.StatusCode)
	}
	// Response capture is synchronous with the proxied response.
	rows, err := recorder.Search("127.0.0.1", 0, 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("recorded exchanges: %+v %v", rows, err)
	}
	req, resp, err := recorder.Get(rows[0].ID)
	if err != nil || !strings.Contains(req, "GET") || !strings.Contains(resp, "managed-response") {
		t.Fatalf("captured exchange: %q %q %v", req, resp, err)
	}
	if !strings.HasPrefix(resp, "HTTP/1.1 200 OK\r\n") || !strings.Contains(resp, "X-Audit: one\r\nX-Audit: two\r\n") || !strings.HasSuffix(resp, "\r\n\r\nmanaged-response") {
		t.Fatalf("raw HTTP capture lost protocol, headers or boundary: %q", resp)
	}
	transport.CloseIdleConnections()
	badTransport.CloseIdleConnections()
	if err = recorder.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-ended:
		if err != nil && err != http.ErrServerClosed {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("managed proxy did not stop")
	}
	if _, err = net.DialTimeout("tcp", listener.Addr().String(), time.Second); err == nil {
		t.Fatal("proxy still accepts connections after close")
	}
}

package aitraffic

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

func clientForLease(t *testing.T, lease *Lease) *http.Client {
	t.Helper()
	proxy, err := url.Parse(lease.ProxyURL())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(lease.CABundle())
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(data) {
		t.Fatal("invalid CA bundle")
	}
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
}
func TestManagedTrafficIsolationPersistenceAndUpstream(t *testing.T) {
	var hit atomic.Int32
	var leaked atomic.Bool
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			leaked.Store(true)
		}
		_, _ = io.WriteString(w, "scoped-response")
	}))
	defer target.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pa:ss%@"))
		if r.Header.Get("Proxy-Authorization") != expected {
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		hit.Add(1)
		if r.Method != "CONNECT" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		remote, err := net.DialTimeout("tcp", r.Host, 3*time.Second)
		if err != nil {
			http.Error(w, "dial", 502)
			return
		}
		client, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			remote.Close()
			return
		}
		_, _ = buf.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buf.Flush()
		done := make(chan struct{})
		go func() { _, _ = io.Copy(remote, buf); _ = remote.Close(); close(done) }()
		_, _ = io.Copy(client, remote)
		_ = client.Close()
		<-done
	}))
	defer upstream.Close()
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	upstreamURL.User = url.UserPassword("user", "pa:ss%@")
	var policy atomic.Pointer[url.URL]
	manager, err := NewManager(t.TempDir(), func(*http.Request) (*url.URL, error) { return policy.Load(), nil })
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	scope := Scope{TenantID: uuid.NewString(), UserID: 7, TaskID: 42}
	first, err := manager.Acquire(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Acquire(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if first.ProxyURL() != second.ProxyURL() {
		t.Fatal("same scope opened separate instances")
	}
	client := clientForLease(t, first)
	request := func(path string) {
		t.Helper()
		resp, err := client.Get(target.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "scoped-response" {
			t.Fatalf("response: %q", body)
		}
	}
	request("/direct")
	policy.Store(upstreamURL)
	client.CloseIdleConnections()
	request("/through-upstream")
	if hit.Load() == 0 || leaked.Load() {
		t.Fatalf("upstream not used or proxy credentials leaked: %d %v", hit.Load(), leaked.Load())
	}
	rows, err := first.Recorder().Search("127.0.0.1", 0, 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("captured rows: %+v %v", rows, err)
	}
	second.Close()
	request("/still-open")
	otherScope := scope
	otherScope.UserID++
	other, err := manager.Acquire(t.Context(), otherScope)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err = other.Recorder().Search("127.0.0.1", 0, 10); err != nil || len(rows) != 0 {
		t.Fatalf("cross-user traffic visible: %+v %v", rows, err)
	}
	other.Close()
	otherScope = scope
	otherScope.TaskID++
	other, err = manager.Acquire(t.Context(), otherScope)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err = other.Recorder().Search("127.0.0.1", 0, 10); err != nil || len(rows) != 0 {
		t.Fatalf("cross-task traffic visible: %+v %v", rows, err)
	}
	other.Close()
	address, _ := url.Parse(first.ProxyURL())
	client.CloseIdleConnections()
	first.Close()
	if conn, err := net.DialTimeout("tcp", address.Host, time.Second); err == nil {
		conn.Close()
		t.Fatal("released proxy still accepts connections")
	}
	reopened, err := manager.Acquire(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rows, err = reopened.Recorder().Search("127.0.0.1", 0, 10)
	if err != nil || len(rows) != 3 {
		t.Fatalf("traffic did not survive restart: %+v %v", rows, err)
	}
	if _, resp, err := reopened.Recorder().Get(rows[0].ID); err != nil || !strings.Contains(resp, "scoped-response") {
		t.Fatalf("recorded response: %q %v", resp, err)
	}
	file, err := os.Stat(reopened.CABundle())
	if err != nil || file.Mode().Perm() != 0600 {
		t.Fatalf("CA bundle permissions: %v %v", file, err)
	}
}
func TestAcquireCancelledDuringCrossProcessInitialization(t *testing.T) {
	root := t.TempDir()
	scope := Scope{TenantID: uuid.NewString(), UserID: 5, TaskID: 19}
	dir := filepath.Join(root, scope.TenantID, "user-5", "traffic", "task-19")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, ".initialize.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	m, err := NewManager(root, func(*http.Request) (*url.URL, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := m.Acquire(ctx, scope); done <- err }()
	other := scope
	other.TaskID++
	lease, err := m.Acquire(t.Context(), other)
	if err != nil {
		t.Fatal(err)
	}
	lease.Close()
	select {
	case err := <-done:
		if err != context.DeadlineExceeded {
			t.Fatalf("lock wait: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("initialization wait did not cancel")
	}
}
func TestConcurrentAcquireSharesProxy(t *testing.T) {
	m, err := NewManager(t.TempDir(), func(*http.Request) (*url.URL, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	scope := Scope{TenantID: uuid.NewString(), UserID: 3, TaskID: 1}
	const n = 10
	items := make([]*Lease, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) { defer wg.Done(); items[i], errs[i] = m.Acquire(t.Context(), scope) }(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range items {
		if item == nil || item.ProxyURL() != items[0].ProxyURL() {
			t.Fatal("concurrent acquisition diverged")
		}
	}
	for _, item := range items {
		item.Close()
	}
}

func TestManagedRecorderVerifiesHTTPSUpstreamProxy(t *testing.T) {
	var upstreamHits atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer upstream.Close()
	proxyURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(t.TempDir(), func(*http.Request) (*url.URL, error) { return proxyURL, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	lease, err := manager.Acquire(t.Context(), Scope{TenantID: uuid.NewString(), UserID: 1, TaskID: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("untrusted upstream was bypassed")
		w.WriteHeader(200)
	}))
	defer target.Close()
	client := clientForLease(t, lease)
	response, err := client.Get(target.URL)
	if err == nil {
		response.Body.Close()
		if response.StatusCode < 500 {
			t.Fatalf("untrusted HTTPS upstream accepted: %d", response.StatusCode)
		}
	}
	if upstreamHits.Load() != 0 {
		t.Fatalf("TLS verification sent requests to an untrusted upstream: %d", upstreamHits.Load())
	}
}

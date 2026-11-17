package deploymentnetwork

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
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
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func fixtureHelper(t *testing.T, daemon http.Handler, environment map[string]string, configure ...func(*Config)) (*Helper, *http.Client) {
	t.Helper()
	root, err := os.MkdirTemp("", "sky-network-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	resolv, hosts := filepath.Join(root, "resolv.conf"), filepath.Join(root, "hosts")
	if err := os.WriteFile(resolv, []byte("nameserver 127.0.0.53\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hosts, []byte("127.0.0.1 private.skyshield.test\n192.168.40.8 fixture.corp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	upstream := filepath.Join(root, "daemon.sock")
	listener, err := net.Listen("unix", upstream)
	if err != nil {
		t.Fatal(err)
	}
	if daemon == nil {
		daemon = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("daemon")) })
	}
	server := &http.Server{Handler: daemon}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Close() })
	cfg := Config{BindIP: "127.0.0.1", DNSPort: freePort(t), ProxyPort: freePort(t), AllowedCIDRs: []string{"127.0.0.0/8"}, ResolvConf: resolv, HostsFile: hosts, DockerSocket: filepath.Join(root, "relay", "docker.sock"), DockerUpstream: upstream, ProxyToken: strings.Repeat("a", 64), ProxyEnvironment: environment, SearchDomains: []string{"corp"}, EngineNetworkMode: "bridge"}
	for _, fn := range configure {
		fn(&cfg)
	}
	encoded, _ := json.Marshal(cfg)
	configPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(configPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	h, err := Start(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", h.cfg.DockerSocket)
	}}}
	return h, client
}

func TestHostDNSAndHTTPUsePrivateHostsWithoutPublicFallback(t *testing.T) {
	h, _ := fixtureHelper(t, nil, nil)
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(h.cfg.BindIP, fmt.Sprint(h.cfg.DNSPort)))
	}}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	addresses, err := resolver.LookupHost(ctx, "fixture.corp")
	if err != nil || len(addresses) != 1 || addresses[0] != "192.168.40.8" {
		t.Fatalf("inherited private host: %v %v", addresses, err)
	}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("host relay credential leaked to target")
		}
		_, _ = w.Write([]byte("private-http-fixture"))
	}))
	defer origin.Close()
	proxy, _ := url.Parse(h.proxyURL())
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(origin.URL, "http://"))
	response, err := client.Get("http://private.skyshield.test:" + port + "/")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || string(data) != "private-http-fixture" {
		t.Fatalf("private HTTP: %d %s", response.StatusCode, data)
	}
	unauth, _ := url.Parse("http://" + net.JoinHostPort(h.cfg.BindIP, fmt.Sprint(h.cfg.ProxyPort)))
	client.Transport = &http.Transport{Proxy: http.ProxyURL(unauth)}
	response, err = client.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("unauthenticated relay status %d", response.StatusCode)
	}
	if err := os.WriteFile(h.cfg.ResolvConf, []byte("# no DNS configured\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadResolvers(h.cfg.ResolvConf); err == nil {
		t.Fatal("missing host DNS was silently replaced")
	}
}

func TestHostRelayKeepsProxyCredentialsPrivateAndRespectsNoProxy(t *testing.T) {
	var proxyCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		if r.Header.Get("Proxy-Authorization") != "Basic Zml4dHVyZTpzZWNyZXQ=" {
			t.Error("upstream proxy did not receive its own fixture credentials")
		}
		_, _ = w.Write([]byte("upstream-fixture"))
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	u.User = url.UserPassword("fixture", "secret")
	h, _ := fixtureHelper(t, nil, map[string]string{"HTTP_PROXY": u.String(), "NO_PROXY": "private.skyshield.test"})
	p, _ := url.Parse(h.proxyURL())
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(p)}}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("direct-fixture")) }))
	defer origin.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(origin.URL, "http://"))
	for _, check := range []struct{ target, want string }{{"http://private.skyshield.test:" + port, "direct-fixture"}, {"http://unresolvable.skyshield.test/", "upstream-fixture"}} {
		response, err := client.Get(check.target)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if string(body) != check.want {
			t.Fatalf("proxy inheritance: %q, want %q", body, check.want)
		}
	}
	if proxyCalls.Load() != 1 {
		t.Fatalf("NO_PROXY was ignored: proxy calls=%d", proxyCalls.Load())
	}
}

func TestHostRelayConnectPreservesTLSVerification(t *testing.T) {
	h, _ := fixtureHelper(t, nil, nil)
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("tls-host-fixture")) }))
	defer origin.Close()
	pool := x509.NewCertPool()
	pool.AddCert(origin.Certificate())
	proxy, _ := url.Parse(h.proxyURL())
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	response, err := client.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "tls-host-fixture" {
		t.Fatalf("CONNECT tunnel failed: %q", body)
	}
}

func TestHostExecutionCanResolveOnlyItsOwnDockerServices(t *testing.T) {
	h, _ := fixtureHelper(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/containers/json" {
			t.Errorf("unexpected service discovery path %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `[{"Labels":{"com.docker.compose.project":"fixture","com.docker.compose.service":"postgres"},"HostConfig":{"NetworkMode":"fixture_default"},"NetworkSettings":{"Networks":{"fixture_default":{"IPAddress":"172.31.41.2"}}}},{"Labels":{"com.docker.compose.project":"fixture","com.docker.compose.service":"server"},"HostConfig":{"NetworkMode":"host"}},{"Labels":{"com.docker.compose.project":"foreign","com.docker.compose.service":"postgres"},"NetworkSettings":{"Networks":{"fixture_default":{"IPAddress":"172.31.41.99"}}}}]`)
	}), nil, func(c *Config) { c.ProjectName = "fixture"; c.EngineNetworkMode = "host" })
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(h.cfg.BindIP, fmt.Sprint(h.cfg.DNSPort)))
	}}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	for _, test := range []struct{ name, ip string }{{"postgres.", "172.31.41.2"}, {"server.", "127.0.0.1"}} {
		ips, err := resolver.LookupHost(ctx, test.name)
		if err != nil || len(ips) != 1 || ips[0] != test.ip {
			t.Fatalf("private service resolution: %s %v %v", test.name, ips, err)
		}
	}
}

func engineCreateFixture() []byte {
	return []byte(`{"Image":"fixture@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","Env":["FIXTURE=preserved"],"Labels":{"task":"preserved"},"HostConfig":{"NetworkMode":"default","Mounts":[{"Target":"/run/lunafox/context/execution.pb"},{"Target":"/run/lunafox/credential/token"},{"Target":"/run/lunafox/socket"}]}}`)
}

func TestDynamicEngineGetsInheritedDNSAndExplicitSettingsArePreserved(t *testing.T) {
	received := make(chan []byte, 4)
	h, client := fixtureHelper(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- body
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"Id":"fixture-container"}`))
	}), nil)
	response, err := client.Post("http://docker/v1.47/containers/create?name=fixture", "application/json", bytes.NewReader(engineCreateFixture()))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	var updated struct {
		Env        []string
		Labels     map[string]string
		HostConfig struct {
			Dns, DnsSearch []string
			NetworkMode    string
		}
	}
	if err := json.Unmarshal(<-received, &updated); err != nil {
		t.Fatal(err)
	}
	if len(updated.HostConfig.Dns) != 1 || updated.HostConfig.Dns[0] != h.cfg.BindIP || strings.Join(updated.HostConfig.DnsSearch, ",") != "corp" || updated.HostConfig.NetworkMode != "default" || updated.Labels["task"] != "preserved" {
		t.Fatalf("Engine settings drift: %+v", updated)
	}
	if !strings.Contains(strings.Join(updated.Env, "\n"), "HTTP_PROXY="+h.proxyURL()) {
		t.Fatal("Engine lost host relay configuration")
	}
	unrelated := []byte(`{"Image":"unrelated","HostConfig":{"NetworkMode":"none"}}`)
	response, err = client.Post("http://docker/v1.47/containers/create", "application/json", bytes.NewReader(unrelated))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if got := <-received; !bytes.Equal(got, unrelated) {
		t.Fatal("unrelated Docker container was modified")
	}
	var input map[string]any
	_ = json.Unmarshal(engineCreateFixture(), &input)
	input["Env"] = []string{"HTTP_PROXY=http://explicit.invalid:1234", "NO_PROXY=.explicit.invalid"}
	input["HostConfig"].(map[string]any)["Dns"] = []string{"192.168.1.53"}
	payload, _ := json.Marshal(input)
	payload, err = h.inheritEngineNetwork(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), "192.168.1.53") || !strings.Contains(string(payload), "HTTP_PROXY=http://explicit.invalid:1234") {
		t.Fatal("explicit Engine network policy was replaced")
	}
}

func TestDockerProxyStreamsAndPreservesHijackedConnections(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	h, client := fixtureHelper(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stream" {
			_, _ = w.Write([]byte("first\n"))
			w.(http.Flusher).Flush()
			<-release
			_, _ = w.Write([]byte("last\n"))
			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
		_ = rw.Flush()
		_, _ = io.Copy(conn, rw.Reader)
	}), nil)
	response, err := client.Get("http://docker/stream")
	if err != nil {
		t.Fatal(err)
	}
	first, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || first != "first\n" {
		t.Fatalf("Docker progress was buffered: %q %v", first, err)
	}
	once.Do(func() { close(release) })
	response.Body.Close()
	conn, err := net.DialTimeout("unix", h.cfg.DockerSocket, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = io.WriteString(conn, "GET /upgrade HTTP/1.1\r\nHost: docker\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
	reader := bufio.NewReader(conn)
	upgraded, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
	if err != nil || upgraded.StatusCode != 101 {
		t.Fatalf("Docker upgrade failed: %v %v", upgraded, err)
	}
	_, _ = io.WriteString(conn, "fixture")
	buffer := make([]byte, 7)
	if _, err := io.ReadFull(reader, buffer); err != nil || string(buffer) != "fixture" {
		t.Fatalf("Docker attach stream lost bytes: %q %v", buffer, err)
	}
}

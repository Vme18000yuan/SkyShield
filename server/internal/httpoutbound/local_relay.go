package httpoutbound

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/google/uuid"
	socks "golang.org/x/net/proxy"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// LocalRelay keeps upstream credentials out of tool environments and applies
// proxy changes to subsequent command requests without replacing the worker.
type LocalRelay struct {
	server    *http.Server
	listener  net.Listener
	transport *http.Transport
	url       string
	mu        sync.Mutex
	tunnels   map[net.Conn]struct{}
	proxy     func(*http.Request) (*url.URL, error)
	caBundle  string
}

func NewLocalRelay(ctx context.Context, fallback ...string) (*LocalRelay, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	token := uuid.NewString()
	relay := &LocalRelay{listener: listener, tunnels: map[net.Conn]struct{}{}, proxy: DefaultProxyController.Proxy}
	if len(fallback) > 0 && fallback[0] != "" {
		relay.proxy = DefaultProxyController.WithProfileProxy(fallback[0])
	}
	relay.proxy = RecordingProxy(ctx, relay.proxy)
	relay.caBundle = RecordingCABundle(ctx)
	relay.url = "http://" + token + ":x@" + listener.Addr().String()
	relay.transport = http.DefaultTransport.(*http.Transport).Clone()
	relay.transport.Proxy = relay.proxy
	relay.server = &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected := "Basic " + base64.StdEncoding.EncodeToString([]byte(token+":x"))
		if r.Header.Get("Proxy-Authorization") != expected {
			w.Header().Set("Proxy-Authenticate", `Basic realm="AI tools"`)
			w.WriteHeader(407)
			return
		}
		if r.Method == http.MethodConnect {
			relay.connect(w, r)
			return
		}
		out := r.Clone(r.Context())
		out.RequestURI = ""
		out.Header.Del("Proxy-Authorization")
		out.Header.Del("Proxy-Connection")
		response, err := relay.transport.RoundTrip(out)
		if err != nil {
			http.Error(w, "HTTP 请求失败", 502)
			return
		}
		defer response.Body.Close()
		for key, values := range response.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	})}
	go func() { _ = relay.server.Serve(listener) }()
	go func() { <-ctx.Done(); relay.Close() }()
	return relay, nil
}
func (r *LocalRelay) URL() string      { return r.url }
func (r *LocalRelay) CABundle() string { return r.caBundle }
func (r *LocalRelay) Environment() []string {
	out := []string{}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "ALL_PROXY", "all_proxy"} {
		out = append(out, key+"="+r.url)
	}
	out = append(out, "NO_PROXY=", "no_proxy=", "NODE_USE_ENV_PROXY=1")
	if r.caBundle != "" {
		for _, key := range []string{"SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO", "NODE_EXTRA_CA_CERTS"} {
			out = append(out, key+"="+r.caBundle)
		}
	}
	return out
}
func (r *LocalRelay) Close() {
	_ = r.server.Close()
	r.transport.CloseIdleConnections()
	r.mu.Lock()
	defer r.mu.Unlock()
	for conn := range r.tunnels {
		_ = conn.Close()
	}
}
func (r *LocalRelay) connect(w http.ResponseWriter, request *http.Request) {
	target := request.Host
	if _, _, err := net.SplitHostPort(target); err != nil {
		http.Error(w, "CONNECT 必须指定端口", 400)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
	defer cancel()
	upstream, err := r.proxy(&http.Request{URL: &url.URL{Scheme: "https", Host: target}})
	if err != nil {
		http.Error(w, "代理配置不可用", 502)
		return
	}
	conn, err := dialTunnel(ctx, target, upstream)
	if err != nil {
		http.Error(w, "连接失败", 502)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		conn.Close()
		http.Error(w, "CONNECT 不可用", 500)
		return
	}
	client, buffer, err := hijacker.Hijack()
	if err != nil {
		conn.Close()
		return
	}
	r.mu.Lock()
	r.tunnels[client] = struct{}{}
	r.tunnels[conn] = struct{}{}
	r.mu.Unlock()
	defer func() {
		client.Close()
		conn.Close()
		r.mu.Lock()
		delete(r.tunnels, client)
		delete(r.tunnels, conn)
		r.mu.Unlock()
	}()
	_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
	_ = buffer.Flush()
	done := make(chan struct{}, 1)
	go func() { _, _ = io.Copy(conn, buffer); done <- struct{}{} }()
	_, _ = io.Copy(client, conn)
	conn.Close()
	client.Close()
	<-done
}
func dialTunnel(ctx context.Context, target string, proxyURL *url.URL) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	if proxyURL == nil {
		return dialer.DialContext(ctx, "tcp", target)
	}
	if strings.HasPrefix(proxyURL.Scheme, "socks5") {
		dial, err := socks.FromURL(proxyURL, dialer)
		if err != nil {
			return nil, err
		}
		if contextDial, ok := dial.(socks.ContextDialer); ok {
			return contextDial.DialContext(ctx, "tcp", target)
		}
		return nil, errors.New("SOCKS 代理不支持超时")
	}
	address := proxyURL.Host
	if proxyURL.Port() == "" {
		port := "80"
		if proxyURL.Scheme == "https" {
			port = "443"
		}
		address = net.JoinHostPort(proxyURL.Hostname(), port)
	}
	var conn net.Conn
	var err error
	if proxyURL.Scheme == "https" {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: proxyURL.Hostname(), MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", address)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	connect := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: make(http.Header)}
	if proxyURL.User != nil {
		password, _ := proxyURL.User.Password()
		connect.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(proxyURL.User.Username()+":"+password)))
	}
	if err = connect.Write(conn); err != nil {
		conn.Close()
		return nil, err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, connect)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if response.StatusCode != 200 {
		conn.Close()
		return nil, fmt.Errorf("upstream CONNECT: %d", response.StatusCode)
	}
	_ = conn.SetDeadline(time.Time{})
	return &bufferedTunnel{Conn: conn, reader: reader}, nil
}

type bufferedTunnel struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedTunnel) Read(value []byte) (int, error) { return c.reader.Read(value) }

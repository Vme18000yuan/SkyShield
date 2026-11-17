package deploymentnetwork

import (
	"bufio"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/http/httpproxy"
	socks "golang.org/x/net/proxy"
)

func (h *Helper) proxyURL() string {
	return "http://skyshield:" + h.cfg.ProxyToken + "@" + net.JoinHostPort(h.cfg.BindIP, fmt.Sprint(h.cfg.ProxyPort))
}

func (h *Helper) upstreamProxy() func(*url.URL) (*url.URL, error) {
	get := func(upper, lower string) string {
		if value := h.cfg.ProxyEnvironment[upper]; value != "" {
			return value
		}
		return h.cfg.ProxyEnvironment[lower]
	}
	all := get("ALL_PROXY", "all_proxy")
	httpProxy, httpsProxy := get("HTTP_PROXY", "http_proxy"), get("HTTPS_PROXY", "https_proxy")
	if httpProxy == "" {
		httpProxy = all
	}
	if httpsProxy == "" {
		httpsProxy = all
	}
	return (&httpproxy.Config{HTTPProxy: httpProxy, HTTPSProxy: httpsProxy, NoProxy: get("NO_PROXY", "no_proxy")}).ProxyFunc()
}

func (h *Helper) hostDialer() *net.Dialer {
	return &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Resolver: &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, net.JoinHostPort(h.cfg.BindIP, fmt.Sprint(h.cfg.DNSPort)))
	}}}
}

func (h *Helper) dialHost(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if net.ParseIP(host) == nil {
		if hosts, err := readHosts(h.cfg.HostsFile); err == nil {
			if addresses := hosts[strings.TrimSuffix(strings.ToLower(host), ".")]; len(addresses) > 0 {
				var last error
				for _, ip := range addresses {
					conn, err := h.hostDialer().DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
					if err == nil {
						return conn, nil
					}
					last = err
				}
				return nil, last
			}
		}
	}
	return h.hostDialer().DialContext(ctx, network, address)
}

func (h *Helper) proxyHandler() http.Handler {
	upstream := h.upstreamProxy()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = h.dialHost
	transport.Proxy = func(r *http.Request) (*url.URL, error) { return upstream(r.URL) }
	forward := &httputil.ReverseProxy{Transport: transport, FlushInterval: -1, Rewrite: func(p *httputil.ProxyRequest) {
		p.Out.Header.Del("Proxy-Authorization")
		p.Out.Header.Del("Proxy-Connection")
		// Preserve explicit test headers without adding the relay's own identity.
		for _, key := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
			if values := p.In.Header.Values(key); len(values) > 0 {
				p.Out.Header[key] = append([]string(nil), values...)
			}
		}
	}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) { http.Error(w, "host relay request failed", 502) }}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.allowed(r.RemoteAddr) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("skyshield:"+h.cfg.ProxyToken))
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Proxy-Authorization")), []byte(expected)) != 1 {
			w.Header().Set("Proxy-Authenticate", `Basic realm="SkyShield host network"`)
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		if r.Method == http.MethodConnect {
			target := &url.URL{Scheme: "https", Host: r.Host}
			selected, err := upstream(target)
			if err != nil {
				http.Error(w, "host relay proxy configuration is invalid", 502)
				return
			}
			conn, err := h.dialTunnel(r.Context(), r.Host, selected)
			if err != nil {
				http.Error(w, "host relay connection failed", 502)
				return
			}
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				conn.Close()
				http.Error(w, "tunneling unavailable", 500)
				return
			}
			client, buffered, err := hijacker.Hijack()
			if err != nil {
				conn.Close()
				return
			}
			if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
				client.Close()
				conn.Close()
				return
			}
			if err := buffered.Flush(); err != nil {
				client.Close()
				conn.Close()
				return
			}
			h.tunnel(client, conn, buffered.Reader)
			return
		}
		if r.URL.Scheme != "http" || r.URL.Host == "" {
			http.Error(w, "absolute HTTP proxy URL required", 400)
			return
		}
		forward.ServeHTTP(w, r)
	})
}

func (h *Helper) dialTunnel(ctx context.Context, target string, upstream *url.URL) (net.Conn, error) {
	if upstream == nil {
		return h.dialHost(ctx, "tcp", target)
	}
	if strings.HasPrefix(upstream.Scheme, "socks5") {
		dialer, err := socks.FromURL(upstream, h.hostDialer())
		if err != nil {
			return nil, err
		}
		if dialer, ok := dialer.(socks.ContextDialer); ok {
			return dialer.DialContext(ctx, "tcp", target)
		}
		return nil, fmt.Errorf("SOCKS proxy does not support cancellation")
	}
	port := upstream.Port()
	if port == "" {
		port = "80"
		if upstream.Scheme == "https" {
			port = "443"
		}
	}
	conn, err := h.dialHost(ctx, "tcp", net.JoinHostPort(upstream.Hostname(), port))
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	if upstream.Scheme == "https" {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: upstream.Hostname(), MinVersion: tls.VersionTLS12})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, err
		}
		conn = tlsConn
	}
	request := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: make(http.Header)}
	if upstream.User != nil {
		password, _ := upstream.User.Password()
		request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(upstream.User.Username()+":"+password)))
	}
	if err := request.Write(conn); err != nil {
		conn.Close()
		return nil, err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if response.StatusCode != 200 {
		conn.Close()
		return nil, fmt.Errorf("upstream CONNECT failed: %d", response.StatusCode)
	}
	_ = conn.SetDeadline(time.Time{})
	return &bufferedConnection{Conn: conn, reader: reader}, nil
}

type bufferedConnection struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConnection) Read(p []byte) (int, error) { return c.reader.Read(p) }

func (h *Helper) tunnel(client, remote net.Conn, reader io.Reader) {
	h.mu.Lock()
	h.tunnels[client] = struct{}{}
	h.tunnels[remote] = struct{}{}
	h.mu.Unlock()
	defer func() {
		client.Close()
		remote.Close()
		h.mu.Lock()
		delete(h.tunnels, client)
		delete(h.tunnels, remote)
		h.mu.Unlock()
	}()
	done := make(chan struct{}, 1)
	go func() { _, _ = io.Copy(remote, reader); done <- struct{}{} }()
	_, _ = io.Copy(client, remote)
	remote.Close()
	client.Close()
	<-done
}

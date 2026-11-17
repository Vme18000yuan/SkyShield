package deploymentnetwork

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Helper struct {
	cfg          Config
	ctx          context.Context
	cancel       context.CancelFunc
	udp          net.PacketConn
	dnsTCP       net.Listener
	proxy        *http.Server
	docker       *http.Server
	dockerListen net.Listener
	clients      []*net.IPNet
	workers      chan struct{}
	mu           sync.Mutex
	tunnels      map[net.Conn]struct{}
	once         sync.Once
	deniedOnce   sync.Once
	serviceMu    sync.Mutex
	services     map[string][]net.IP
	serviceUntil time.Time
}

func Start(ctx context.Context, cfg Config) (*Helper, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if _, err := ReadResolvers(cfg.ResolvConf); err != nil {
		return nil, err
	}
	if _, err := readHosts(cfg.HostsFile); err != nil {
		return nil, fmt.Errorf("read host overrides: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	h := &Helper{cfg: cfg, ctx: ctx, cancel: cancel, workers: make(chan struct{}, 128), tunnels: map[net.Conn]struct{}{}}
	for _, raw := range cfg.AllowedCIDRs {
		_, subnet, _ := net.ParseCIDR(raw)
		h.clients = append(h.clients, subnet)
	}
	success := false
	defer func() {
		if !success {
			h.Close()
		}
	}()
	address := net.JoinHostPort(cfg.BindIP, fmt.Sprint(cfg.DNSPort))
	var err error
	h.udp, err = net.ListenPacket("udp", address)
	if err != nil {
		return nil, fmt.Errorf("bind host DNS UDP interface: %w", err)
	}
	h.dnsTCP, err = net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("bind host DNS TCP interface: %w", err)
	}
	proxyListener, err := net.Listen("tcp", net.JoinHostPort(cfg.BindIP, fmt.Sprint(cfg.ProxyPort)))
	if err != nil {
		return nil, fmt.Errorf("bind host HTTP relay interface: %w", err)
	}
	h.proxy = &http.Server{Handler: h.proxyHandler(), ReadHeaderTimeout: 10 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() { _ = h.proxy.Serve(proxyListener) }()
	if err := os.MkdirAll(filepath.Dir(cfg.DockerSocket), 0o700); err != nil {
		return nil, err
	}
	if old, err := os.Lstat(cfg.DockerSocket); err == nil {
		if old.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("network Docker socket path is occupied by a non-socket")
		}
		if connection, err := net.DialTimeout("unix", cfg.DockerSocket, 200*time.Millisecond); err == nil {
			connection.Close()
			return nil, fmt.Errorf("network Docker socket is already active")
		}
		if err := os.Remove(cfg.DockerSocket); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	h.dockerListen, err = net.Listen("unix", cfg.DockerSocket)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(cfg.DockerSocket, 0o600); err != nil {
		return nil, err
	}
	h.docker = &http.Server{Handler: h.dockerHandler(), ReadHeaderTimeout: 10 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() { _ = h.docker.Serve(h.dockerListen) }()
	go h.serveDNSUDP()
	go h.serveDNSTCP()
	go func() { <-ctx.Done(); h.Close() }()
	success = true
	return h, nil
}

func (h *Helper) allowed(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		h.deniedOnce.Do(func() { fmt.Fprintf(os.Stderr, "Network adapter rejected an invalid client address: %q\n", address) })
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		h.deniedOnce.Do(func() { fmt.Fprintf(os.Stderr, "Network adapter rejected an invalid client IP: %q\n", host) })
		return false
	}
	for _, subnet := range h.clients {
		if subnet.Contains(ip) {
			return true
		}
	}
	// Host VPN/TUN software may SNAT a local or Docker connection to one of
	// the machine's own addresses. Follow interface changes without allowing LANs.
	if addresses, err := net.InterfaceAddrs(); err == nil {
		for _, address := range addresses {
			if own, _, err := net.ParseCIDR(address.String()); err == nil && own.Equal(ip) {
				return true
			}
		}
	}
	h.deniedOnce.Do(func() {
		fmt.Fprintln(os.Stderr, "Network adapter rejected a client outside its configured networks:", host)
	})
	return false
}

func (h *Helper) Close() {
	h.once.Do(func() {
		h.cancel()
		if h.udp != nil {
			_ = h.udp.Close()
		}
		if h.dnsTCP != nil {
			_ = h.dnsTCP.Close()
		}
		if h.proxy != nil {
			_ = h.proxy.Close()
		}
		if h.docker != nil {
			_ = h.docker.Close()
		}
		if h.dockerListen != nil {
			_ = h.dockerListen.Close()
		}
		h.mu.Lock()
		for conn := range h.tunnels {
			_ = conn.Close()
		}
		h.mu.Unlock()
	})
}

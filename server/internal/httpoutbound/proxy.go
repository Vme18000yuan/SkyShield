// Package httpoutbound owns the Server process-wide outbound HTTP proxy.
package httpoutbound

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
)

type proxyState struct {
	override bool
	enabled  bool
	proxyURL *url.URL
}

// ProxyController supplies one dynamic proxy policy to every Server HTTP
// client that uses net/http's default transport. Updating the state changes
// subsequent requests without restarting the process.
type ProxyController struct {
	state atomic.Pointer[proxyState]
}

func NewProxyController() *ProxyController {
	controller := &ProxyController{}
	controller.state.Store(&proxyState{})
	return controller
}

// Set stores a system-owned proxy override. A disabled override intentionally
// bypasses both this proxy and environment proxy variables.
func (controller *ProxyController) Set(enabled bool, rawProxyURL string) error {
	if controller == nil {
		return fmt.Errorf("HTTP proxy controller is required")
	}
	state := &proxyState{override: true, enabled: enabled}
	if strings.TrimSpace(rawProxyURL) != "" {
		parsed, err := ParseProxyURL(rawProxyURL)
		if err != nil {
			return err
		}
		state.proxyURL = parsed
	}
	if enabled && state.proxyURL == nil {
		return fmt.Errorf("an HTTP proxy URL is required when the proxy is enabled")
	}
	controller.state.Store(state)
	return nil
}

// UseEnvironment restores the default HTTP_PROXY/HTTPS_PROXY behavior when
// the application has no persisted system proxy setting.
func (controller *ProxyController) UseEnvironment() {
	if controller != nil {
		controller.state.Store(&proxyState{})
	}
}

func (controller *ProxyController) Proxy(request *http.Request) (*url.URL, error) {
	if request == nil || request.URL == nil {
		return nil, nil
	}
	if isInternalServiceHost(request.URL.Hostname()) {
		return nil, nil
	}
	if controller == nil {
		return http.ProxyFromEnvironment(request)
	}
	state := controller.state.Load()
	if state == nil || !state.override {
		return http.ProxyFromEnvironment(request)
	}
	if !state.enabled {
		return nil, nil
	}
	return state.proxyURL, nil
}

// WithFallback keeps an explicit provider proxy until system settings supply an
// override; both enabled and disabled system settings then take precedence.
func (controller *ProxyController) WithFallback(raw string) func(*http.Request) (*url.URL, error) {
	return func(request *http.Request) (*url.URL, error) {
		if request == nil || request.URL == nil || isInternalServiceHost(request.URL.Hostname()) {
			return nil, nil
		}
		if controller != nil {
			if state := controller.state.Load(); state != nil && state.override {
				return controller.Proxy(request)
			}
		}
		if strings.TrimSpace(raw) != "" {
			return url.Parse(raw)
		}
		return controller.Proxy(request)
	}
}

// WithProfileProxy preserves ARTEX's explicit model proxy when the system
// proxy is disabled. An enabled system proxy always has precedence.
func (controller *ProxyController) WithProfileProxy(raw string) func(*http.Request) (*url.URL, error) {
	return func(request *http.Request) (*url.URL, error) {
		if request == nil || request.URL == nil || isInternalServiceHost(request.URL.Hostname()) {
			return nil, nil
		}
		if controller != nil {
			if state := controller.state.Load(); state != nil && state.override && state.enabled {
				return controller.Proxy(request)
			}
		}
		if strings.TrimSpace(raw) != "" {
			return url.Parse(raw)
		}
		return controller.Proxy(request)
	}
}

// ChildEnvironment returns proxy variables for network-aware helper processes
// such as the Server-owned Git source synchronizer. The override flag tells
// callers to remove inherited proxy variables before applying these values.
func (controller *ProxyController) ChildEnvironment() (override bool, values map[string]string) {
	if controller == nil {
		return false, nil
	}
	state := controller.state.Load()
	if state == nil || !state.override {
		return false, nil
	}
	if !state.enabled || state.proxyURL == nil {
		return true, nil
	}
	proxyURL := state.proxyURL.String()
	return true, map[string]string{
		"HTTP_PROXY":  proxyURL,
		"HTTPS_PROXY": proxyURL,
		"http_proxy":  proxyURL,
		"https_proxy": proxyURL,
	}
}

var DefaultProxyController = NewProxyController()

// InstallDefaultProxy installs the dynamic policy before any Server services
// are built. net/http clients with a nil Transport and libraries that defer to
// http.DefaultTransport will then observe the same live setting.
func InstallDefaultProxy() error {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok || transport == nil {
		return fmt.Errorf("default HTTP transport must be *http.Transport")
	}
	transport.Proxy = DefaultProxyController.Proxy
	return nil
}

// ParseProxyURL accepts HTTP and HTTPS forward proxies, optionally with
// Basic authentication in URL userinfo.
func ParseProxyURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 || strings.ContainsAny(raw, "\r\n\t\x00 ") {
		return nil, fmt.Errorf("proxy URL is invalid")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || (!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) || parsed.Opaque != "" || parsed.Host == "" || parsed.Hostname() == "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, fmt.Errorf("proxy URL must be an http or https proxy address without a path, query, or fragment")
	}
	if port := parsed.Port(); port != "" {
		if parsedPort, parseErr := strconv.Atoi(port); parseErr != nil || parsedPort < 1 || parsedPort > 65535 {
			return nil, fmt.Errorf("proxy URL port is invalid")
		}
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Path = ""
	parsed.RawPath = ""
	return parsed, nil
}

func ProxyAddress(raw string) string {
	parsed, err := ParseProxyURL(raw)
	if err != nil {
		return ""
	}
	parsed.User = nil
	return parsed.String()
}

func isInternalServiceHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || host == "server" || host == "nginx" || host == "loki" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}

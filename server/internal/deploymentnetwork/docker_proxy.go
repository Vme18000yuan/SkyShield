package deploymentnetwork

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"
)

// dockerHandler preserves the Docker API, including streaming and protocol
// upgrades. Only Engine API v2 workload creates receive inherited defaults;
// the closed Agent executable and unrelated container specifications are intact.
func (h *Helper) dockerHandler() http.Handler {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "unix", h.cfg.DockerUpstream)
	}, MaxIdleConns: 32, IdleConnTimeout: 90 * time.Second}
	proxy := &httputil.ReverseProxy{Director: func(r *http.Request) { r.URL.Scheme = "http"; r.URL.Host = "docker" }, Transport: transport, FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "Docker daemon is unavailable", http.StatusBadGateway)
		}}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/_skyshield/health" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("OK"))
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/containers/create") {
			payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
			if err != nil {
				http.Error(w, "container specification exceeds limit", http.StatusRequestEntityTooLarge)
				return
			}
			_ = r.Body.Close()
			updated, err := h.inheritEngineNetwork(payload)
			if err != nil {
				http.Error(w, "invalid Engine network specification", http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(updated))
			r.ContentLength = int64(len(updated))
			r.Header.Set("Content-Length", fmt.Sprint(len(updated)))
		}
		proxy.ServeHTTP(w, r)
	})
}

func (h *Helper) inheritEngineNetwork(payload []byte) ([]byte, error) {
	var spec map[string]json.RawMessage
	if err := json.Unmarshal(payload, &spec); err != nil {
		return nil, err
	}
	var host map[string]json.RawMessage
	if raw := spec["HostConfig"]; len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &host); err != nil {
			return nil, err
		}
	}
	if host == nil {
		return payload, nil
	}
	var mounts []struct{ Target string }
	if err := json.Unmarshal(host["Mounts"], &mounts); err != nil {
		return payload, nil
	}
	contextMount, socketMount, credentialMount := false, false, false
	for _, mount := range mounts {
		switch mount.Target {
		case "/run/lunafox/context", "/run/lunafox/context/execution.pb":
			contextMount = true
		case "/run/lunafox/socket", "/run/lunafox/socket/engine.sock":
			socketMount = true
		case "/run/lunafox/credential", "/run/lunafox/credential/token":
			credentialMount = true
		}
	}
	if !contextMount || !socketMount || !credentialMount {
		return payload, nil
	}
	set := func(key string, value any) { host[key], _ = json.Marshal(value) }
	var dns []string
	if err := json.Unmarshal(host["Dns"], &dns); err != nil && len(host["Dns"]) > 0 {
		return nil, err
	}
	if len(dns) == 0 {
		set("Dns", []string{h.cfg.BindIP})
	}
	var search []string
	if err := json.Unmarshal(host["DnsSearch"], &search); err != nil && len(host["DnsSearch"]) > 0 {
		return nil, err
	}
	if len(search) == 0 && len(h.cfg.SearchDomains) > 0 {
		set("DnsSearch", h.cfg.SearchDomains)
	}
	if h.cfg.EngineNetworkMode == "host" {
		set("NetworkMode", "host")
		delete(spec, "NetworkingConfig")
	}
	var environment []string
	if err := json.Unmarshal(spec["Env"], &environment); err != nil && len(spec["Env"]) > 0 {
		return nil, err
	}
	existing := map[string]bool{}
	for _, value := range environment {
		key, _, _ := strings.Cut(value, "=")
		existing[strings.ToUpper(key)] = true
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
		if !existing[key] {
			environment = append(environment, key+"="+h.proxyURL(), strings.ToLower(key)+"="+h.proxyURL())
		}
	}
	if !existing["NO_PROXY"] {
		environment = append(environment, "NO_PROXY=", "no_proxy=")
	}
	spec["Env"], _ = json.Marshal(environment)
	spec["HostConfig"], _ = json.Marshal(host)
	return json.Marshal(spec)
}

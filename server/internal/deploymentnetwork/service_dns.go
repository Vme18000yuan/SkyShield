package deploymentnetwork

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Host-network execution components no longer use Docker's embedded DNS. Only
// the deployment's reserved service aliases are projected from its own labels;
// all target names still go through the host's DNS/hosts configuration.
func (h *Helper) serviceAddresses(name string) ([]net.IP, bool) {
	if h.cfg.ProjectName == "" {
		return nil, false
	}
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	switch name {
	case "postgres", "redis", "loki", "server", "frontend", "nginx", "network-helper":
	default:
		return nil, false
	}
	h.serviceMu.Lock()
	defer h.serviceMu.Unlock()
	if time.Now().After(h.serviceUntil) {
		filters, _ := json.Marshal(map[string][]string{"label": {"com.docker.compose.project=" + h.cfg.ProjectName}})
		ctx, cancel := context.WithTimeout(h.ctx, 2*time.Second)
		defer cancel()
		transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", h.cfg.DockerUpstream)
		}}
		defer transport.CloseIdleConnections()
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/containers/json?filters="+url.QueryEscape(string(filters)), nil)
		response, err := (&http.Client{Transport: transport}).Do(request)
		if err != nil {
			return nil, true
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return nil, true
		}
		var containers []struct {
			Labels          map[string]string
			HostConfig      struct{ NetworkMode string }
			NetworkSettings struct {
				Networks map[string]struct{ IPAddress string }
			}
		}
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&containers); err != nil {
			return nil, true
		}
		services := map[string][]net.IP{}
		for _, container := range containers {
			if container.Labels["com.docker.compose.project"] != h.cfg.ProjectName {
				continue
			}
			service := container.Labels["com.docker.compose.service"]
			if container.HostConfig.NetworkMode == "host" {
				services[service] = append(services[service], net.ParseIP(h.cfg.BindIP))
			} else if endpoint, ok := container.NetworkSettings.Networks[h.cfg.ProjectName+"_default"]; ok {
				if ip := net.ParseIP(endpoint.IPAddress); ip != nil {
					services[service] = append(services[service], ip)
				}
			}
		}
		h.services, h.serviceUntil = services, time.Now().Add(time.Second)
	}
	return h.services[name], true
}

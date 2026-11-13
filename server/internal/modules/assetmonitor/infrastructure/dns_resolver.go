package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	contractresults "github.com/yyhuni/lunafox/contracts/results"
	"github.com/yyhuni/lunafox/server/internal/geolocation"
)

const dnsEndpoint = "https://dns.google/resolve"

// DNSResolver obtains a current public A record for geographic estimation.
// A DNS answer is evidence of a name's current resolution, not a scan result.
type DNSResolver struct {
	client   *http.Client
	endpoint string
}

func NewDNSResolver(client *http.Client) *DNSResolver {
	if client == nil {
		client = http.DefaultClient
	}
	copy := *client
	copy.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || req.URL.Scheme != "https" || req.URL.Host != via[0].URL.Host {
			return errors.New("DNS resolver redirect rejected")
		}
		return nil
	}
	return &DNSResolver{client: &copy, endpoint: dnsEndpoint}
}

func (r *DNSResolver) ResolveHost(ctx context.Context, host string) (string, error) {
	name, ok := contractresults.NormalizeSubdomainDNSName(host)
	if !ok || reservedDNSName(name) {
		return "", errors.New("hostname is not a public DNS name")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	endpoint, err := url.Parse(r.endpoint)
	if err != nil || endpoint.Scheme != "https" {
		return "", errors.New("invalid DNS resolver endpoint")
	}
	q := endpoint.Query()
	q.Set("name", name)
	q.Set("type", "A")
	endpoint.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/dns-json")
	response, err := r.client.Do(req)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("DNS resolver returned status %d", response.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(payload) > 64<<10 {
		return "", errors.New("DNS response is unavailable or too large")
	}
	var result struct {
		Status *int `json:"Status"`
		Answer []struct {
			Type int    `json:"type"`
			Data string `json:"data"`
		} `json:"Answer"`
	}
	if err := json.Unmarshal(payload, &result); err != nil || result.Status == nil || *result.Status != 0 {
		return "", errors.New("DNS lookup failed")
	}
	for _, record := range result.Answer {
		if record.Type == 1 {
			address, err := netip.ParseAddr(strings.TrimSpace(record.Data))
			if err != nil || !address.Is4() {
				continue
			}
			if ip, ok := geolocation.NormalizePublicIP(record.Data); ok {
				return ip, nil
			}
		}
	}
	return "", errors.New("DNS lookup has no public IPv4 answer")
}

func reservedDNSName(host string) bool {
	for _, suffix := range []string{".localhost", ".local", ".localdomain", ".internal", ".lan", ".invalid", ".test", ".example", ".onion", ".arpa"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

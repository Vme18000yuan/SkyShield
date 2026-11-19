package oast

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

type Config struct {
	PublicURL   string
	HTTPListen  string
	DNSListen   string
	DNSZone     string
	DNSAnswerIP string
}

var dnsLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var tokenPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

// DeploymentConfig derives the callback origin from the configured listener,
// never from the Web port. An explicit reverse-proxy/public URL takes priority.
func DeploymentConfig(siteURL string, c Config) (Config, error) {
	c.HTTPListen = strings.TrimSpace(c.HTTPListen)
	if strings.EqualFold(c.HTTPListen, "off") {
		c.HTTPListen = ""
	}
	if strings.TrimSpace(c.PublicURL) == "" {
		c.PublicURL = siteURL
		if c.HTTPListen != "" {
			_, port, err := net.SplitHostPort(c.HTTPListen)
			if err != nil || !validPort(port) {
				return c, fmt.Errorf("invalid OAST_HTTP_LISTEN")
			}
			site, err := url.Parse(siteURL)
			if err != nil || site.Hostname() == "" {
				return c, fmt.Errorf("PUBLIC_URL must contain a host to derive OAST_PUBLIC_URL")
			}
			// The dedicated listener serves HTTP; HTTPS needs an explicit URL
			// pointing at a TLS reverse proxy, independent of the Web's scheme.
			c.PublicURL = (&url.URL{Scheme: "http", Host: net.JoinHostPort(site.Hostname(), port)}).String()
		}
	}
	return c.Normalize()
}

func (c Config) Normalize() (Config, error) {
	c.PublicURL = strings.TrimRight(strings.TrimSpace(c.PublicURL), "/")
	c.DNSZone = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(c.DNSZone), "."))
	c.HTTPListen = strings.TrimSpace(c.HTTPListen)
	c.DNSListen = strings.TrimSpace(c.DNSListen)
	c.DNSAnswerIP = strings.TrimSpace(c.DNSAnswerIP)
	if c.PublicURL != "" {
		u, err := url.Parse(c.PublicURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Path != "" {
			return c, fmt.Errorf("OAST_PUBLIC_URL must be an HTTP(S) origin without credentials, path, query or fragment")
		}
		if strings.HasSuffix(u.Host, ":") || (u.Port() != "" && !validPort(u.Port())) {
			return c, fmt.Errorf("OAST_PUBLIC_URL port must be between 1 and 65535")
		}
	}
	for _, address := range []string{c.HTTPListen, c.DNSListen} {
		if address != "" {
			_, port, err := net.SplitHostPort(address)
			if err != nil {
				return c, fmt.Errorf("invalid OAST listen address: %w", err)
			}
			if !validPort(port) {
				return c, fmt.Errorf("OAST listen port must be between 1 and 65535")
			}
			if c.PublicURL == "" {
				return c, ErrDisabled
			}
		}
	}
	if c.DNSListen != "" || c.DNSZone != "" || c.DNSAnswerIP != "" {
		if c.DNSListen == "" || c.DNSZone == "" || net.ParseIP(c.DNSAnswerIP) == nil || len(c.DNSZone) > 190 || !strings.Contains(c.DNSZone, ".") {
			return c, fmt.Errorf("OAST DNS requires DNS_LISTEN, a DNS_ZONE and DNS_ANSWER_IP")
		}
		for _, label := range strings.Split(c.DNSZone, ".") {
			if !dnsLabel.MatchString(label) {
				return c, fmt.Errorf("invalid OAST_DNS_ZONE")
			}
		}
	}
	return c, nil
}

func validPort(value string) bool {
	n, err := strconv.Atoi(value)
	return err == nil && n > 0 && n <= 65535
}

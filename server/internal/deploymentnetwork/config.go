// Package deploymentnetwork adapts a deployment's host DNS, hosts and proxy
// settings to its containers, without changing the Docker daemon configuration.
package deploymentnetwork

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	BindIP            string            `json:"bindIP"`
	DNSPort           int               `json:"dnsPort"`
	ProxyPort         int               `json:"proxyPort"`
	AllowedCIDRs      []string          `json:"allowedCIDRs"`
	ResolvConf        string            `json:"resolvConf"`
	HostsFile         string            `json:"hostsFile"`
	DockerSocket      string            `json:"dockerSocket"`
	DockerUpstream    string            `json:"dockerUpstream"`
	ProxyToken        string            `json:"proxyToken"`
	ProxyEnvironment  map[string]string `json:"proxyEnvironment"`
	SearchDomains     []string          `json:"searchDomains"`
	EngineNetworkMode string            `json:"engineNetworkMode"`
	ProjectName       string            `json:"projectName,omitempty"`
}

func LoadConfig(path string) (Config, error) {
	var cfg Config
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return cfg, fmt.Errorf("network configuration must be a private regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 128<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("invalid network configuration")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return cfg, fmt.Errorf("network configuration contains trailing data")
	}
	return cfg, cfg.validate()
}

func (c Config) validate() error {
	ip := net.ParseIP(c.BindIP)
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
		return fmt.Errorf("network helper requires a specific host interface address")
	}
	if c.DNSPort < 1 || c.DNSPort > 65535 || c.ProxyPort < 1 || c.ProxyPort > 65535 || c.ProxyPort == c.DNSPort {
		return fmt.Errorf("invalid network helper ports")
	}
	if _, err := hex.DecodeString(c.ProxyToken); len(c.ProxyToken) != 64 || err != nil {
		return fmt.Errorf("network helper proxy token is invalid")
	}
	if len(c.AllowedCIDRs) == 0 {
		return fmt.Errorf("network helper client networks are required")
	}
	for _, raw := range c.AllowedCIDRs {
		_, subnet, err := net.ParseCIDR(raw)
		if err != nil {
			return fmt.Errorf("invalid network helper client network")
		}
		ones, _ := subnet.Mask.Size()
		if ones == 0 {
			return fmt.Errorf("network helper cannot allow every address")
		}
	}
	for _, path := range []string{c.ResolvConf, c.HostsFile, c.DockerSocket, c.DockerUpstream} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
			return fmt.Errorf("network helper paths must be clean absolute paths")
		}
	}
	if c.EngineNetworkMode != "bridge" && c.EngineNetworkMode != "host" {
		return fmt.Errorf("engine network mode must be bridge or host")
	}
	if strings.ContainsAny(c.ProjectName, "/\\ \r\n\t") || len(c.ProjectName) > 128 {
		return fmt.Errorf("invalid deployment project name")
	}
	return nil
}

// ReadResolvers keeps resolver order, including a host-local systemd-resolved
// stub. The helper shares the host network namespace, so loopback is meaningful.
// An empty host configuration is an error; public DNS is never substituted.
func ReadResolvers(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read system DNS configuration: %w", err)
	}
	defer f.Close()
	var result []string
	seen := map[string]bool{}
	scanner := bufio.NewScanner(io.LimitReader(f, 64<<10))
	for scanner.Scan() {
		line, _, _ := strings.Cut(scanner.Text(), "#")
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		ip := fields[1]
		plain, _, _ := strings.Cut(ip, "%")
		parsed := net.ParseIP(plain)
		if parsed == nil || parsed.IsUnspecified() || parsed.IsMulticast() {
			return nil, fmt.Errorf("invalid nameserver in system DNS configuration")
		}
		if !seen[ip] {
			result = append(result, ip)
			seen[ip] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("system DNS configuration has no nameservers")
	}
	return result, nil
}

func readHosts(path string) (map[string][]net.IP, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	result := map[string][]net.IP{}
	scanner := bufio.NewScanner(io.LimitReader(f, 1<<20))
	for scanner.Scan() {
		line, _, _ := strings.Cut(scanner.Text(), "#")
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		ip := net.ParseIP(fields[0])
		if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
			continue
		}
		for _, host := range fields[1:] {
			host = strings.TrimSuffix(strings.ToLower(host), ".")
			if host != "" {
				result[host] = append(result[host], ip)
			}
		}
	}
	return result, scanner.Err()
}

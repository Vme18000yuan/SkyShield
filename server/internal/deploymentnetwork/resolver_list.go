package deploymentnetwork

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

// ResolverList produces the scanner's explicit resolver resource. Native
// deployments use the OS configuration; Docker supplies its host adapter IP.
func ResolverList(path string) (string, error) {
	var servers []string
	if path == "" {
		var err error
		servers, err = ReadResolvers(systemResolvConf())
		if err != nil {
			return "", err
		}
	} else {
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
			return "", fmt.Errorf("system resolver list must be a bounded regular file")
		}
		scanner := bufio.NewScanner(io.LimitReader(f, 64<<10))
		for scanner.Scan() {
			line, _, _ := strings.Cut(scanner.Text(), "#")
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			ip := net.ParseIP(line)
			if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
				return "", fmt.Errorf("system resolver list contains an invalid IP address")
			}
			servers = append(servers, ip.String())
		}
		if err := scanner.Err(); err != nil {
			return "", err
		}
	}
	seen := map[string]bool{}
	var result strings.Builder
	for _, server := range servers {
		if !seen[server] {
			result.WriteString(server)
			result.WriteByte('\n')
			seen[server] = true
		}
	}
	if result.Len() == 0 {
		return "", fmt.Errorf("no system DNS servers are configured; public DNS fallback is disabled")
	}
	return result.String(), nil
}

func systemResolvConf() string {
	if path := os.Getenv("SKYSHIELD_HOST_RESOLV_CONF"); path != "" {
		return path
	}
	const stub = "/run/systemd/resolve/stub-resolv.conf"
	if info, err := os.Stat(stub); err == nil && info.Mode().IsRegular() {
		if conn, err := net.DialTimeout("tcp", "127.0.0.53:53", 300*time.Millisecond); err == nil {
			conn.Close()
			return stub
		}
	}
	return "/etc/resolv.conf"
}

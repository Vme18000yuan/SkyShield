package oast

import (
	"net"
	"net/url"
	"strings"
)

// Capabilities reports configuration, not a successful network reachability
// test. It does not resolve names or make requests that could pollute evidence.
type Capabilities struct {
	Enabled           bool     `json:"enabled"`
	PublicURL         string   `json:"publicUrl"`
	Protocols         []string `json:"protocols"`
	DNSZone           string   `json:"dnsZone,omitempty"`
	AddressScope      string   `json:"addressScope"`
	Reachability      string   `json:"reachability"`
	DefaultTTLSeconds int      `json:"defaultTtlSeconds"`
	MaxTTLSeconds     int      `json:"maxTtlSeconds"`
	MaxWaitSeconds    int      `json:"maxWaitSeconds"`
	Notes             []string `json:"notes"`
}

func (s *Service) capabilities() Capabilities {
	out := Capabilities{
		Enabled: s.config.PublicURL != "", PublicURL: s.config.PublicURL,
		Protocols: []string{}, AddressScope: "unconfigured", Reachability: "not_verified",
		DefaultTTLSeconds: 3600, MaxTTLSeconds: 86400, MaxWaitSeconds: 20, Notes: []string{},
	}
	if !out.Enabled {
		out.Notes = append(out.Notes, "未配置反连入口；请配置 OAST_PUBLIC_URL 为被测目标可访问的 HTTP(S) 地址。")
		return out
	}
	u, _ := url.Parse(s.config.PublicURL) // Validated by New.
	out.Protocols = append(out.Protocols, u.Scheme)
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	ip := net.ParseIP(host)
	switch {
	case host == "localhost" || strings.HasSuffix(host, ".localhost") || ip != nil && ip.IsLoopback():
		out.AddressScope = "loopback"
		out.Notes = append(out.Notes, "回连入口为回环地址，仅同机可用。其他主机或容器内的目标会访问其自身，不能用它验证远程目标；需配置目标可达的 OAST_PUBLIC_URL。")
	case ip != nil && (ip.IsUnspecified() || ip.IsMulticast()):
		out.AddressScope = "unusable"
		out.Notes = append(out.Notes, "监听地址不能作为回连目标；请将 OAST_PUBLIC_URL 改为目标可达的具体地址。")
	case ip != nil && (ip.IsPrivate() || ip.IsLinkLocalUnicast()):
		out.AddressScope = "private"
		out.Notes = append(out.Notes, "回连入口属于私有或链路本地地址；仅适用于可路由到该网络的目标。")
	case ip != nil:
		out.AddressScope = "public_ip"
	default:
		out.AddressScope = "hostname"
	}
	if s.config.DNSListen != "" {
		out.Protocols = append(out.Protocols, "dns")
		out.DNSZone = s.config.DNSZone
		out.Notes = append(out.Notes, "DNS 已配置，仍需父区域 NS 委派与 UDP/TCP 53 可达；配置状态不等于验证通过。")
	} else {
		out.Notes = append(out.Notes, "未配置 DNS 反连，仅使用 oast_create 返回的 httpUrl，不要自行拼接 DNS 地址。")
	}
	out.Notes = append(out.Notes, "未验证从目标网络的实际可达性；禁止访问正式测试探针自测，部署自测应使用单独探针。")
	return out
}

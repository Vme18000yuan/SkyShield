package infogather

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	contractresults "github.com/yyhuni/lunafox/contracts/results"
	assetdomain "github.com/yyhuni/lunafox/server/internal/modules/asset/domain"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

type Scope struct {
	Name string
	Kind string
	// Input preserves an explicit HTTP(S) path but never contains credentials or a query.
	Input string
}

func parseScope(raw string) (Scope, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 4096 || strings.ContainsAny(raw, "\x00\r\n\t ") || strings.HasPrefix(raw, "-") {
		return Scope{}, errors.New("目标格式无效")
	}
	if prefix, err := netip.ParsePrefix(raw); err == nil {
		value := prefix.Masked().String()
		return Scope{Name: value, Kind: "CIDR", Input: value}, nil
	}
	if ip, err := netip.ParseAddr(raw); err == nil {
		value := ip.String()
		return Scope{Name: value, Kind: "IP", Input: value}, nil
	}
	value, input := raw, ""
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return Scope{}, errors.New("请输入不含凭据、查询参数和片段的 HTTP(S) URL")
		}
		if _, err = contractresults.ValidateObservedAssetURL(raw); err != nil {
			return Scope{}, errors.New("URL 无效")
		}
		value = u.Hostname()
		input = raw
	} else if strings.ContainsAny(raw, "/:") {
		return Scope{}, errors.New("带端口的目标请使用完整 HTTP(S) URL")
	}
	if ip, err := netip.ParseAddr(value); err == nil {
		return Scope{Name: ip.String(), Kind: "IP", Input: input}, nil
	}
	value, err := idna.Lookup.ToASCII(strings.ToLower(strings.TrimSuffix(value, ".")))
	if err != nil {
		return Scope{}, errors.New("域名无效")
	}
	value, ok := contractresults.NormalizeSubdomainDNSName(value)
	if !ok || len(value) > 253 {
		return Scope{}, errors.New("域名无效")
	}
	if input == "" {
		input = value
	}
	return Scope{Name: value, Kind: "HOST", Input: input}, nil
}

func (s Scope) target() assetdomain.TargetRef {
	typ := map[string]string{"HOST": "domain", "IP": "ip", "CIDR": "cidr"}[s.Kind]
	return assetdomain.TargetRef{Name: s.Name, Type: typ}
}

// targetContainsScope requires the entire requested scope to fit, not merely
// one address from a requested network.
func targetContainsScope(target assetdomain.TargetRef, requested Scope) bool {
	if requested.Kind == "HOST" {
		return assetdomain.IsSubdomainMatchTarget(requested.Name, target)
	}
	if requested.Kind == "IP" {
		return targetContainsIP(target, requested.Name)
	}
	if requested.Kind != "CIDR" || assetdomain.NormalizeTargetType(target.Type) != "cidr" {
		return false
	}
	want, err := netip.ParsePrefix(requested.Name)
	if err != nil {
		return false
	}
	have, err := netip.ParsePrefix(strings.TrimSpace(target.Name))
	return err == nil && have.Bits() <= want.Bits() && have.Contains(want.Masked().Addr())
}

func targetContainsIP(target assetdomain.TargetRef, value string) bool {
	ip, err := netip.ParseAddr(value)
	if err != nil {
		return false
	}
	switch assetdomain.NormalizeTargetType(target.Type) {
	case "ip":
		expected, err := netip.ParseAddr(target.Name)
		return err == nil && expected.Unmap() == ip.Unmap()
	case "cidr":
		prefix, err := netip.ParsePrefix(target.Name)
		return err == nil && prefix.Contains(ip)
	default:
		return false
	}
}

func validURL(raw string) bool {
	if _, err := contractresults.ValidateObservedAssetURL(raw); err != nil {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Scheme == "http" || u.Scheme == "https")
}

func unsupportedIPv6URL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(u.Hostname())
	return err == nil && ip.Is6()
}

// toAssetInput rechecks each observation against a server-selected target. It
// deliberately ignores the report's scope label.
func toAssetInput(a Asset, target assetdomain.TargetRef, allowCDN bool, runID string) (assetapp.AssetInput, error) {
	item := assetapp.AssetInput{TargetID: target.ID, Extra: map[string]any{"source": "info-gath", "run_id": runID, "evidence": a.Evidence}}
	if a.Confidence == "candidate" {
		item.Extra["confidence"] = "candidate"
	}
	switch a.Type {
	case "domain":
		name, ok := contractresults.NormalizeSubdomainDNSName(a.Value)
		if !ok || !assetdomain.IsSubdomainMatchTarget(name, target) {
			return item, errors.New("域名不在所选目标范围内")
		}
		item.Type, item.Domain = "subdomain", name
		if root, err := publicsuffix.EffectiveTLDPlusOne(name); err == nil && root == name {
			item.Type = "root_domain"
		}
	case "ip":
		if _, err := netip.ParseAddr(a.Value); err != nil {
			return item, errors.New("IP 地址无效")
		}
		if a.CDN && !allowCDN {
			return item, errors.New("CDN 地址归属待确认")
		}
		item.Type, item.IP = "ip", a.Value
		if assetdomain.IsDomainTargetType(target.Type) {
			for _, domain := range a.BoundDomains {
				if assetdomain.IsSubdomainMatchTarget(domain, target) {
					item.BoundDomains = append(item.BoundDomains, domain)
				}
			}
			if len(item.BoundDomains) == 0 {
				return item, errors.New("IP 缺少目标范围内的 DNS 关联")
			}
		} else if !targetContainsIP(target, a.Value) {
			return item, errors.New("IP 不在所选目标范围内")
		}
	case "service":
		item.Type = "service"
		if a.Port == 0 {
			if unsupportedIPv6URL(a.Value) {
				return item, errors.New("系统暂不支持 IPv6 URL 资产，记录已保留")
			}
			if !validURL(a.Value) || !assetdomain.IsURLMatchTarget(a.Value, target) {
				return item, errors.New("Web 服务不在所选目标范围内")
			}
			item.URL = a.Value
			item.PageTitle, item.Technologies = a.PageTitle, a.Technologies
			if a.StatusCode != nil && *a.StatusCode >= 100 && *a.StatusCode <= 599 {
				item.StatusCode = a.StatusCode
			}
			break
		}
		if a.Port < 1 || a.Port > 65535 || (a.Protocol != "tcp" && a.Protocol != "udp") {
			return item, errors.New("服务端口或协议无效")
		}
		if _, err := netip.ParseAddr(a.Value); err != nil {
			return item, errors.New("服务 IP 无效")
		}
		if a.CDN && !allowCDN {
			return item, errors.New("CDN 地址归属待确认")
		}
		item.IP, item.Port, item.Proto, item.ServiceName = a.Value, a.Port, a.Protocol, a.ServiceName
		if item.ServiceName == "" || item.ServiceName == "unknown" || item.ServiceName == "tcp-connect" {
			item.ServiceName = knownPortServices[item.Port]
			if item.ServiceName == "" {
				item.ServiceName = "unknown"
			} else {
				item.Extra["service_confidence"] = "port-based"
			}
		}
		if assetdomain.IsDomainTargetType(target.Type) {
			for _, domain := range a.BoundDomains {
				if assetdomain.IsSubdomainMatchTarget(domain, target) {
					item.Domain = domain
					break
				}
			}
			if item.Domain == "" {
				return item, errors.New("端口缺少目标范围内的 DNS 关联")
			}
		} else if !targetContainsIP(target, a.Value) {
			return item, errors.New("服务 IP 不在所选目标范围内")
		}
	case "endpoint":
		if unsupportedIPv6URL(a.Value) {
			return item, errors.New("系统暂不支持 IPv6 URL 资产，记录已保留")
		}
		if !validURL(a.Value) || !assetdomain.IsURLMatchTarget(a.Value, target) {
			return item, errors.New("URL 不在所选目标范围内")
		}
		item.Type, item.URL = "endpoint", a.Value
	default:
		return item, errors.New("资产类型无效")
	}
	return item, nil
}

func serviceAddress(host string, port int) string { return net.JoinHostPort(host, strconv.Itoa(port)) }

func scopeLabel(s Scope) string { return fmt.Sprintf("%s (%s)", s.Name, s.Kind) }

var knownPortServices = map[int]string{
	21: "ftp", 22: "ssh", 23: "telnet", 25: "smtp", 53: "dns", 80: "http", 110: "pop3", 143: "imap", 443: "https", 445: "smb",
	1883: "mqtt", 3306: "mysql", 5432: "postgresql", 6379: "redis", 8883: "mqtts", 9200: "elasticsearch", 27017: "mongodb",
}

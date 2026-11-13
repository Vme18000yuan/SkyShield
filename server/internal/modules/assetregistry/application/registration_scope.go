package application

import (
	"net/netip"
	"strings"

	assetdomain "github.com/yyhuni/lunafox/server/internal/modules/asset/domain"
)

func RegistrationMatchesTarget(asset PreparedAsset, target assetdomain.TargetRef) bool {
	input := asset.Input
	switch input.Type {
	case "root_domain", "subdomain":
		return assetdomain.IsSubdomainMatchTarget(asset.Label, target)
	case "endpoint":
		return assetdomain.IsURLMatchTarget(input.URL, target)
	case "service":
		if input.URL != "" {
			return assetdomain.IsURLMatchTarget(input.URL, target)
		}
		if assetdomain.IsDomainTargetType(target.Type) {
			return assetdomain.IsSubdomainMatchTarget(input.Domain, target)
		}
		return registrationIPMatchesTarget(input.IP, target)
	case "ip":
		if assetdomain.IsDomainTargetType(target.Type) {
			for _, domain := range input.BoundDomains {
				if assetdomain.IsSubdomainMatchTarget(domain, target) {
					return true
				}
			}
			return false
		}
		return registrationIPMatchesTarget(asset.Label, target)
	case "app":
		if assetdomain.IsDomainTargetType(target.Type) {
			for _, domain := range input.BoundDomains {
				if assetdomain.IsSubdomainMatchTarget(domain, target) {
					return true
				}
			}
		}
		return false
	default:
		return false
	}
}

func registrationIPMatchesTarget(ip string, target assetdomain.TargetRef) bool {
	address, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return false
	}
	switch assetdomain.NormalizeTargetType(target.Type) {
	case "ip":
		expected, err := netip.ParseAddr(strings.TrimSpace(target.Name))
		return err == nil && address.Unmap() == expected.Unmap()
	case "cidr":
		network, err := netip.ParsePrefix(strings.TrimSpace(target.Name))
		return err == nil && network.Contains(address)
	default:
		return false
	}
}

package repository

import (
	"net"
	"net/netip"
	"net/url"
	"strings"

	contractresults "github.com/yyhuni/lunafox/contracts/results"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

// Only an explicitly registered item adds scope; derived projections do not.
// This shares the asset's transaction and the task's active-run fence.
func addDiscoveredTaskScope(tx *gorm.DB, command assetapp.RegistrationCommand) error {
	if command.Task == nil || command.Origin != "ai" {
		return nil
	}
	input := command.Asset.Input
	kind, domain, network := "", "", ""
	switch input.Type {
	case "root_domain", "subdomain":
		kind, domain = input.Type, command.Asset.Label
	case "ip":
		kind, network = "ip", command.Asset.Label
	case "service", "endpoint":
		host := strings.TrimSpace(input.Domain)
		if host == "" && input.URL != "" {
			if parsed, err := url.Parse(input.URL); err == nil {
				host = parsed.Hostname()
			}
		}
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if _, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
			kind, network = "ip", strings.Trim(host, "[]")
		} else if normalized, ok := contractresults.NormalizeSubdomainDNSName(host); ok {
			kind, domain = "subdomain", normalized
		} else if input.ServiceIP != "" {
			kind, network = "ip", input.ServiceIP
		} else if input.IP != "" {
			kind, network = "ip", input.IP
		}
	}
	key := domain
	if kind == "ip" {
		addr, err := netip.ParseAddr(network)
		if err != nil {
			return nil
		}
		addr = addr.Unmap()
		network = netip.PrefixFrom(addr, addr.BitLen()).String()
		key = network
	}
	if kind == "" || key == "" {
		return nil
	}
	return tx.Exec(`INSERT INTO ai_task_scope(tenant_id,owner_user_id,task_id,kind,scope_key,domain,net,source)
		VALUES(?,?,?,?,?,NULLIF(?,''),NULLIF(?,'')::cidr,'auto') ON CONFLICT(task_id,kind,scope_key) DO NOTHING`,
		assetapp.DefaultTenantID, command.UserID, command.Task.TaskID, kind, key, domain, network).Error
}

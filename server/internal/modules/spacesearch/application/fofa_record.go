package application

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	contractresults "github.com/yyhuni/lunafox/contracts/results"
	registryapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

func NormalizeRecord(record Record) Record {
	record.Importable = false
	record.ImportError = ""
	record.URL = ""
	record.Hostname = ""
	record.Target = ""
	ip, err := netip.ParseAddr(strings.TrimSpace(record.IP))
	if err != nil {
		record.ImportError = "FOFA 结果中的 IP 地址无效"
		return record
	}
	record.IP = ip.Unmap().String()
	if record.Port < 1 || record.Port > 65535 {
		record.ImportError = "FOFA 结果中的端口无效"
		return record
	}
	record.Protocol = strings.ToLower(strings.TrimSpace(record.Protocol))
	record.BaseProtocol = strings.ToLower(strings.TrimSpace(record.BaseProtocol))
	host := strings.TrimSpace(record.Host)
	if strings.HasPrefix(strings.ToLower(host), "http://") || strings.HasPrefix(strings.ToLower(host), "https://") {
		u, parseErr := url.Parse(host)
		if parseErr != nil || u.User != nil || u.Fragment != "" {
			record.ImportError = "FOFA 结果中的服务 URL 无效"
			return record
		}
		if _, parseErr = contractresults.ValidateObservedAssetURL(host); parseErr != nil {
			record.ImportError = "FOFA 结果中的服务 URL 无效"
			return record
		}
		record.URL = host
		record.Hostname = strings.ToLower(u.Hostname())
	} else {
		hostname := host
		if parsed, _, splitErr := net.SplitHostPort(host); splitErr == nil {
			hostname = parsed
		}
		if strings.Contains(hostname, "://") {
			if u, e := url.Parse(hostname); e == nil {
				hostname = u.Hostname()
			}
		}
		hostname = strings.Trim(hostname, "[]")
		if hostname == "" {
			hostname = record.IP
		}
		if parsed, e := netip.ParseAddr(hostname); e == nil {
			record.Hostname = parsed.Unmap().String()
		} else if domain, ok := contractresults.NormalizeSubdomainDNSName(hostname); ok {
			record.Hostname = domain
		} else {
			record.Hostname = record.IP
		}
		if record.Protocol == "http" || record.Protocol == "https" {
			authority := record.Hostname
			defaultPort := record.Protocol == "http" && record.Port == 80 || record.Protocol == "https" && record.Port == 443
			if !defaultPort {
				authority = net.JoinHostPort(record.Hostname, strconv.Itoa(record.Port))
			} else if strings.Contains(authority, ":") {
				authority = "[" + authority + "]"
			}
			record.URL = record.Protocol + "://" + authority
		}
	}
	if parsed, e := netip.ParseAddr(record.Hostname); e == nil {
		record.Hostname = parsed.Unmap().String()
	} else if domain, ok := contractresults.NormalizeSubdomainDNSName(record.Hostname); ok {
		record.Hostname = domain
	} else {
		record.ImportError = "FOFA 结果中的主机名无效"
		return record
	}
	record.Target = AutomaticTarget(record.Hostname)
	record.Importable = true
	return record
}

// AutomaticTarget groups DNS hosts under their registrable root while keeping
// IP addresses and hostnames without a registrable root unchanged.
func AutomaticTarget(hostname string) string {
	if root, ok := registryapp.RegistrableRootDomain("subdomain", hostname); ok {
		return root
	}
	return hostname
}

func RecordAssets(command ImportCommand, target int) []registryapp.AssetInput {
	r := command.Record
	extra := map[string]any{"provider": "fofa", "query": command.Query, "searchId": command.Selection.SearchID, "rowId": r.RowID, "host": r.Host, "ip": r.IP, "port": r.Port, "protocol": r.Protocol, "domain": r.Domain, "country": r.Country, "countryName": r.CountryName, "region": r.Region, "city": r.City, "icp": r.ICP, "server": r.Server, "baseProtocol": r.BaseProtocol}
	org := command.Selection.OrganizationID
	domain := ""
	if _, err := netip.ParseAddr(r.Hostname); err != nil {
		domain = r.Hostname
	}
	bound := []string{}
	if domain != "" {
		bound = append(bound, domain)
	}
	transport := r.BaseProtocol
	if transport != "tcp" && transport != "udp" {
		transport = "unknown"
	}
	items := []registryapp.AssetInput{{Type: "ip", IP: r.IP, TargetID: target, OrganizationID: org, BoundDomains: bound, OpenPorts: []registryapp.PortObservation{{Port: r.Port, Service: r.Protocol, Protocol: transport}}, Extra: extra}}
	if domain != "" {
		items = append(items, registryapp.AssetInput{Type: "subdomain", Domain: domain, TargetID: target, OrganizationID: org, Extra: extra})
	}
	service := registryapp.AssetInput{Type: "service", TargetID: target, OrganizationID: org, URL: r.URL, ServiceIP: r.IP, PageTitle: r.Title, Webserver: r.Server, Extra: extra}
	if r.URL == "" {
		service.IP = r.IP
		service.Domain = domain
		service.Port = r.Port
		service.Proto = transport
		service.ServiceName = r.Protocol
		if service.ServiceName == "" {
			service.ServiceName = "unknown"
		}
	}
	items = append(items, service)
	return items
}

func ImportRequestID(selection ImportSelection) string {
	return fmt.Sprintf("fofa/%s/%d/%d/%d", selection.SearchID, selection.RowID, selection.OrganizationID, selection.TargetID)
}

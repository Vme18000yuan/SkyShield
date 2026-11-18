package application

import (
	"context"
	"strings"
	"time"
)

type PortAsset struct {
	Label         string              `json:"label"`
	Port          int                 `json:"port"`
	Service       string              `json:"service"`
	ServiceSource string              `json:"serviceSource"`
	FirstSeenAt   time.Time           `json:"firstSeenAt"`
	LastSeenAt    time.Time           `json:"lastSeenAt"`
	Organizations []AssetOrganization `json:"organizations"`
}
type PortQuery struct {
	HideCDN        bool
	TenantID       string
	OrganizationID int
	RootDomainID   int64
	Query          string
	Offset         int
	Limit          int
}
type PortListInput struct {
	HideCDN        bool
	UserID         int
	OrganizationID int
	RootDomainID   int64
	Query          string
	Page           int
	PageSize       int
}
type PortListResult struct {
	Items     []PortAsset `json:"ports"`
	TotalSize int64       `json:"totalSize"`
	Page      int         `json:"page"`
}

func (f *Facade) ListPorts(ctx context.Context, input PortListInput) (*PortListResult, error) {
	if f == nil || f.store == nil || input.UserID <= 0 {
		return nil, ErrForbidden
	}
	input.Query = strings.TrimSpace(input.Query)
	if len(input.Query) > 200 || input.OrganizationID < 0 || input.OrganizationID > 2147483647 || input.RootDomainID < 0 || input.Page < 0 || input.Page > 1000000 || input.PageSize < 0 || input.PageSize > 100 {
		return nil, ErrInvalidQuery
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.PageSize == 0 {
		input.PageSize = 50
	}
	member, err := f.store.IsActiveTenantMember(ctx, DefaultTenantID, input.UserID)
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, ErrForbidden
	}
	query := PortQuery{HideCDN: input.HideCDN, TenantID: DefaultTenantID, OrganizationID: input.OrganizationID, RootDomainID: input.RootDomainID, Query: input.Query, Offset: (input.Page - 1) * input.PageSize, Limit: input.PageSize}
	items, total, err := f.store.ListPorts(ctx, query)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []PortAsset{}
	}
	classifyPorts(items)
	return &PortListResult{Items: items, TotalSize: total, Page: input.Page}, nil
}

func classifyPorts(items []PortAsset) {
	for i := range items {
		if strings.TrimSpace(items[i].Service) != "" {
			items[i].Service = strings.ToUpper(strings.TrimSpace(items[i].Service))
			items[i].ServiceSource = "observed"
		} else if name, ok := commonPortServices[items[i].Port]; ok {
			items[i].Service = name
			items[i].ServiceSource = "port"
		} else {
			items[i].Service = "UNKNOWN"
			items[i].ServiceSource = "unknown"
		}
	}
}

// Port-number hints are not proof of the protocol running on a host.
var commonPortServices = map[int]string{
	20: "FTP-DATA", 21: "FTP", 22: "SSH", 23: "TELNET", 25: "SMTP", 53: "DNS", 80: "HTTP", 110: "POP3", 111: "RPC", 123: "NTP", 135: "MSRPC", 139: "NETBIOS", 143: "IMAP", 161: "SNMP", 389: "LDAP", 443: "HTTPS", 445: "SMB", 465: "SMTPS", 587: "SMTP", 636: "LDAPS", 873: "RSYNC", 993: "IMAPS", 995: "POP3S", 1433: "MSSQL", 1521: "ORACLE", 1883: "MQTT", 2049: "NFS", 2375: "DOCKER", 2376: "DOCKER-TLS", 3000: "HTTP", 3306: "MYSQL", 3389: "RDP", 5000: "HTTP", 5432: "POSTGRESQL", 5672: "AMQP", 5900: "VNC", 6379: "REDIS", 8000: "HTTP", 8080: "HTTP", 8081: "HTTP", 8443: "HTTPS", 8883: "MQTTS", 9092: "KAFKA", 9200: "ELASTICSEARCH", 11211: "MEMCACHED", 27017: "MONGODB",
}

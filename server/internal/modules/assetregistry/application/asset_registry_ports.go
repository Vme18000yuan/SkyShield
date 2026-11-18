package application

import (
	"context"
	"time"
)

// DefaultTenantID is the sole supported data scope until legacy resources are tenant-aware.
const DefaultTenantID = "00000000-0000-0000-0000-000000000001"

type AssetEntity struct {
	ID                 int64
	AssetType          string
	DisplayType        string
	Label              string
	Method             *string
	FirstSeenAt        time.Time
	LastSeenAt         time.Time
	SourceCount        int64
	Derived            bool
	ApplicationSources int64
	Organizations      []AssetOrganization `gorm:"-"`
}

type AssetOrganization struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type AssetQuery struct {
	HideCDN               bool
	TenantID              string
	AssetType             string
	AssetCategory         string
	Query                 string
	BeforeID              int64
	TargetID              int
	OrganizationID        int
	RootDomainID          int64
	AssetID               int64
	Offset                int
	ExcludeNonWebServices bool
	Limit                 int
}

type Store interface {
	IsActiveTenantMember(context.Context, string, int) (bool, error)
	ListAssets(context.Context, AssetQuery) ([]AssetEntity, error)
	CountAssets(context.Context, AssetQuery) (int64, error)
	ListPorts(context.Context, PortQuery) ([]PortAsset, int64, error)
	ListApplicationWebsites(context.Context, ApplicationWebsiteQuery) ([]ApplicationWebsite, int64, error)
	BackfillBatch(context.Context, int) (bool, error)
}

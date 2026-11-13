package application

import (
	"context"
	"errors"
	"time"

	"github.com/yyhuni/lunafox/server/internal/geolocation"
)

var ErrInvalidSelection = errors.New("invalid organization selection")
var ErrForbidden = errors.New("active tenant membership required")
var ErrOrganizationNotFound = errors.New("selected organization not found")

type Count struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}
type Organization struct {
	ID          int    `json:"id"`
	DisplayName string `json:"displayName"`
	TargetCount int64  `json:"targetCount"`
	AssetCount  int64  `json:"assetCount"`
}
type Summary struct {
	TotalAssets     int64 `json:"totalAssets"`
	Targets         int64 `json:"targets"`
	IPAssets        int64 `json:"ipAssets"`
	Risks           int64 `json:"risks"`
	NewAssets24h    int64 `json:"newAssets24h"`
	NewRisks24h     int64 `json:"newRisks24h"`
	RunningScans    int64 `json:"runningScans"`
	PendingScans    int64 `json:"pendingScans"`
	LocatedAssets   int64 `json:"locatedAssets"`
	UnlocatedAssets int64 `json:"unlocatedAssets"`
}
type Trend struct {
	Date   string `json:"date"`
	Assets int64  `json:"assets"`
	Risks  int64  `json:"risks"`
}
type RecentAsset struct {
	ID          string    `json:"id"`
	Label       string    `json:"label"`
	AssetType   string    `json:"assetType"`
	DisplayType string    `json:"displayType"`
	FirstSeenAt time.Time `json:"firstSeenAt"`
}
type Location struct {
	Key         string   `json:"key"`
	Latitude    *float64 `json:"latitude"`
	Longitude   *float64 `json:"longitude"`
	CountryCode string   `json:"countryCode"`
	RegionName  string   `json:"regionName"`
	CityName    string   `json:"cityName"`
	Label       string   `json:"label"`
	Count       int64    `json:"count"`
	Precision   string   `json:"precision"`
	Expired     bool     `json:"expired"`
}
type Snapshot struct {
	GeneratedAt         time.Time      `json:"generatedAt"`
	Organizations       []Organization `json:"organizations"`
	Summary             Summary        `json:"summary"`
	AssetTypes          []Count        `json:"assetTypes"`
	AssetCategories     []Count        `json:"assetCategories"`
	InventoryCategories []Count        `json:"inventoryCategories"`
	Severities          []Count        `json:"severities"`
	Ports               []Count        `json:"ports"`
	Technologies        []Count        `json:"technologies"`
	Trend               []Trend        `json:"trend"`
	RecentAssets        []RecentAsset  `json:"recentAssets"`
	Locations           []Location     `json:"locations"`
	PendingIPs          []string       `json:"-"`
	PendingHosts        []string       `json:"-"`
}
type Store interface {
	Read(context.Context, int, []int) (*Snapshot, error)
	ClaimIP(context.Context, string) (bool, error)
	SaveLocation(context.Context, geolocation.Location) error
	ClaimHost(context.Context, string) (bool, error)
	SaveHostResolution(context.Context, string, string, time.Time) error
}
type LocationCoordinator interface {
	SubmitIP(context.Context, string, geolocation.LookupCompletion) error
}
type DNSResolver interface {
	ResolveHost(context.Context, string) (string, error)
}

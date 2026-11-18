package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

var (
	ErrInvalidQuery = errors.New("invalid asset query")
	ErrForbidden    = errors.New("tenant membership is required")
)

type ListInput struct {
	HideCDN        bool
	UserID         int
	TargetID       int
	OrganizationID int
	RootDomainID   int64
	AssetID        int64
	BeforeID       int64
	AssetType      string
	AssetCategory  string
	Query          string
	PageSize       int
	PageToken      string
	Page           int
}

type ListResult struct {
	Items         []AssetEntity
	NextPageToken string
	TotalSize     int64
}

type assetCursor struct {
	HideCDN      bool   `json:"hideCdn,omitempty"`
	Tenant       string `json:"tenant"`
	User         int    `json:"user"`
	Type         string `json:"type"`
	Category     string `json:"category,omitempty"`
	Query        string `json:"query"`
	Size         int    `json:"size"`
	Before       int64  `json:"before"`
	Target       int    `json:"target"`
	Organization int    `json:"organization,omitempty"`
	Root         int64  `json:"root,omitempty"`
	Asset        int64  `json:"asset,omitempty"`
}

type Facade struct{ store Store }

func NewFacade(store Store) *Facade { return &Facade{store: store} }

func (f *Facade) List(ctx context.Context, input ListInput) (*ListResult, error) {
	if f == nil || f.store == nil || input.UserID <= 0 {
		return nil, ErrForbidden
	}
	input.Query = strings.TrimSpace(input.Query)
	if utf8.RuneCountInString(input.Query) > 200 || input.PageSize < 0 || input.PageSize > 100 || input.TargetID < 0 || input.TargetID > 2147483647 || input.OrganizationID < 0 || input.OrganizationID > 2147483647 || input.RootDomainID < 0 || input.AssetID < 0 || input.BeforeID < 0 || input.BeforeID > 0 && (input.Page > 0 || input.PageToken != "") || input.Page < 0 || input.Page > 1000000 || (input.Page > 0 && input.PageToken != "") {
		return nil, ErrInvalidQuery
	}
	if input.PageSize == 0 {
		input.PageSize = 50
	}
	switch input.AssetType {
	case "", "root_domain", "subdomain", "ip", "app", "service", "endpoint":
	default:
		return nil, ErrInvalidQuery
	}
	switch input.AssetCategory {
	case "", "web", "non_web_service":
	default:
		return nil, ErrInvalidQuery
	}
	member, err := f.store.IsActiveTenantMember(ctx, DefaultTenantID, input.UserID)
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, ErrForbidden
	}
	cursor := assetCursor{HideCDN: input.HideCDN, Tenant: DefaultTenantID, User: input.UserID, Type: input.AssetType, Category: input.AssetCategory, Query: input.Query, Size: input.PageSize, Target: input.TargetID, Organization: input.OrganizationID, Root: input.RootDomainID, Asset: input.AssetID}
	cursor.Before = input.BeforeID
	if input.PageToken != "" {
		if len(input.PageToken) > 2048 {
			return nil, ErrInvalidQuery
		}
		decoded, decodeErr := base64.RawURLEncoding.DecodeString(input.PageToken)
		var previous assetCursor
		if decodeErr != nil || json.Unmarshal(decoded, &previous) != nil || previous.Before <= 0 ||
			previous.HideCDN != cursor.HideCDN || previous.Tenant != cursor.Tenant || previous.User != cursor.User || previous.Type != cursor.Type || previous.Category != cursor.Category ||
			previous.Query != cursor.Query || previous.Size != cursor.Size || previous.Target != cursor.Target || previous.Organization != cursor.Organization || previous.Root != cursor.Root || previous.Asset != cursor.Asset {
			return nil, ErrInvalidQuery
		}
		cursor.Before = previous.Before
	}
	query := AssetQuery{
		HideCDN: cursor.HideCDN, TenantID: cursor.Tenant, AssetType: cursor.Type, AssetCategory: cursor.Category, Query: cursor.Query,
		BeforeID: cursor.Before, Limit: cursor.Size + 1, TargetID: cursor.Target, OrganizationID: cursor.Organization, RootDomainID: cursor.Root, AssetID: cursor.Asset,
	}
	if input.Page > 0 {
		query.Offset = (input.Page - 1) * cursor.Size
	}
	var total int64
	if input.Page > 0 {
		total, err = f.store.CountAssets(ctx, query)
		if err != nil {
			return nil, err
		}
	}
	items, err := f.store.ListAssets(ctx, query)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].DisplayType = AssetDisplayType(items[i].AssetType, items[i].Label)
	}
	result := &ListResult{Items: items, TotalSize: total}
	if len(items) > cursor.Size {
		result.Items = items[:cursor.Size]
		cursor.Before = result.Items[len(result.Items)-1].ID
		encoded, marshalErr := json.Marshal(cursor)
		if marshalErr != nil {
			return nil, marshalErr
		}
		result.NextPageToken = base64.RawURLEncoding.EncodeToString(encoded)
	}
	if result.Items == nil {
		result.Items = []AssetEntity{}
	}
	return result, nil
}

// AssetDisplayType is the user-facing classification. The canonical asset
// identity still distinguishes a website service from a URL endpoint.
func AssetDisplayType(kind, label string) string {
	if kind == "service" || kind == "endpoint" {
		lower := strings.ToLower(label)
		if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
			return "web"
		}
	}
	return kind
}

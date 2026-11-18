package external

import (
	"context"

	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

type orgQuery struct {
	Org     int   `json:"organization_id" jsonschema:"Organization ID returned by list_organizations."`
	HideCDN *bool `json:"hide_cdn,omitempty" jsonschema:"Hide CDN addresses and ports, default true."`
}
type inventoryQuery struct {
	orgQuery
	Page
	Query string `json:"query,omitempty"`
}

func (s *Service) organizations(ctx context.Context, c Client, in Page) (any, error) {
	limit, offset, err := in.limits()
	if err != nil {
		return nil, err
	}
	type item struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	items := []item{}
	err = s.db.WithContext(ctx).Raw(`SELECT id,name FROM organization WHERE deleted_at IS NULL AND (? OR id=ANY(?::integer[])) ORDER BY id LIMIT ? OFFSET ?`, c.AllOrganizations, c.OrganizationIDs, limit+1, offset).Scan(&items).Error
	if err != nil {
		return nil, err
	}
	return listPage(items, limit), nil
}
func listPage[T any](items []T, limit int) map[string]any {
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	return map[string]any{"items": items, "hasMore": more}
}
func (s *Service) overview(ctx context.Context, c Client, in orgQuery) (any, error) {
	if !c.permitsOrganization(in.Org) {
		return nil, errDenied
	}
	return s.assets.OrganizationOverview(ctx, c.OwnerUserID, in.Org, in.HideCDN == nil || *in.HideCDN)
}
func (s *Service) inventory(ctx context.Context, c Client, in inventoryQuery) (any, error) {
	if !c.permitsOrganization(in.Org) {
		return nil, errDenied
	}
	size, _, err := in.limits()
	if err != nil {
		return nil, err
	}
	result, err := s.assets.ListInventory(ctx, assets.PortListInput{UserID: c.OwnerUserID, OrganizationID: in.Org,
		Page: in.Page.Page, PageSize: size, Query: in.Query, HideCDN: in.HideCDN == nil || *in.HideCDN})
	if err != nil {
		return nil, err
	}
	// Shared assets can belong to other organizations. Only export this view.
	rows := make([]map[string]any, 0, len(result.Items))
	for _, item := range result.Items {
		if a := item.Asset; a != nil {
			rows = append(rows, map[string]any{"id": a.ID, "type": a.DisplayType, "assetType": a.AssetType, "label": a.Label, "lastSeenAt": a.LastSeenAt})
		} else if p := item.Port; p != nil {
			rows = append(rows, map[string]any{"type": "port", "label": p.Label, "port": p.Port, "service": p.Service, "lastSeenAt": p.LastSeenAt})
		}
	}
	return map[string]any{"items": rows, "totalSize": result.TotalSize, "page": result.Page}, nil
}

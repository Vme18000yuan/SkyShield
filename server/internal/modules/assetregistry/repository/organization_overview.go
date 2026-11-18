package repository

import (
	"context"
	"database/sql"
	"errors"

	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

// The same filter drives the inventory list. Count canonical IDs once even
// when several targets or observations in the organization point to one asset.
func (s *AssetRegistryStore) ReadOrganizationOverview(ctx context.Context, q assetapp.AssetQuery) (*assetapp.OrganizationAssetOverview, error) {
	filter, args := assetFilters(q, false)
	var item assetapp.OrganizationAssetOverview
	err := s.db.WithContext(ctx).Raw(organizationAssetCTE+`, counts AS (
 SELECT COUNT(*) FILTER (WHERE e.asset_type='root_domain') roots,
        COUNT(*) FILTER (WHERE e.asset_type='subdomain') domains,
        COUNT(*) FILTER (WHERE e.asset_type='ip') ips
 FROM asset_entity e WHERE e.asset_type IN ('root_domain','subdomain','ip') AND `+filter+`
 ) SELECT org.id,org.name,c.roots,c.domains,c.ips FROM organization org CROSS JOIN counts c
 WHERE org.id=? AND org.deleted_at IS NULL`, append(args, q.OrganizationID)...).Row().Scan(&item.ID, &item.Name, &item.RootDomains, &item.Subdomains, &item.IPs)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, assetapp.ErrOrganizationNotFound
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

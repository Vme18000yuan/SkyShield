package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository/persistence"
	"gorm.io/gorm"
)

type AssetRegistryStore struct{ db *gorm.DB }

func NewAssetRegistryStore(db *gorm.DB) *AssetRegistryStore { return &AssetRegistryStore{db: db} }

func (s *AssetRegistryStore) IsActiveTenantMember(ctx context.Context, tenant string, user int) (bool, error) {
	var exists bool
	err := s.db.WithContext(ctx).Raw(`SELECT EXISTS (
		SELECT 1 FROM tenant_membership m
		JOIN tenant t ON t.id=m.tenant_id
		JOIN auth_user u ON u.id=m.user_id
		WHERE m.tenant_id=? AND m.user_id=? AND t.status='active' AND u.is_active=TRUE
	)`, tenant, user).Scan(&exists).Error
	return exists, err
}

func (s *AssetRegistryStore) ListAssets(ctx context.Context, q assetapp.AssetQuery) ([]assetapp.AssetEntity, error) {
	filter, args := assetFilters(q, true)
	query := organizationAssetCTE + `SELECT e.id,e.asset_type,e.label,e.method,
	 CASE WHEN e.asset_type='root_domain' THEN LEAST(e.first_seen_at,COALESCE((SELECT MIN(child.first_seen_at) FROM asset_root_domain_visible_source rs JOIN asset_entity child ON child.tenant_id=rs.tenant_id AND child.id=rs.source_asset_id WHERE rs.tenant_id=e.tenant_id AND rs.root_asset_id=e.id),e.first_seen_at)) ELSE e.first_seen_at END AS first_seen_at,
	 CASE WHEN e.asset_type='root_domain' THEN GREATEST(e.last_seen_at,COALESCE((SELECT MAX(child.last_seen_at) FROM asset_root_domain_visible_source rs JOIN asset_entity child ON child.tenant_id=rs.tenant_id AND child.id=rs.source_asset_id WHERE rs.tenant_id=e.tenant_id AND rs.root_asset_id=e.id),e.last_seen_at)) ELSE e.last_seen_at END AS last_seen_at,
	 (SELECT COUNT(*) FROM asset_subdomain_binding b JOIN subdomain a ON a.id=b.subdomain_id JOIN target t ON t.id=a.target_id AND t.deleted_at IS NULL WHERE b.asset_id=e.id) +
	 (SELECT COUNT(*) FROM asset_website_binding b JOIN website a ON a.id=b.website_id JOIN target t ON t.id=a.target_id AND t.deleted_at IS NULL WHERE b.asset_id=e.id) +
	 (SELECT COUNT(*) FROM asset_endpoint_binding b JOIN endpoint a ON a.id=b.endpoint_id JOIN target t ON t.id=a.target_id AND t.deleted_at IS NULL WHERE b.asset_id=e.id) +
	 (SELECT COUNT(*) FROM asset_host_port_binding b JOIN host_port_mapping a ON a.id=b.host_port_id JOIN target t ON t.id=a.target_id AND t.deleted_at IS NULL WHERE b.asset_id=e.id) +
	 (SELECT COUNT(*) FROM asset_application_visible_website x JOIN website w ON w.id=x.website_id JOIN target t ON t.id=w.target_id AND t.deleted_at IS NULL WHERE x.tenant_id=e.tenant_id AND x.application_id=e.id) +
	 (SELECT COUNT(*) FROM asset_observation o LEFT JOIN target t ON t.id=o.target_id WHERE o.tenant_id=e.tenant_id AND o.asset_id=e.id AND (o.original_target_id IS NULL OR (o.target_id IS NOT NULL AND t.deleted_at IS NULL))) +
	 CASE WHEN e.asset_type='root_domain' THEN (SELECT COUNT(*) FROM asset_root_domain_visible_source rs WHERE rs.tenant_id=e.tenant_id AND rs.root_asset_id=e.id) + (SELECT COUNT(*) FROM asset_root_domain_target rt JOIN target t ON t.id=rt.target_id AND t.deleted_at IS NULL WHERE rt.tenant_id=e.tenant_id AND rt.root_asset_id=e.id) ELSE 0 END AS source_count,
	 (e.asset_type='root_domain' AND NOT e.is_manual AND NOT EXISTS(SELECT 1 FROM asset_observation o LEFT JOIN target t ON t.id=o.target_id WHERE o.tenant_id=e.tenant_id AND o.asset_id=e.id AND (o.original_target_id IS NULL OR (o.target_id IS NOT NULL AND t.deleted_at IS NULL)))) AS derived,
	 (SELECT COUNT(*) FROM asset_application_visible_website x JOIN website w ON w.id=x.website_id JOIN target t ON t.id=w.target_id AND t.deleted_at IS NULL WHERE x.tenant_id=e.tenant_id AND x.application_id=e.id) AS application_sources,
	 COALESCE((SELECT jsonb_agg(jsonb_build_object('id',o.id,'name',o.name) ORDER BY o.name,o.id) FROM org_assets a JOIN organization o ON o.id=a.org_id WHERE a.asset_id=e.id),'[]'::jsonb)::text AS organizations_json
	 FROM asset_entity e WHERE ` + filter + ` ORDER BY e.id DESC LIMIT ? OFFSET ?`
	args = append(args, q.Limit, q.Offset)
	var rows []struct {
		assetapp.AssetEntity `gorm:"embedded"`
		OrganizationsJSON    string `gorm:"column:organizations_json"`
	}
	if err := s.db.WithContext(ctx).Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]assetapp.AssetEntity, 0, len(rows))
	for _, row := range rows {
		item := row.AssetEntity
		if err := json.Unmarshal([]byte(row.OrganizationsJSON), &item.Organizations); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *AssetRegistryStore) CountAssets(ctx context.Context, q assetapp.AssetQuery) (int64, error) {
	filter, args := assetFilters(q, false)
	var count int64
	err := s.db.WithContext(ctx).Raw(organizationAssetCTE+`SELECT COUNT(*) FROM asset_entity e WHERE `+filter, args...).Scan(&count).Error
	return count, err
}

type backfillSpec struct {
	source, binding, sourceID, label, kind string
}

var backfillSources = map[string]backfillSpec{
	"subdomain": {"subdomain", "asset_subdomain_binding", "subdomain_id", "dns_name", "subdomain"},
	"website":   {"website", "asset_website_binding", "website_id", "url", "service"},
	"endpoint":  {"endpoint", "asset_endpoint_binding", "endpoint_id", "url", "endpoint"},
	"host_port": {"host_port_mapping", "asset_host_port_binding", "host_port_id", "host(ip)", "ip"},
}

func (s *AssetRegistryStore) BackfillBatch(ctx context.Context, batchSize int) (bool, error) {
	if batchSize < 1 || batchSize > 1000 {
		return false, errors.New("invalid asset backfill batch size")
	}
	complete := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var cursor persistence.BackfillCursor
		row := tx.Raw(`SELECT source_kind,last_id FROM asset_registry_backfill WHERE completed=FALSE ORDER BY source_kind FOR UPDATE SKIP LOCKED LIMIT 1`).Row()
		if err := row.Scan(&cursor.SourceKind, &cursor.LastID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				var remaining int64
				if err := tx.Raw(`SELECT COUNT(*) FROM asset_registry_backfill WHERE completed=FALSE`).Scan(&remaining).Error; err != nil {
					return err
				}
				complete = remaining == 0
				return nil
			}
			return err
		}
		spec, ok := backfillSources[cursor.SourceKind]
		if !ok {
			return fmt.Errorf("unknown asset backfill source %q", cursor.SourceKind)
		}
		// All SQL identifiers come from the fixed map above, never from a request.
		query := fmt.Sprintf(`WITH batch AS MATERIALIZED (
			SELECT id, %s AS label, created_at FROM %s WHERE id > ? ORDER BY id LIMIT ? FOR UPDATE
		), bound AS (
			INSERT INTO %s (%s, asset_id)
			SELECT id, tq_register_asset(?::uuid, ?, label, NULL, created_at) FROM batch
			ON CONFLICT (%s) DO UPDATE SET asset_id=EXCLUDED.asset_id RETURNING %s
		) SELECT COALESCE(MAX(%s), ?), COUNT(*) FROM bound`, spec.label, spec.source, spec.binding, spec.sourceID, spec.sourceID, spec.sourceID, spec.sourceID)
		var lastID, count int64
		if err := tx.Raw(query, cursor.LastID, batchSize, assetapp.DefaultTenantID, spec.kind, cursor.LastID).Row().Scan(&lastID, &count); err != nil {
			return err
		}
		return tx.Exec(`UPDATE asset_registry_backfill SET last_id=?, completed=?, updated_at=CURRENT_TIMESTAMP WHERE source_kind=?`, lastID, count < int64(batchSize), cursor.SourceKind).Error
	})
	return complete, err
}

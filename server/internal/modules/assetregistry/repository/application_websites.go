package repository

import (
	"context"
	"time"

	"github.com/lib/pq"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

func applicationSitesSQL(q assetapp.ApplicationWebsiteQuery) (string, []any) {
	query := organizationAssetCTE + `, app_site_candidates AS (
	 SELECT x.website_id,x.evidence_kind,x.evidence_value,0 priority FROM asset_application_visible_website x WHERE x.tenant_id=?::uuid AND x.application_id=?
	 UNION ALL
	 SELECT w.id,'binding'::text,domain.value,1 FROM asset_observation obs
	 LEFT JOIN target original_target ON original_target.id=obs.target_id
	 CROSS JOIN LATERAL jsonb_array_elements_text(CASE WHEN jsonb_typeof(obs.payload->'boundDomains')='array' THEN obs.payload->'boundDomains' ELSE '[]'::jsonb END) domain(value)
	 JOIN website w ON lower(rtrim(w.host,'.'))=lower(rtrim(domain.value,'.')) OR right(lower(rtrim(w.host,'.')),length(rtrim(domain.value,'.'))+1)='.'||lower(rtrim(domain.value,'.'))
	 WHERE obs.tenant_id=?::uuid AND obs.asset_id=? AND rtrim(domain.value,'.')<>'' AND (obs.original_target_id IS NULL OR original_target.id IS NOT NULL AND original_target.deleted_at IS NULL)
	 AND (obs.target_id=w.target_id OR EXISTS(SELECT 1 FROM org_targets ot WHERE ot.org_id=obs.organization_id AND ot.target_id=w.target_id) OR (obs.target_id IS NULL AND obs.organization_id IS NULL))
	), app_sites AS (SELECT DISTINCT ON(website_id) website_id,evidence_kind,evidence_value FROM app_site_candidates ORDER BY website_id,priority,evidence_value),
	 scoped_app_sites AS (SELECT s.*,w.url,w.host,COALESCE(w.title,'') title,w.status_code,COALESCE(w.tech,'{}'::varchar[]) tech,w.created_at first_seen_at
	 FROM app_sites s JOIN website w ON w.id=s.website_id JOIN target t ON t.id=w.target_id AND t.deleted_at IS NULL`
	args := []any{q.TenantID, q.ApplicationID, q.TenantID, q.ApplicationID}
	if q.OrganizationID > 0 {
		query += ` WHERE EXISTS(SELECT 1 FROM org_targets ot WHERE ot.org_id=? AND ot.target_id=w.target_id)`
		args = append(args, q.OrganizationID)
	}
	return query + `) `, args
}

func (s *AssetRegistryStore) ListApplicationWebsites(ctx context.Context, q assetapp.ApplicationWebsiteQuery) ([]assetapp.ApplicationWebsite, int64, error) {
	base, args := applicationSitesSQL(q)
	var total int64
	if err := s.db.WithContext(ctx).Raw(base+`SELECT COUNT(*) FROM scoped_app_sites`, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	query := base + `SELECT website_id AS id,url,host,title,status_code,tech,evidence_kind,evidence_value,first_seen_at FROM scoped_app_sites ORDER BY first_seen_at DESC,website_id DESC LIMIT ? OFFSET ?`
	args = append(args, q.Limit, q.Offset)
	var rows []struct {
		ID                          int64
		URL, Host, Title            string
		StatusCode                  *int64
		Tech                        pq.StringArray `gorm:"type:text[]"`
		EvidenceKind, EvidenceValue string
		FirstSeenAt                 time.Time
	}
	if err := s.db.WithContext(ctx).Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	items := make([]assetapp.ApplicationWebsite, 0, len(rows))
	for _, v := range rows {
		tech := []string(v.Tech)
		if tech == nil {
			tech = []string{}
		}
		items = append(items, assetapp.ApplicationWebsite{ID: v.ID, URL: v.URL, Host: v.Host, Title: v.Title, StatusCode: v.StatusCode, Technologies: tech, EvidenceKind: v.EvidenceKind, EvidenceValue: v.EvidenceValue, FirstSeenAt: v.FirstSeenAt})
	}
	return items, total, nil
}

package repository

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

func portSources(q assetapp.PortQuery, includeCanonicalServices bool) (string, []any) {
	filter, filterArgs := assetFilters(assetapp.AssetQuery{HideCDN: q.HideCDN, TenantID: q.TenantID, OrganizationID: q.OrganizationID, RootDomainID: q.RootDomainID}, false)
	// Coverage already loaded canonical services/endpoints. Only project extra
	// observations there, without re-evaluating the expensive asset scope.
	if !includeCanonicalServices {
		filter = "(" + filter + ") AND FALSE"
	}
	query := organizationAssetCTE + `, service_assets AS (
	 SELECT e.id source_asset_id,NULL::integer source_target_id,NULL::integer source_organization_id,tq_inventory_host_port(e.label) label,e.first_seen_at,e.last_seen_at,
	 COALESCE((SELECT COALESCE(CASE WHEN lower(COALESCE(o.payload->>'serviceName','')) NOT IN ('','unknown','tcp','udp') THEN o.payload->>'serviceName' END,CASE WHEN lower(COALESCE(o.payload->'extra'->>'protocol','')) NOT IN ('','unknown','tcp','udp') THEN o.payload->'extra'->>'protocol' END)
	   FROM asset_observation o LEFT JOIN target t ON t.id=o.target_id WHERE o.tenant_id=e.tenant_id AND o.asset_id=e.id AND (o.original_target_id IS NULL OR t.id IS NOT NULL AND t.deleted_at IS NULL)
	   AND (?=0 OR o.organization_id=? OR EXISTS(SELECT 1 FROM org_targets ot WHERE ot.org_id=? AND ot.target_id=o.target_id))
	   AND (lower(COALESCE(o.payload->>'serviceName','')) NOT IN ('','unknown','tcp','udp') OR lower(COALESCE(o.payload->'extra'->>'protocol','')) NOT IN ('','unknown','tcp','udp')) ORDER BY o.observed_at DESC,o.id DESC LIMIT 1),
	   CASE WHEN e.label ILIKE 'https://%' THEN 'HTTPS' WHEN e.label ILIKE 'http://%' THEN 'HTTP' END,'') observed_service
	 FROM asset_entity e WHERE e.asset_type IN ('service','endpoint') AND ` + filter + `
	), host_sources AS (
	 SELECT h.host,h.ip,h.port,h.target_id,b.asset_id,e.last_seen_at,h.created_at FROM host_port_mapping h JOIN target t ON t.id=h.target_id AND t.deleted_at IS NULL
	 JOIN asset_host_port_binding b ON b.host_port_id=h.id JOIN asset_entity e ON e.id=b.asset_id WHERE e.tenant_id=?`
	args := []any{q.OrganizationID, q.OrganizationID, q.OrganizationID}
	args = append(args, filterArgs...)
	args = append(args, q.TenantID)
	if q.HideCDN {
		query += ` AND NOT public.tq_inventory_is_cdn(h.ip,e.tenant_id,?,0)`
		args = append(args, q.OrganizationID)
	}
	if q.OrganizationID > 0 {
		query += ` AND EXISTS(SELECT 1 FROM org_targets ot WHERE ot.org_id=? AND ot.target_id=h.target_id)`
		args = append(args, q.OrganizationID)
	}
	if q.RootDomainID > 0 {
		query += ` AND EXISTS(SELECT 1 FROM asset_entity root WHERE root.id=? AND root.tenant_id=e.tenant_id AND root.asset_type='root_domain' AND (lower(h.host)=root.label OR right(lower(h.host),length(root.label)+1)='.'||root.label))`
		args = append(args, q.RootDomainID)
	}
	query += `), observed_ports AS (
	 SELECT e.id source_asset_id,o.target_id source_target_id,o.organization_id source_organization_id,
	 (CASE WHEN position(':' IN e.label)>0 THEN '['||e.label||']' ELSE e.label END)||':'||(CASE WHEN (p.value->>'port') ~ '^[0-9]{1,5}$' THEN (p.value->>'port')::integer END)::text label,
	 o.observed_at first_seen_at,o.observed_at last_seen_at,
	 CASE WHEN lower(COALESCE(p.value->>'service','')) NOT IN ('','unknown','tcp','udp') THEN p.value->>'service' ELSE '' END observed_service
	 FROM asset_observation o JOIN asset_entity e ON e.id=o.asset_id AND e.tenant_id=o.tenant_id LEFT JOIN target t ON t.id=o.target_id
	 CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(o.payload->'openPorts')='array' THEN o.payload->'openPorts' ELSE '[]'::jsonb END) p(value)
	 WHERE e.tenant_id=? AND e.asset_type='ip' AND (o.original_target_id IS NULL OR t.id IS NOT NULL AND t.deleted_at IS NULL)
	 AND (CASE WHEN (p.value->>'port') ~ '^[0-9]{1,5}$' THEN (p.value->>'port')::integer END) BETWEEN 1 AND 65535`
	args = append(args, q.TenantID)
	if q.HideCDN {
		query += ` AND NOT public.tq_inventory_cdn_asset('ip',e.label,e.tenant_id,?,0)`
		args = append(args, q.OrganizationID)
	}
	if q.OrganizationID > 0 {
		query += ` AND (o.organization_id=? OR EXISTS(SELECT 1 FROM org_targets ot WHERE ot.org_id=? AND ot.target_id=o.target_id))`
		args = append(args, q.OrganizationID, q.OrganizationID)
	}
	if q.RootDomainID > 0 {
		query += ` AND EXISTS(SELECT 1 FROM asset_entity root CROSS JOIN LATERAL jsonb_array_elements_text(CASE WHEN jsonb_typeof(o.payload->'boundDomains')='array' THEN o.payload->'boundDomains' ELSE '[]'::jsonb END) domain(value) WHERE root.id=? AND root.tenant_id=e.tenant_id AND root.asset_type='root_domain' AND (lower(domain.value)=root.label OR right(lower(domain.value),length(root.label)+1)='.'||root.label))`
		args = append(args, q.RootDomainID)
	}
	query += `), raw_port_sources AS (
	 SELECT source_asset_id,source_target_id,source_organization_id,label,first_seen_at,last_seen_at,observed_service FROM service_assets WHERE label IS NOT NULL
	 UNION ALL SELECT asset_id,target_id,NULL::integer,lower(rtrim(host,'.'))||':'||port::text,created_at,created_at,''::text FROM host_sources WHERE host ~* '^[a-z0-9][a-z0-9.-]*[a-z0-9]$'
	 UNION ALL SELECT asset_id,target_id,NULL::integer,(CASE WHEN family(ip)=6 THEN '['||host(ip)||']' ELSE host(ip) END)||':'||port::text,created_at,created_at,''::text FROM host_sources
	 UNION ALL SELECT source_asset_id,source_target_id,source_organization_id,label,first_seen_at,last_seen_at,observed_service FROM observed_ports
	), port_sources AS (SELECT * FROM raw_port_sources WHERE label IS NOT NULL`
	if q.Query != "" {
		pattern := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(q.Query)
		query += ` AND label ILIKE ? ESCAPE '\'`
		args = append(args, "%"+pattern+"%")
	}
	return query + `) `, args
}

func (s *AssetRegistryStore) ListPorts(ctx context.Context, q assetapp.PortQuery) ([]assetapp.PortAsset, int64, error) {
	base, args := portSources(q, true)
	var total int64
	if err := s.db.WithContext(ctx).Raw(base+`SELECT COUNT(DISTINCT label) FROM port_sources`, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	query := base + `, page_sources AS (
	 SELECT p.* FROM port_sources p JOIN (SELECT label FROM port_sources GROUP BY label ORDER BY label LIMIT ? OFFSET ?) selected ON selected.label=p.label
	), port_organizations AS (
	 SELECT DISTINCT p.label,ot.org_id FROM page_sources p JOIN org_targets ot ON ot.target_id=p.source_target_id WHERE p.source_target_id IS NOT NULL
	 UNION SELECT DISTINCT p.label,p.source_organization_id FROM page_sources p WHERE p.source_organization_id IS NOT NULL
	 UNION SELECT DISTINCT p.label,oa.org_id FROM page_sources p JOIN org_assets oa ON oa.asset_id=p.source_asset_id WHERE p.source_target_id IS NULL AND p.source_organization_id IS NULL
	), organization_names AS (
	 SELECT po.label,jsonb_agg(jsonb_build_object('id',o.id,'name',o.name) ORDER BY o.name,o.id) organizations FROM port_organizations po JOIN organization o ON o.id=po.org_id AND o.deleted_at IS NULL GROUP BY po.label
	)
	 SELECT p.label,substring(p.label FROM ':([0-9]+)$')::integer port,MIN(p.first_seen_at) first_seen_at,MAX(p.last_seen_at) last_seen_at,
	 COALESCE(string_agg(DISTINCT NULLIF(p.observed_service,''), ', '),'') service,COALESCE(o.organizations,'[]'::jsonb)::text organizations_json
	 FROM page_sources p LEFT JOIN organization_names o ON o.label=p.label GROUP BY p.label,o.organizations ORDER BY p.label`
	args = append(args, q.Limit, q.Offset)
	var rows []struct {
		Label                   string
		Port                    int
		Service                 string
		FirstSeenAt, LastSeenAt time.Time
		OrganizationsJSON       string `gorm:"column:organizations_json"`
	}
	if err := s.db.WithContext(ctx).Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	items := make([]assetapp.PortAsset, 0, len(rows))
	for _, row := range rows {
		item := assetapp.PortAsset{Label: row.Label, Port: row.Port, Service: row.Service, FirstSeenAt: row.FirstSeenAt, LastSeenAt: row.LastSeenAt}
		if err := json.Unmarshal([]byte(row.OrganizationsJSON), &item.Organizations); err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, nil
}

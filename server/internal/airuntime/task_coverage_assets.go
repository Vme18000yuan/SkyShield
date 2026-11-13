package airuntime

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"
)

// CoverageAssetProjection is the non-secret subset used by the task graph.
type CoverageAssetProjection struct {
	ID          int64   `json:"id"`
	Type        string  `json:"type"`
	Label       string  `json:"label"`
	Domain      string  `json:"domain"`
	RootDomain  string  `json:"root_domain"`
	IP          string  `json:"ip"`
	URL         string  `json:"url"`
	Port        *int    `json:"port"`
	ServiceType string  `json:"service_type"`
	AppName     string  `json:"app_name"`
	PageTitle   string  `json:"page_title"`
	StatusCode  *int    `json:"status_code"`
	CompanyIDs  []int64 `json:"company_ids"`
	Tested      bool    `json:"-"`
}

const humanTaskAssetPermissionSQL = `WITH permitted AS (
 SELECT t.id,t.tenant_id,t.owner_user_id,t.target_id FROM public.ai_task t
 JOIN public.tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id JOIN public.auth_user u ON u.id=m.user_id JOIN public.tenant tenant ON tenant.id=m.tenant_id
 WHERE t.id=$1 AND t.owner_user_id=$2 AND t.deleted_at IS NULL AND u.is_active AND tenant.status='active'
 AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM public.target WHERE id=t.target_id AND deleted_at IS NULL))
)`

// TaskCoverageAssets reads the same unified assets as list_assets, with a
// bounded graph size. The caller must additionally authorize the task owner.
func TaskCoverageAssets(ctx context.Context, db *sql.DB, user int, taskID int64) ([]CoverageAssetProjection, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var permitted bool
	if err := db.QueryRowContext(ctx, humanTaskAssetPermissionSQL+` SELECT EXISTS(SELECT 1 FROM permitted)`, taskID, user).Scan(&permitted); err != nil {
		return nil, false, err
	}
	if !permitted {
		return nil, false, sql.ErrNoRows
	}
	rows, err := db.QueryContext(ctx, humanTaskAssetPermissionSQL+` SELECT a.asset_id,a.tested FROM permitted p CROSS JOIN LATERAL tianqiong_ai.task_scope_assets(p.id,p.owner_user_id) a ORDER BY a.asset_id DESC LIMIT 5001`, taskID, user)
	if err != nil {
		return nil, false, err
	}
	ids := []int64{}
	tested := map[int64]bool{}
	for rows.Next() {
		var id int64
		var value bool
		if err = rows.Scan(&id, &value); err != nil {
			rows.Close()
			return nil, false, err
		}
		ids = append(ids, id)
		tested[id] = value
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, false, err
	}
	rows.Close()
	truncated := len(ids) > 5000
	if truncated {
		ids = ids[:5000]
	}
	items := make([]CoverageAssetProjection, 0, len(ids))
	for start := 0; start < len(ids); start += 250 {
		end := start + 250
		if end > len(ids) {
			end = len(ids)
		}
		query := humanTaskAssetPermissionSQL + coverageAssetDetailsSQL
		details, err := db.QueryContext(ctx, query, taskID, user, pq.Int64Array(ids[start:end]))
		if err != nil {
			return nil, false, err
		}
		batch := []CoverageAssetProjection{}
		index := map[int64]int{}
		for details.Next() {
			var item CoverageAssetProjection
			if err = details.Scan(&item.ID, &item.Type, &item.Label, &item.Domain, &item.RootDomain, &item.IP, &item.URL, &item.Port, &item.ServiceType, &item.AppName, &item.PageTitle, &item.StatusCode); err != nil {
				details.Close()
				return nil, false, err
			}
			item.Tested = tested[item.ID]
			index[item.ID] = len(batch)
			batch = append(batch, item)
		}
		if err = details.Err(); err != nil {
			details.Close()
			return nil, false, err
		}
		details.Close()
		companies, err := db.QueryContext(ctx, humanTaskAssetPermissionSQL+coverageAssetCompaniesSQL, taskID, user, pq.Int64Array(ids[start:end]))
		if err != nil {
			return nil, false, err
		}
		for companies.Next() {
			var id int64
			var companyIDs pq.Int64Array
			if err = companies.Scan(&id, &companyIDs); err != nil {
				companies.Close()
				return nil, false, err
			}
			if pos, ok := index[id]; ok {
				batch[pos].CompanyIDs = append([]int64(nil), companyIDs...)
			}
		}
		if err = companies.Err(); err != nil {
			companies.Close()
			return nil, false, err
		}
		companies.Close()
		items = append(items, batch...)
	}
	if err = db.QueryRowContext(ctx, humanTaskAssetPermissionSQL+` SELECT EXISTS(SELECT 1 FROM permitted)`, taskID, user).Scan(&permitted); err != nil {
		return nil, false, err
	}
	if !permitted {
		return nil, false, errors.New("task coverage scope changed")
	}
	return items, truncated, nil
}

const coverageAssetDetailsSQL = ` SELECT e.id,e.asset_type::text,e.label,
 COALESCE(NULLIF(obs.payload->>'domain',''),CASE WHEN e.asset_type IN ('root_domain','subdomain','service','endpoint') THEN tianqiong_ai.scope_host(e.label) END,''),
 COALESCE(CASE WHEN e.asset_type='root_domain' THEN e.label END,(SELECT root.label FROM public.asset_root_domain_source r JOIN public.asset_entity root ON root.id=r.root_asset_id AND root.tenant_id=r.tenant_id WHERE r.tenant_id=e.tenant_id AND r.source_asset_id=e.id LIMIT 1),''),
 COALESCE(NULLIF(obs.payload->>'ip',''),NULLIF(obs.payload->>'serviceIp',''),NULLIF(obs.payload->'extra'->>'ip',''),CASE WHEN e.asset_type='ip' THEN e.label END,CASE WHEN tianqiong_ai.scope_inet(tianqiong_ai.scope_host(e.label)) IS NOT NULL THEN tianqiong_ai.scope_host(e.label) END,''),
 COALESCE(CASE WHEN e.asset_type IN ('service','endpoint') AND e.label ~* '^https?://' THEN e.label END,''),
 CASE WHEN COALESCE(obs.payload->>'port','') ~ '^[0-9]{1,5}$' THEN (obs.payload->>'port')::integer ELSE substring(public.tq_inventory_host_port(e.label) FROM ':([0-9]+)$')::integer END,
 CASE WHEN e.asset_type='service' AND e.label ~* '^https?://' THEN 'http' WHEN e.asset_type='service' THEN 'other' ELSE '' END,
 COALESCE(CASE WHEN e.asset_type='app' THEN e.label END,''),COALESCE(NULLIF(obs.payload->>'pageTitle',''),web.title,''),
 COALESCE(web.status_code,CASE WHEN COALESCE(obs.payload->>'statusCode','') ~ '^[0-9]{1,6}$' THEN (obs.payload->>'statusCode')::integer END)
 FROM permitted p JOIN public.asset_entity e ON e.tenant_id=p.tenant_id AND e.id=ANY($3::bigint[])
 LEFT JOIN LATERAL (SELECT o.payload FROM public.asset_observation o LEFT JOIN public.target t ON t.id=o.target_id WHERE o.tenant_id=e.tenant_id AND o.asset_id=e.id AND (p.target_id IS NULL OR o.target_id=p.target_id) AND (o.original_target_id IS NULL OR t.id IS NOT NULL AND t.deleted_at IS NULL) ORDER BY o.id DESC LIMIT 1) obs ON TRUE
 LEFT JOIN LATERAL (SELECT x.title,x.status_code FROM (
  SELECT w.id,w.title,w.status_code FROM public.asset_website_binding b JOIN public.website w ON w.id=b.website_id JOIN public.target t ON t.id=w.target_id AND t.deleted_at IS NULL WHERE b.asset_id=e.id AND (p.target_id IS NULL OR w.target_id=p.target_id)
  UNION ALL SELECT a.id,a.title,a.status_code FROM public.asset_endpoint_binding b JOIN public.endpoint a ON a.id=b.endpoint_id JOIN public.target t ON t.id=a.target_id AND t.deleted_at IS NULL WHERE b.asset_id=e.id AND (p.target_id IS NULL OR a.target_id=p.target_id)
 ) x ORDER BY x.id DESC LIMIT 1) web ON TRUE ORDER BY e.id DESC`

const coverageAssetCompaniesSQL = `, links AS (
 SELECT b.asset_id,a.target_id FROM public.asset_subdomain_binding b JOIN public.subdomain a ON a.id=b.subdomain_id WHERE b.asset_id=ANY($3::bigint[])
 UNION SELECT b.asset_id,a.target_id FROM public.asset_subdomain_variant_binding b JOIN public.subdomain a ON a.id=b.subdomain_id WHERE b.asset_id=ANY($3::bigint[])
 UNION SELECT b.asset_id,a.target_id FROM public.asset_website_binding b JOIN public.website a ON a.id=b.website_id WHERE b.asset_id=ANY($3::bigint[])
 UNION SELECT b.asset_id,a.target_id FROM public.asset_endpoint_binding b JOIN public.endpoint a ON a.id=b.endpoint_id WHERE b.asset_id=ANY($3::bigint[])
 UNION SELECT b.asset_id,a.target_id FROM public.asset_endpoint_variant_binding b JOIN public.endpoint a ON a.id=b.endpoint_id WHERE b.asset_id=ANY($3::bigint[])
 UNION SELECT b.asset_id,a.target_id FROM public.asset_host_port_binding b JOIN public.host_port_mapping a ON a.id=b.host_port_id WHERE b.asset_id=ANY($3::bigint[])
 UNION SELECT o.asset_id,o.target_id FROM public.asset_observation o WHERE o.asset_id=ANY($3::bigint[]) AND o.target_id IS NOT NULL
 UNION SELECT x.application_id,w.target_id FROM public.asset_application_visible_website x JOIN public.website w ON w.id=x.website_id WHERE x.application_id=ANY($3::bigint[])
 UNION SELECT r.root_asset_id,r.target_id FROM public.asset_root_domain_target r WHERE r.root_asset_id=ANY($3::bigint[])
 UNION SELECT r.root_asset_id,o.target_id FROM public.asset_root_domain_visible_source r JOIN public.asset_observation o ON o.tenant_id=r.tenant_id AND o.asset_id=r.source_asset_id WHERE r.root_asset_id=ANY($3::bigint[]) AND o.target_id IS NOT NULL
 UNION SELECT r.root_asset_id,a.target_id FROM public.asset_root_domain_visible_source r JOIN public.asset_website_binding b ON b.asset_id=r.source_asset_id JOIN public.website a ON a.id=b.website_id WHERE r.root_asset_id=ANY($3::bigint[])
 UNION SELECT r.root_asset_id,a.target_id FROM public.asset_root_domain_visible_source r JOIN public.asset_subdomain_binding b ON b.asset_id=r.source_asset_id JOIN public.subdomain a ON a.id=b.subdomain_id WHERE r.root_asset_id=ANY($3::bigint[])
), orgs AS (
 SELECT l.asset_id,ot.organization_id::bigint AS org_id FROM links l JOIN public.target t ON t.id=l.target_id AND t.deleted_at IS NULL JOIN public.organization_target ot ON ot.target_id=l.target_id JOIN public.organization org ON org.id=ot.organization_id AND org.deleted_at IS NULL JOIN permitted p ON p.target_id IS NULL OR p.target_id=l.target_id
 UNION SELECT o.asset_id,o.organization_id::bigint FROM public.asset_observation o JOIN public.organization org ON org.id=o.organization_id AND org.deleted_at IS NULL LEFT JOIN public.target t ON t.id=o.target_id JOIN permitted p ON p.tenant_id=o.tenant_id AND (p.target_id IS NULL OR p.target_id=o.target_id) WHERE o.asset_id=ANY($3::bigint[]) AND (o.original_target_id IS NULL OR t.id IS NOT NULL AND t.deleted_at IS NULL)
) SELECT asset_id,array_agg(DISTINCT org_id ORDER BY org_id) FROM orgs GROUP BY asset_id`

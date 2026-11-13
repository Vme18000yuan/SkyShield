package repository

import (
	"strings"

	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

// Organization membership follows active targets and explicit organization
// observations. Derived roots inherit only their own visible source links.
const organizationAssetCTE = `WITH org_targets AS (
 SELECT ot.organization_id AS org_id,t.id AS target_id FROM organization_target ot JOIN organization o ON o.id=ot.organization_id AND o.deleted_at IS NULL JOIN target t ON t.id=ot.target_id AND t.deleted_at IS NULL
), org_direct AS (
 SELECT ot.org_id,b.asset_id FROM org_targets ot JOIN subdomain a ON a.target_id=ot.target_id JOIN asset_subdomain_binding b ON b.subdomain_id=a.id
 UNION SELECT ot.org_id,b.asset_id FROM org_targets ot JOIN subdomain a ON a.target_id=ot.target_id JOIN asset_subdomain_variant_binding b ON b.subdomain_id=a.id
 UNION SELECT ot.org_id,b.asset_id FROM org_targets ot JOIN website a ON a.target_id=ot.target_id JOIN asset_website_binding b ON b.website_id=a.id
 UNION SELECT ot.org_id,b.asset_id FROM org_targets ot JOIN endpoint a ON a.target_id=ot.target_id JOIN asset_endpoint_binding b ON b.endpoint_id=a.id
 UNION SELECT ot.org_id,b.asset_id FROM org_targets ot JOIN endpoint a ON a.target_id=ot.target_id JOIN asset_endpoint_variant_binding b ON b.endpoint_id=a.id
 UNION SELECT ot.org_id,b.asset_id FROM org_targets ot JOIN host_port_mapping a ON a.target_id=ot.target_id JOIN asset_host_port_binding b ON b.host_port_id=a.id
 UNION SELECT ot.org_id,x.application_id FROM org_targets ot JOIN website w ON w.target_id=ot.target_id JOIN asset_application_visible_website x ON x.website_id=w.id
 UNION SELECT ot.org_id,a.asset_id FROM org_targets ot JOIN asset_observation a ON a.target_id=ot.target_id
 UNION SELECT o.id,a.asset_id FROM organization o JOIN asset_observation a ON a.organization_id=o.id LEFT JOIN target t ON t.id=a.target_id WHERE o.deleted_at IS NULL AND (a.original_target_id IS NULL OR t.id IS NOT NULL AND t.deleted_at IS NULL)
), org_assets AS (
 SELECT org_id,asset_id FROM org_direct
 UNION SELECT d.org_id,r.root_asset_id FROM org_direct d JOIN asset_root_domain_visible_source r ON r.source_asset_id=d.asset_id
 UNION SELECT ot.org_id,r.root_asset_id FROM org_targets ot JOIN asset_root_domain_target r ON r.target_id=ot.target_id
 UNION SELECT d.org_id,root.id FROM org_direct d JOIN asset_observation obs ON obs.asset_id=d.asset_id JOIN asset_entity child ON child.id=obs.asset_id AND child.tenant_id=obs.tenant_id AND child.asset_type IN ('ip','app')
 LEFT JOIN target t ON t.id=obs.target_id CROSS JOIN LATERAL jsonb_array_elements_text(CASE WHEN jsonb_typeof(obs.payload->'boundDomains')='array' THEN obs.payload->'boundDomains' ELSE '[]'::jsonb END) domain(value)
 JOIN asset_entity root ON root.tenant_id=obs.tenant_id AND root.asset_type='root_domain' AND (lower(domain.value)=root.label OR right(lower(domain.value),length(root.label)+1)='.'||root.label)
 WHERE (obs.original_target_id IS NULL OR t.id IS NOT NULL AND t.deleted_at IS NULL) AND (obs.organization_id=d.org_id OR EXISTS(SELECT 1 FROM org_targets ot WHERE ot.org_id=d.org_id AND ot.target_id=obs.target_id))
) `

func assetFilters(q assetapp.AssetQuery, includeCursor bool) (string, []any) {
	where := []string{`e.tenant_id = ?`, `(e.is_manual OR
	 EXISTS(SELECT 1 FROM asset_observation o LEFT JOIN target t ON t.id=o.target_id WHERE o.tenant_id=e.tenant_id AND o.asset_id=e.id AND (o.original_target_id IS NULL OR t.id IS NOT NULL AND t.deleted_at IS NULL)) OR
	 EXISTS(SELECT 1 FROM asset_subdomain_binding b JOIN subdomain a ON a.id=b.subdomain_id JOIN target t ON t.id=a.target_id AND t.deleted_at IS NULL WHERE b.asset_id=e.id) OR
	 EXISTS(SELECT 1 FROM asset_subdomain_variant_binding b JOIN subdomain a ON a.id=b.subdomain_id JOIN target t ON t.id=a.target_id AND t.deleted_at IS NULL WHERE b.asset_id=e.id) OR
	 EXISTS(SELECT 1 FROM asset_website_binding b JOIN website a ON a.id=b.website_id JOIN target t ON t.id=a.target_id AND t.deleted_at IS NULL WHERE b.asset_id=e.id) OR
	 EXISTS(SELECT 1 FROM asset_endpoint_binding b JOIN endpoint a ON a.id=b.endpoint_id JOIN target t ON t.id=a.target_id AND t.deleted_at IS NULL WHERE b.asset_id=e.id) OR
	 EXISTS(SELECT 1 FROM asset_endpoint_variant_binding b JOIN endpoint a ON a.id=b.endpoint_id JOIN target t ON t.id=a.target_id AND t.deleted_at IS NULL WHERE b.asset_id=e.id) OR
	 EXISTS(SELECT 1 FROM asset_host_port_binding b JOIN host_port_mapping a ON a.id=b.host_port_id JOIN target t ON t.id=a.target_id AND t.deleted_at IS NULL WHERE b.asset_id=e.id) OR
	 EXISTS(SELECT 1 FROM asset_application_visible_website x JOIN website w ON w.id=x.website_id JOIN target t ON t.id=w.target_id AND t.deleted_at IS NULL WHERE x.tenant_id=e.tenant_id AND x.application_id=e.id) OR
	 EXISTS(SELECT 1 FROM asset_root_domain_visible_source rs WHERE rs.tenant_id=e.tenant_id AND rs.root_asset_id=e.id) OR
	 EXISTS(SELECT 1 FROM asset_root_domain_target rt JOIN target t ON t.id=rt.target_id AND t.deleted_at IS NULL WHERE rt.tenant_id=e.tenant_id AND rt.root_asset_id=e.id))`}
	args := []any{q.TenantID}
	if q.AssetType != "" {
		where = append(where, `e.asset_type = ?`)
		args = append(args, q.AssetType)
	}
	if q.AssetCategory == "web" {
		where = append(where, `e.asset_type IN ('service','endpoint') AND (e.label ILIKE 'http://%' OR e.label ILIKE 'https://%')`)
	}
	if q.AssetCategory == "non_web_service" {
		where = append(where, `e.asset_type='service' AND NOT (e.label ILIKE 'http://%' OR e.label ILIKE 'https://%')`)
	}
	if q.ExcludeNonWebServices {
		where = append(where, `(e.asset_type<>'service' OR e.label ILIKE 'http://%' OR e.label ILIKE 'https://%')`)
	}
	if q.Query != "" {
		pattern := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(q.Query)
		where = append(where, `e.label ILIKE ? ESCAPE '\'`)
		args = append(args, "%"+pattern+"%")
	}
	if includeCursor && q.BeforeID > 0 {
		where = append(where, `e.id < ?`)
		args = append(args, q.BeforeID)
	}
	if q.AssetID > 0 {
		where = append(where, `e.id = ?`)
		args = append(args, q.AssetID)
	}
	if q.OrganizationID > 0 {
		where = append(where, `EXISTS(SELECT 1 FROM org_assets oa WHERE oa.org_id=? AND oa.asset_id=e.id)`)
		args = append(args, q.OrganizationID)
	}
	if q.TargetID > 0 {
		where = append(where, `(EXISTS(SELECT 1 FROM asset_website_binding b JOIN website a ON a.id=b.website_id WHERE b.asset_id=e.id AND a.target_id=?) OR EXISTS(SELECT 1 FROM asset_endpoint_binding b JOIN endpoint a ON a.id=b.endpoint_id WHERE b.asset_id=e.id AND a.target_id=?) OR EXISTS(SELECT 1 FROM asset_subdomain_binding b JOIN subdomain a ON a.id=b.subdomain_id WHERE b.asset_id=e.id AND a.target_id=?) OR EXISTS(SELECT 1 FROM asset_host_port_binding b JOIN host_port_mapping a ON a.id=b.host_port_id WHERE b.asset_id=e.id AND a.target_id=?) OR EXISTS(SELECT 1 FROM asset_observation o WHERE o.asset_id=e.id AND o.tenant_id=e.tenant_id AND o.target_id=?) OR EXISTS(SELECT 1 FROM asset_application_visible_website x JOIN website w ON w.id=x.website_id JOIN target t ON t.id=w.target_id AND t.deleted_at IS NULL WHERE x.tenant_id=e.tenant_id AND x.application_id=e.id AND w.target_id=?) OR EXISTS(SELECT 1 FROM asset_root_domain_target rt JOIN target t ON t.id=rt.target_id AND t.deleted_at IS NULL WHERE rt.tenant_id=e.tenant_id AND rt.root_asset_id=e.id AND rt.target_id=? AND t.type='domain' AND t.name=e.label))`)
		for range 7 {
			args = append(args, q.TargetID)
		}
	}
	if q.RootDomainID > 0 {
		where = append(where, `(EXISTS(SELECT 1 FROM asset_root_domain_visible_source rs WHERE rs.tenant_id=e.tenant_id AND rs.root_asset_id=? AND rs.source_asset_id=e.id) OR
		 (e.asset_type='ip' AND EXISTS(SELECT 1 FROM asset_host_port_binding b JOIN host_port_mapping h ON h.id=b.host_port_id JOIN target t ON t.id=h.target_id AND t.deleted_at IS NULL JOIN asset_entity root ON root.id=? AND root.tenant_id=e.tenant_id AND root.asset_type='root_domain' WHERE b.asset_id=e.id AND (lower(h.host)=root.label OR right(lower(h.host),length(root.label)+1)='.'||root.label) AND (?=0 OR EXISTS(SELECT 1 FROM org_targets ot WHERE ot.org_id=? AND ot.target_id=h.target_id)))) OR
		 (e.asset_type='ip' AND EXISTS(SELECT 1 FROM asset_root_domain_visible_source rs JOIN asset_observation o ON o.tenant_id=rs.tenant_id AND o.asset_id=rs.source_asset_id LEFT JOIN target t ON t.id=o.target_id WHERE rs.tenant_id=e.tenant_id AND rs.root_asset_id=? AND (o.original_target_id IS NULL OR t.id IS NOT NULL AND t.deleted_at IS NULL) AND e.label=COALESCE(NULLIF(o.payload->>'ip',''),NULLIF(o.payload->>'serviceIp',''),o.payload->'extra'->>'ip') AND (?=0 OR o.organization_id=? OR EXISTS(SELECT 1 FROM org_targets ot WHERE ot.org_id=? AND ot.target_id=o.target_id)))) OR
		 (e.asset_type IN ('ip','app') AND EXISTS(SELECT 1 FROM asset_observation o LEFT JOIN target t ON t.id=o.target_id JOIN asset_entity root ON root.id=? AND root.tenant_id=e.tenant_id AND root.asset_type='root_domain' CROSS JOIN LATERAL jsonb_array_elements_text(CASE WHEN jsonb_typeof(o.payload->'boundDomains')='array' THEN o.payload->'boundDomains' ELSE '[]'::jsonb END) domain(value) WHERE o.tenant_id=e.tenant_id AND o.asset_id=e.id AND (o.original_target_id IS NULL OR t.id IS NOT NULL AND t.deleted_at IS NULL) AND (lower(domain.value)=root.label OR right(lower(domain.value),length(root.label)+1)='.'||root.label) AND (?=0 OR o.organization_id=? OR EXISTS(SELECT 1 FROM org_targets ot WHERE ot.org_id=? AND ot.target_id=o.target_id)))) OR
		 (e.asset_type='app' AND EXISTS(SELECT 1 FROM asset_application_visible_website x JOIN website w ON w.id=x.website_id JOIN target t ON t.id=w.target_id AND t.deleted_at IS NULL JOIN asset_website_binding b ON b.website_id=w.id JOIN asset_root_domain_visible_source rs ON rs.source_asset_id=b.asset_id AND rs.tenant_id=x.tenant_id WHERE x.tenant_id=e.tenant_id AND x.application_id=e.id AND rs.root_asset_id=? AND (?=0 OR EXISTS(SELECT 1 FROM org_targets ot WHERE ot.org_id=? AND ot.target_id=w.target_id)))))`)
		args = append(args, q.RootDomainID, q.RootDomainID, q.OrganizationID, q.OrganizationID, q.RootDomainID, q.OrganizationID, q.OrganizationID, q.OrganizationID, q.RootDomainID, q.OrganizationID, q.OrganizationID, q.OrganizationID, q.RootDomainID, q.OrganizationID, q.OrganizationID)
	}
	return strings.Join(where, " AND "), args
}

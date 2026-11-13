-- Statistical identities do not replace the original URL and service records.
-- They collapse URL paths and HTTP methods to a normalized host and port.
CREATE FUNCTION tq_inventory_host_port(raw text) RETURNS text
LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
DECLARE
    authority text;
    parts text[];
    host_name text;
    port_number integer;
    default_port integer;
BEGIN
    IF raw ~* '^https?://' THEN
        default_port := CASE WHEN raw ~* '^https://' THEN 443 ELSE 80 END;
        authority := regexp_replace(raw, '^https?://', '', 'i');
        authority := regexp_replace(authority, '[/\?#].*$', '');
        authority := regexp_replace(authority, '^.*@', '');
    ELSE
        authority := raw;
    END IF;
    parts := regexp_match(authority, '^(\[[0-9a-fA-F:.]+\]|[^[:space:]:/?#@]+)(:([0-9]{1,5}))?$');
    IF parts IS NULL THEN RETURN NULL; END IF;
    host_name := lower(rtrim(parts[1], '.'));
    IF host_name = '' THEN RETURN NULL; END IF;
    port_number := COALESCE(NULLIF(parts[3], '')::integer, default_port);
    IF port_number IS NULL OR port_number < 1 OR port_number > 65535 THEN RETURN NULL; END IF;
    RETURN host_name || ':' || port_number::text;
END;
$$;

-- Source rows retain the original identity and target attribution. A single
-- source can contribute an IP:port and a domain:port; grouping is by category
-- and label, across sources and organizations.
CREATE VIEW asset_inventory_source AS
 SELECT e.tenant_id,e.id AS source_asset_id,NULL::integer AS source_target_id,
        e.asset_type AS category,lower(rtrim(e.label,'.')) AS label,e.first_seen_at
 FROM asset_entity e WHERE e.asset_type IN ('subdomain','ip')
 UNION ALL
 SELECT e.tenant_id,e.id,NULL::integer,'service',tq_inventory_host_port(e.label),e.first_seen_at
 FROM asset_entity e WHERE e.asset_type IN ('service','endpoint') AND tq_inventory_host_port(e.label) IS NOT NULL
 UNION ALL
 SELECT e.tenant_id,e.id,h.target_id,'service',
        tq_inventory_host_port(CASE WHEN position(':' IN h.host)>0 THEN '[' || h.host || ']:' || h.port ELSE h.host || ':' || h.port END),h.created_at
 FROM host_port_mapping h JOIN asset_host_port_binding b ON b.host_port_id=h.id JOIN asset_entity e ON e.id=b.asset_id
 WHERE h.host<>'' AND h.port BETWEEN 1 AND 65535
 UNION ALL
 SELECT e.tenant_id,e.id,h.target_id,'service',
        tq_inventory_host_port(CASE WHEN position(':' IN host(h.ip))>0 THEN '[' || host(h.ip) || ']:' || h.port ELSE host(h.ip) || ':' || h.port END),h.created_at
 FROM host_port_mapping h JOIN asset_host_port_binding b ON b.host_port_id=h.id JOIN asset_entity e ON e.id=b.asset_id
 WHERE h.port BETWEEN 1 AND 65535;

CREATE VIEW asset_inventory_visible_source AS
 SELECT s.* FROM asset_inventory_source s JOIN asset_entity e ON e.id=s.source_asset_id AND e.tenant_id=s.tenant_id
 WHERE s.label IS NOT NULL AND (
   (s.source_target_id IS NOT NULL AND EXISTS (SELECT 1 FROM target t WHERE t.id=s.source_target_id AND t.deleted_at IS NULL)) OR
   (s.source_target_id IS NULL AND (e.is_manual OR
     EXISTS (SELECT 1 FROM asset_observation o LEFT JOIN target t ON t.id=o.target_id WHERE o.tenant_id=e.tenant_id AND o.asset_id=e.id AND (o.original_target_id IS NULL OR t.id IS NOT NULL AND t.deleted_at IS NULL)) OR
     EXISTS (SELECT 1 FROM asset_subdomain_binding b JOIN subdomain a ON a.id=b.subdomain_id JOIN target t ON t.id=a.target_id AND t.deleted_at IS NULL WHERE b.asset_id=e.id) OR
     EXISTS (SELECT 1 FROM asset_subdomain_variant_binding b JOIN subdomain a ON a.id=b.subdomain_id JOIN target t ON t.id=a.target_id AND t.deleted_at IS NULL WHERE b.asset_id=e.id) OR
     EXISTS (SELECT 1 FROM asset_website_binding b JOIN website a ON a.id=b.website_id JOIN target t ON t.id=a.target_id AND t.deleted_at IS NULL WHERE b.asset_id=e.id) OR
     EXISTS (SELECT 1 FROM asset_endpoint_binding b JOIN endpoint a ON a.id=b.endpoint_id JOIN target t ON t.id=a.target_id AND t.deleted_at IS NULL WHERE b.asset_id=e.id) OR
     EXISTS (SELECT 1 FROM asset_endpoint_variant_binding b JOIN endpoint a ON a.id=b.endpoint_id JOIN target t ON t.id=a.target_id AND t.deleted_at IS NULL WHERE b.asset_id=e.id) OR
     EXISTS (SELECT 1 FROM asset_host_port_binding b JOIN host_port_mapping a ON a.id=b.host_port_id JOIN target t ON t.id=a.target_id AND t.deleted_at IS NULL WHERE b.asset_id=e.id)
   ))
 );

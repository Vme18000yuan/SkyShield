ALTER TABLE ai_task ADD COLUMN coverage_enabled BOOLEAN NOT NULL DEFAULT TRUE;
CREATE TABLE ai_task_scope (
 id BIGSERIAL PRIMARY KEY,
 tenant_id UUID NOT NULL,
 owner_user_id INTEGER NOT NULL,
 task_id BIGINT NOT NULL REFERENCES ai_task(id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK(kind IN ('company','root_domain','subdomain','ip','cidr','icp','keyword')),
 scope_key TEXT NOT NULL,
 organization_id INTEGER REFERENCES organization(id) ON DELETE CASCADE,
 domain TEXT,
 net CIDR,
 value TEXT,
 source TEXT NOT NULL CHECK(source IN ('auto','agent','manual')),
 reason TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES tenant_membership(tenant_id,user_id) ON DELETE CASCADE,
 FOREIGN KEY(tenant_id,task_id) REFERENCES ai_task(tenant_id,id) ON DELETE CASCADE,
 UNIQUE(task_id,kind,scope_key),
 CHECK((kind='company' AND organization_id IS NOT NULL AND domain IS NULL AND net IS NULL AND value IS NULL) OR
       (kind IN ('root_domain','subdomain') AND organization_id IS NULL AND domain IS NOT NULL AND net IS NULL AND value IS NULL) OR
       (kind IN ('ip','cidr') AND organization_id IS NULL AND domain IS NULL AND net IS NOT NULL AND value IS NULL) OR
       (kind IN ('icp','keyword') AND organization_id IS NULL AND domain IS NULL AND net IS NULL AND value IS NOT NULL))
);
CREATE INDEX ai_task_scope_task ON ai_task_scope(task_id,id);
CREATE FUNCTION tianqiong_ai.guard_task_scope() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_task public.ai_task%ROWTYPE;v_task_id BIGINT;v_exp BIGINT;
BEGIN
 IF TG_OP='DELETE' AND pg_trigger_depth()>1 AND (NOT EXISTS(SELECT 1 FROM public.ai_task WHERE id=OLD.task_id) OR (OLD.organization_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM public.organization WHERE id=OLD.organization_id))) THEN RETURN OLD; END IF;
 IF TG_OP='DELETE' THEN v_task_id:=OLD.task_id; ELSE v_task_id:=NEW.task_id; END IF;
 SELECT exploration_id INTO v_exp FROM public.ai_task WHERE id=v_task_id;
 IF current_setting('tianqiong.execution_mode',TRUE)='human_scope' THEN
  v_task:=tianqiong_ai.assert_human_goal_scope(v_exp);
  IF TG_OP='INSERT' AND (NEW.source<>'manual' OR NEW.task_id<>v_task.id OR NEW.tenant_id<>v_task.tenant_id OR NEW.owner_user_id<>v_task.owner_user_id) THEN RAISE EXCEPTION 'AI task scope creation rejected'; END IF;
  IF TG_OP='DELETE' AND (OLD.task_id<>v_task.id OR OLD.tenant_id<>v_task.tenant_id OR OLD.owner_user_id<>v_task.owner_user_id) THEN RAISE EXCEPTION 'AI task scope deletion rejected'; END IF;
  IF TG_OP='UPDATE' THEN RAISE EXCEPTION 'AI task scope update rejected'; END IF;
 ELSE
  v_task:=tianqiong_ai.assert_task_write(v_exp,NULL);
  IF TG_OP<>'INSERT' OR NEW.source NOT IN ('agent','auto') OR NEW.task_id<>v_task.id OR NEW.tenant_id<>v_task.tenant_id OR NEW.owner_user_id<>v_task.owner_user_id THEN RAISE EXCEPTION 'AI task scope execution rejected'; END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
END;
$$;
CREATE TRIGGER tq_guard_task_scope BEFORE INSERT OR UPDATE OR DELETE ON ai_task_scope FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_task_scope();

-- A scope describes a coverage denominator, never an expansion of a task's
-- explicitly selected target. These helpers tolerate legacy asset labels.
CREATE FUNCTION tianqiong_ai.scope_host(raw text) RETURNS text LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
DECLARE v text; match text[];
BEGIN
 v:=lower(btrim(COALESCE(raw,'')));
 v:=regexp_replace(v,'^[a-z][a-z0-9+.-]*://','','i');
 v:=regexp_replace(v,'[/\?#].*$','');
 v:=regexp_replace(v,'^.*@','');
 IF left(v,1)='[' THEN
  match:=regexp_match(v,'^\[([^]]+)\](:[0-9]+)?$');
  IF match IS NULL THEN RETURN NULL; END IF;
  v:=match[1];
 ELSIF v ~ '^[^:]+:[0-9]+$' THEN
  v:=regexp_replace(v,':[0-9]+$','');
 END IF;
 RETURN NULLIF(rtrim(v,'.'),'');
END;
$$;
CREATE FUNCTION tianqiong_ai.scope_inet(raw text) RETURNS inet LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
BEGIN
 RETURN NULLIF(raw,'')::inet;
EXCEPTION WHEN invalid_text_representation OR invalid_parameter_value THEN RETURN NULL;
END;
$$;

-- Resolve the current and direct source tasks against the existing unified
-- asset store. A confirmed fact anchor is an approximate coverage signal.
CREATE FUNCTION tianqiong_ai.task_scope_assets(p_task bigint,p_user integer)
RETURNS TABLE(asset_id bigint,asset_type text,label text,tested boolean)
LANGUAGE sql STABLE AS $$
WITH current_task AS (
 SELECT t.id,t.tenant_id,t.owner_user_id,t.target_id,t.exploration_id
 FROM public.ai_task t JOIN public.tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id
 JOIN public.auth_user u ON u.id=m.user_id JOIN public.tenant tenant ON tenant.id=m.tenant_id
 WHERE t.id=p_task AND t.owner_user_id=p_user AND t.deleted_at IS NULL AND u.is_active AND tenant.status='active'
 AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM public.target WHERE id=t.target_id AND deleted_at IS NULL))
), context_tasks AS (
 SELECT id,tenant_id,exploration_id FROM current_task
 UNION ALL
 SELECT s.id,s.tenant_id,s.exploration_id FROM current_task c
 JOIN public.ai_task_relation r ON r.task_id=c.id AND r.tenant_id=c.tenant_id
 JOIN public.ai_task s ON s.id=r.source_task_id AND s.tenant_id=c.tenant_id AND s.owner_user_id=c.owner_user_id AND s.deleted_at IS NULL
 WHERE s.original_target_id IS NULL OR EXISTS(SELECT 1 FROM public.target WHERE id=s.target_id AND deleted_at IS NULL)
), scopes AS (
 SELECT s.* FROM public.ai_task_scope s JOIN context_tasks c ON c.id=s.task_id AND c.tenant_id=s.tenant_id
 LEFT JOIN public.organization org ON org.id=s.organization_id AND org.deleted_at IS NULL
 WHERE s.organization_id IS NULL OR org.id IS NOT NULL
), asset_targets AS (
 SELECT b.asset_id,a.target_id FROM public.asset_subdomain_binding b JOIN public.subdomain a ON a.id=b.subdomain_id
 UNION SELECT b.asset_id,a.target_id FROM public.asset_subdomain_variant_binding b JOIN public.subdomain a ON a.id=b.subdomain_id
 UNION SELECT b.asset_id,a.target_id FROM public.asset_website_binding b JOIN public.website a ON a.id=b.website_id
 UNION SELECT b.asset_id,a.target_id FROM public.asset_endpoint_binding b JOIN public.endpoint a ON a.id=b.endpoint_id
 UNION SELECT b.asset_id,a.target_id FROM public.asset_endpoint_variant_binding b JOIN public.endpoint a ON a.id=b.endpoint_id
 UNION SELECT b.asset_id,a.target_id FROM public.asset_host_port_binding b JOIN public.host_port_mapping a ON a.id=b.host_port_id
 UNION SELECT o.asset_id,o.target_id FROM public.asset_observation o WHERE o.target_id IS NOT NULL
 UNION SELECT x.application_id,w.target_id FROM public.asset_application_visible_website x JOIN public.website w ON w.id=x.website_id
 UNION SELECT root_asset_id,target_id FROM public.asset_root_domain_target
), visible_targets AS (
 SELECT a.asset_id,a.target_id FROM asset_targets a JOIN public.target t ON t.id=a.target_id AND t.deleted_at IS NULL
), visible AS (
 SELECT e.* FROM public.asset_entity e JOIN current_task c ON c.tenant_id=e.tenant_id
 WHERE (e.is_manual OR EXISTS(SELECT 1 FROM visible_targets v WHERE v.asset_id=e.id)
  OR EXISTS(SELECT 1 FROM public.asset_observation o LEFT JOIN public.target t ON t.id=o.target_id WHERE o.tenant_id=e.tenant_id AND o.asset_id=e.id AND (o.original_target_id IS NULL OR t.id IS NOT NULL AND t.deleted_at IS NULL))
  OR EXISTS(SELECT 1 FROM public.asset_root_domain_visible_source r WHERE r.tenant_id=e.tenant_id AND r.root_asset_id=e.id))
 AND (c.target_id IS NULL OR
  EXISTS(SELECT 1 FROM public.asset_website_binding b JOIN public.website w ON w.id=b.website_id WHERE b.asset_id=e.id AND w.target_id=c.target_id) OR
  EXISTS(SELECT 1 FROM public.asset_endpoint_binding b JOIN public.endpoint a ON a.id=b.endpoint_id WHERE b.asset_id=e.id AND a.target_id=c.target_id) OR
  EXISTS(SELECT 1 FROM public.asset_subdomain_binding b JOIN public.subdomain a ON a.id=b.subdomain_id WHERE b.asset_id=e.id AND a.target_id=c.target_id) OR
  EXISTS(SELECT 1 FROM public.asset_host_port_binding b JOIN public.host_port_mapping a ON a.id=b.host_port_id WHERE b.asset_id=e.id AND a.target_id=c.target_id) OR
  EXISTS(SELECT 1 FROM public.asset_observation o WHERE o.asset_id=e.id AND o.tenant_id=c.tenant_id AND o.target_id=c.target_id) OR
  EXISTS(SELECT 1 FROM public.asset_application_visible_website x JOIN public.website w ON w.id=x.website_id WHERE x.application_id=e.id AND x.tenant_id=c.tenant_id AND w.target_id=c.target_id) OR
  EXISTS(SELECT 1 FROM public.asset_root_domain_target r JOIN public.target t ON t.id=r.target_id AND t.deleted_at IS NULL WHERE r.tenant_id=c.tenant_id AND r.root_asset_id=e.id AND r.target_id=c.target_id AND t.type='domain' AND t.name=e.label AND e.asset_type='root_domain'))
), hosts AS (
 SELECT e.id AS asset_id,tianqiong_ai.scope_host(e.label) AS host FROM visible e WHERE e.asset_type IN ('root_domain','subdomain','service','endpoint','ip')
 UNION SELECT b.asset_id,tianqiong_ai.scope_host(h.host) FROM public.asset_host_port_binding b JOIN public.host_port_mapping h ON h.id=b.host_port_id JOIN public.target t ON t.id=h.target_id AND t.deleted_at IS NULL JOIN visible e ON e.id=b.asset_id JOIN current_task c ON TRUE WHERE c.target_id IS NULL OR h.target_id=c.target_id
 UNION SELECT o.asset_id,tianqiong_ai.scope_host(o.payload->>'domain') FROM public.asset_observation o JOIN visible e ON e.id=o.asset_id AND e.tenant_id=o.tenant_id JOIN current_task c ON TRUE LEFT JOIN public.target t ON t.id=o.target_id WHERE (c.target_id IS NULL OR o.target_id=c.target_id) AND (o.original_target_id IS NULL OR t.id IS NOT NULL AND t.deleted_at IS NULL)
 UNION SELECT o.asset_id,tianqiong_ai.scope_host(d.value) FROM public.asset_observation o JOIN visible e ON e.id=o.asset_id AND e.tenant_id=o.tenant_id JOIN current_task c ON TRUE LEFT JOIN public.target t ON t.id=o.target_id CROSS JOIN LATERAL jsonb_array_elements_text(CASE WHEN jsonb_typeof(o.payload->'boundDomains')='array' THEN o.payload->'boundDomains' ELSE '[]'::jsonb END) d(value) WHERE (c.target_id IS NULL OR o.target_id=c.target_id) AND (o.original_target_id IS NULL OR t.id IS NOT NULL AND t.deleted_at IS NULL)
 UNION SELECT x.application_id,tianqiong_ai.scope_host(w.url) FROM public.asset_application_visible_website x JOIN public.website w ON w.id=x.website_id JOIN visible e ON e.id=x.application_id AND e.tenant_id=x.tenant_id JOIN current_task c ON TRUE WHERE c.target_id IS NULL OR w.target_id=c.target_id
), ips AS (
 SELECT h.asset_id,tianqiong_ai.scope_inet(h.host) AS ip FROM hosts h
 UNION SELECT b.asset_id,h.ip FROM public.asset_host_port_binding b JOIN public.host_port_mapping h ON h.id=b.host_port_id JOIN public.target t ON t.id=h.target_id AND t.deleted_at IS NULL JOIN visible e ON e.id=b.asset_id JOIN current_task c ON TRUE WHERE c.target_id IS NULL OR h.target_id=c.target_id
 UNION SELECT o.asset_id,tianqiong_ai.scope_inet(v.ip) FROM public.asset_observation o JOIN visible e ON e.id=o.asset_id AND e.tenant_id=o.tenant_id JOIN current_task c ON TRUE LEFT JOIN public.target t ON t.id=o.target_id CROSS JOIN LATERAL (VALUES(o.payload->>'ip'),(o.payload->>'serviceIp'),(o.payload->'extra'->>'ip')) v(ip) WHERE (c.target_id IS NULL OR o.target_id=c.target_id) AND (o.original_target_id IS NULL OR t.id IS NOT NULL AND t.deleted_at IS NULL)
), candidates AS (
 SELECT e.id,e.asset_type,e.label FROM visible e
 WHERE EXISTS(SELECT 1 FROM public.ai_task_asset a JOIN context_tasks c ON c.id=a.task_id AND c.tenant_id=a.tenant_id WHERE a.asset_id=e.id)
 OR EXISTS(SELECT 1 FROM tianqiong_ai.exploration_anchors a JOIN tianqiong_ai.exploration_nodes n ON n.id=a.node_id JOIN context_tasks c ON c.exploration_id=n.exploration_id WHERE a.asset_id=e.id)
 OR EXISTS(SELECT 1 FROM scopes s WHERE
  (s.kind='company' AND (EXISTS(SELECT 1 FROM visible_targets v JOIN public.organization_target ot ON ot.target_id=v.target_id JOIN current_task c ON TRUE WHERE v.asset_id=e.id AND ot.organization_id=s.organization_id AND (c.target_id IS NULL OR v.target_id=c.target_id))
    OR EXISTS(SELECT 1 FROM public.asset_observation o JOIN current_task c ON TRUE LEFT JOIN public.target t ON t.id=o.target_id WHERE o.asset_id=e.id AND o.tenant_id=e.tenant_id AND o.organization_id=s.organization_id AND (c.target_id IS NULL OR o.target_id=c.target_id) AND (o.original_target_id IS NULL OR t.id IS NOT NULL AND t.deleted_at IS NULL))
    OR EXISTS(SELECT 1 FROM public.asset_root_domain_visible_source r JOIN visible_targets v ON v.asset_id=r.source_asset_id JOIN public.organization_target ot ON ot.target_id=v.target_id JOIN current_task c ON TRUE WHERE r.tenant_id=e.tenant_id AND r.root_asset_id=e.id AND ot.organization_id=s.organization_id AND (c.target_id IS NULL OR v.target_id=c.target_id))))
  OR (s.kind='root_domain' AND EXISTS(SELECT 1 FROM hosts h WHERE h.asset_id=e.id AND (h.host=s.domain OR right(h.host,length(s.domain)+1)='.'||s.domain)))
  OR (s.kind='subdomain' AND EXISTS(SELECT 1 FROM hosts h WHERE h.asset_id=e.id AND h.host=s.domain))
  OR (s.kind IN ('ip','cidr') AND EXISTS(SELECT 1 FROM ips i WHERE i.asset_id=e.id AND s.net >>= i.ip))
  OR (s.kind='icp' AND EXISTS(SELECT 1 FROM public.asset_observation o JOIN current_task c ON TRUE LEFT JOIN public.target t ON t.id=o.target_id WHERE o.asset_id=e.id AND o.tenant_id=e.tenant_id AND (c.target_id IS NULL OR o.target_id=c.target_id) AND (o.original_target_id IS NULL OR t.id IS NOT NULL AND t.deleted_at IS NULL) AND (lower(regexp_replace(COALESCE(o.payload->>'icp',''),'[[:space:]]+','','g'))=s.value OR lower(regexp_replace(COALESCE(o.payload->>'appIcp',''),'[[:space:]]+','','g'))=s.value)))
 )
)
SELECT e.id,e.asset_type::text,e.label,EXISTS(SELECT 1 FROM tianqiong_ai.exploration_anchors a JOIN tianqiong_ai.exploration_nodes n ON n.id=a.node_id JOIN context_tasks c ON c.exploration_id=n.exploration_id WHERE a.asset_id=e.id AND n.kind='fact' AND n.state='confirmed') FROM candidates e;
$$;

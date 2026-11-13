-- Recognition records are derived from existing Web evidence; they do not
-- change scan scope or remove the original website observations.
CREATE TABLE asset_application_website (
 tenant_id UUID NOT NULL,
 website_id INTEGER PRIMARY KEY REFERENCES website(id) ON DELETE CASCADE,
 application_id BIGINT NOT NULL,
 scope TEXT NOT NULL,
 manual_observation_id BIGINT REFERENCES asset_observation(id) ON DELETE CASCADE,
 evidence_kind TEXT NOT NULL CHECK(evidence_kind IN ('title','technology')),
 evidence_value TEXT NOT NULL,
 FOREIGN KEY (tenant_id,application_id) REFERENCES asset_entity(tenant_id,id) ON DELETE CASCADE
);
CREATE INDEX idx_asset_application_website_app ON asset_application_website(tenant_id,application_id,website_id);
CREATE INDEX idx_asset_application_label_lower ON asset_entity(tenant_id,lower(label),id) WHERE asset_type='app';
CREATE INDEX idx_asset_application_observation ON asset_observation(id) WHERE payload->>'type'='app';

-- Explicit matches remain visible only while their own target/organization
-- and bound-domain evidence still agrees with the original website.
CREATE VIEW asset_application_visible_website AS
 SELECT x.* FROM asset_application_website x JOIN website w ON w.id=x.website_id JOIN target t ON t.id=w.target_id AND t.deleted_at IS NULL
 LEFT JOIN asset_observation obs ON obs.id=x.manual_observation_id AND obs.tenant_id=x.tenant_id AND obs.asset_id=x.application_id
 LEFT JOIN target original_target ON original_target.id=obs.target_id
 WHERE x.manual_observation_id IS NULL OR (obs.id IS NOT NULL AND (obs.original_target_id IS NULL OR original_target.id IS NOT NULL AND original_target.deleted_at IS NULL)
  AND (obs.target_id=w.target_id OR EXISTS(SELECT 1 FROM organization_target ot JOIN organization org ON org.id=ot.organization_id AND org.deleted_at IS NULL WHERE ot.organization_id=obs.organization_id AND ot.target_id=w.target_id))
  AND EXISTS(SELECT 1 FROM jsonb_array_elements_text(CASE WHEN jsonb_typeof(obs.payload->'boundDomains')='array' THEN obs.payload->'boundDomains' ELSE '[]'::jsonb END) d(value)
   WHERE lower(rtrim(d.value,'.'))=x.scope OR right(lower(rtrim(d.value,'.')),length(x.scope)+1)='.'||x.scope));

CREATE TABLE asset_application_projection_queue (
 id BIGSERIAL PRIMARY KEY,
 website_id INTEGER NOT NULL,
 enqueued_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE asset_application_projection_progress (
 id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(id),
 last_website_id BIGINT NOT NULL DEFAULT 0 CHECK(last_website_id>=0),
 completed BOOLEAN NOT NULL DEFAULT FALSE,
 last_observation_id BIGINT NOT NULL DEFAULT 0 CHECK(last_observation_id>=0),
 observations_completed BOOLEAN NOT NULL DEFAULT FALSE,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO asset_application_projection_progress(id) VALUES(TRUE);
CREATE TABLE asset_application_manual_queue (
 id BIGSERIAL PRIMARY KEY,
 observation_id BIGINT NOT NULL UNIQUE,
 last_website_id BIGINT NOT NULL DEFAULT 0
);
CREATE TABLE asset_application_target_queue (
 id BIGSERIAL PRIMARY KEY,
 target_id INTEGER NOT NULL,
 last_website_id BIGINT NOT NULL DEFAULT 0
);

CREATE FUNCTION tq_enqueue_application_projection() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO asset_application_projection_queue(website_id) VALUES(NEW.id);
 RETURN NEW;
END;
$$;
CREATE TRIGGER tq_application_website_insert AFTER INSERT ON website
 FOR EACH ROW EXECUTE FUNCTION tq_enqueue_application_projection();
CREATE TRIGGER tq_application_website_update AFTER UPDATE OF url,host,title,status_code,tech ON website
 FOR EACH ROW WHEN (OLD.url IS DISTINCT FROM NEW.url OR OLD.host IS DISTINCT FROM NEW.host OR OLD.title IS DISTINCT FROM NEW.title OR OLD.status_code IS DISTINCT FROM NEW.status_code OR OLD.tech IS DISTINCT FROM NEW.tech)
 EXECUTE FUNCTION tq_enqueue_application_projection();

CREATE FUNCTION tq_enqueue_application_manual_projection() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.payload->>'type'='app' AND jsonb_typeof(NEW.payload->'boundDomains')='array' THEN
  IF jsonb_array_length(NEW.payload->'boundDomains')>0 THEN
   INSERT INTO asset_application_manual_queue(observation_id) VALUES(NEW.id) ON CONFLICT(observation_id) DO NOTHING;
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER tq_application_manual_insert AFTER INSERT ON asset_observation
 FOR EACH ROW EXECUTE FUNCTION tq_enqueue_application_manual_projection();

CREATE FUNCTION tq_enqueue_application_target_projection() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='organization_target' THEN
  INSERT INTO asset_application_target_queue(target_id) VALUES(CASE WHEN TG_OP='DELETE' THEN OLD.target_id ELSE NEW.target_id END);
 ELSE
  INSERT INTO asset_application_target_queue(target_id) VALUES(NEW.id);
 END IF;
 RETURN NULL;
END;
$$;
CREATE TRIGGER tq_application_org_target_change AFTER INSERT OR DELETE ON organization_target
 FOR EACH ROW EXECUTE FUNCTION tq_enqueue_application_target_projection();
CREATE TRIGGER tq_application_target_visibility AFTER UPDATE OF deleted_at ON target
 FOR EACH ROW WHEN (OLD.deleted_at IS DISTINCT FROM NEW.deleted_at)
 EXECUTE FUNCTION tq_enqueue_application_target_projection();

-- Application evidence grants access only to its original active target.
CREATE OR REPLACE FUNCTION tianqiong_ai.assert_task_write(p_exploration BIGINT,p_asset BIGINT DEFAULT NULL)
RETURNS public.ai_task LANGUAGE plpgsql AS $$
DECLARE v_task public.ai_task%ROWTYPE; v_id BIGINT; v_owner INTEGER; v_run UUID; v_mode TEXT;
BEGIN
 v_id:=NULLIF(current_setting('tianqiong.task_id',TRUE),'')::BIGINT;
 v_owner:=NULLIF(current_setting('tianqiong.owner_user_id',TRUE),'')::INTEGER;
 v_run:=NULLIF(current_setting('tianqiong.execution_id',TRUE),'')::UUID;
 v_mode:=current_setting('tianqiong.execution_mode',TRUE);
 SELECT task.* INTO v_task FROM public.ai_task task
 JOIN public.tenant_membership m ON m.tenant_id=task.tenant_id AND m.user_id=task.owner_user_id
 JOIN public.auth_user u ON u.id=m.user_id JOIN public.tenant tenant ON tenant.id=m.tenant_id
 WHERE task.id=v_id AND task.owner_user_id=v_owner AND task.exploration_id=p_exploration
 AND task.active_run=v_run AND task.deleted_at IS NULL AND u.is_active=TRUE
 AND tenant.status='active' AND m.role IN ('admin','analyst') FOR SHARE OF task,m,u;
 IF NOT FOUND THEN RAISE EXCEPTION 'AI task execution scope rejected'; END IF;
 IF v_mode='setup' THEN
   IF v_task.status<>'created' OR v_task.lease_epoch<>0 THEN RAISE EXCEPTION 'AI task setup scope rejected'; END IF;
 ELSIF v_task.status<>'running' OR v_task.lease_until IS NULL OR v_task.lease_until<=clock_timestamp() THEN
   RAISE EXCEPTION 'AI task execution lease rejected';
 END IF;
 IF v_task.original_target_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM public.target WHERE id=v_task.target_id AND deleted_at IS NULL) THEN
   RAISE EXCEPTION 'AI task target is inactive';
 END IF;
 IF p_asset IS NOT NULL THEN
   IF NOT EXISTS(SELECT 1 FROM public.asset_entity a WHERE a.id=p_asset AND a.tenant_id=v_task.tenant_id) THEN RAISE EXCEPTION 'AI task asset scope rejected'; END IF;
   IF v_task.target_id IS NOT NULL AND NOT (
     EXISTS(SELECT 1 FROM public.asset_website_binding b JOIN public.website a ON a.id=b.website_id WHERE b.asset_id=p_asset AND a.target_id=v_task.target_id) OR
     EXISTS(SELECT 1 FROM public.asset_endpoint_binding b JOIN public.endpoint a ON a.id=b.endpoint_id WHERE b.asset_id=p_asset AND a.target_id=v_task.target_id) OR
     EXISTS(SELECT 1 FROM public.asset_subdomain_binding b JOIN public.subdomain a ON a.id=b.subdomain_id WHERE b.asset_id=p_asset AND a.target_id=v_task.target_id) OR
     EXISTS(SELECT 1 FROM public.asset_host_port_binding b JOIN public.host_port_mapping a ON a.id=b.host_port_id WHERE b.asset_id=p_asset AND a.target_id=v_task.target_id) OR
     EXISTS(SELECT 1 FROM public.asset_observation o WHERE o.asset_id=p_asset AND o.tenant_id=v_task.tenant_id AND o.target_id=v_task.target_id) OR
     EXISTS(SELECT 1 FROM public.asset_application_visible_website app JOIN public.website w ON w.id=app.website_id WHERE app.application_id=p_asset AND app.tenant_id=v_task.tenant_id AND w.target_id=v_task.target_id) OR
     EXISTS(SELECT 1 FROM public.asset_root_domain_target r JOIN public.target t ON t.id=r.target_id JOIN public.asset_entity root ON root.tenant_id=r.tenant_id AND root.id=r.root_asset_id
       WHERE r.tenant_id=v_task.tenant_id AND r.root_asset_id=p_asset AND r.target_id=v_task.target_id AND t.deleted_at IS NULL AND t.type='domain' AND t.name=root.label AND root.asset_type='root_domain')
   ) THEN RAISE EXCEPTION 'AI task asset is outside the selected target'; END IF;
 END IF;
 RETURN v_task;
END;
$$;

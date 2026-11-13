BEGIN;

-- Derived roots are references to observed assets or configured targets;
-- they never create or enlarge a scan target.
CREATE TABLE public.asset_root_domain_source (
 tenant_id UUID NOT NULL,
 source_asset_id BIGINT PRIMARY KEY,
 root_asset_id BIGINT NOT NULL,
 FOREIGN KEY (tenant_id,source_asset_id) REFERENCES public.asset_entity(tenant_id,id) ON DELETE CASCADE,
 FOREIGN KEY (tenant_id,root_asset_id) REFERENCES public.asset_entity(tenant_id,id) ON DELETE CASCADE,
 CHECK (source_asset_id<>root_asset_id)
);
CREATE INDEX idx_asset_root_domain_source_root ON public.asset_root_domain_source(tenant_id,root_asset_id,source_asset_id);
CREATE TABLE public.asset_root_domain_target (
 tenant_id UUID NOT NULL,
 target_id INTEGER PRIMARY KEY REFERENCES public.target(id) ON DELETE CASCADE,
 root_asset_id BIGINT NOT NULL,
 FOREIGN KEY (tenant_id,root_asset_id) REFERENCES public.asset_entity(tenant_id,id) ON DELETE CASCADE
);
CREATE INDEX idx_asset_root_domain_target_root ON public.asset_root_domain_target(tenant_id,root_asset_id,target_id);

-- Events and the resumable historical cursor cover concurrent writes and
-- restarts without doing an unbounded table rewrite in the migration.
CREATE TABLE public.asset_root_projection_queue (
 id BIGSERIAL PRIMARY KEY,
 source_kind VARCHAR(12) NOT NULL CHECK (source_kind IN ('asset','target')),
 source_id BIGINT NOT NULL CHECK (source_id>0),
 enqueued_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE public.asset_root_projection_progress (
 source_kind VARCHAR(12) PRIMARY KEY CHECK (source_kind IN ('asset','target')),
 last_id BIGINT NOT NULL DEFAULT 0 CHECK(last_id>=0),
 completed BOOLEAN NOT NULL DEFAULT FALSE,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO public.asset_root_projection_progress(source_kind) VALUES ('target'),('asset');
CREATE INDEX idx_asset_entity_root_projection ON public.asset_entity(id) WHERE asset_type IN ('subdomain','service','endpoint');

CREATE FUNCTION public.tq_enqueue_root_domain_projection() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE kind TEXT;
BEGIN
 kind:=CASE WHEN TG_TABLE_NAME='target' THEN 'target' ELSE 'asset' END;
 INSERT INTO public.asset_root_projection_queue(source_kind,source_id)
 VALUES(kind,NEW.id);
 RETURN NEW;
END;
$$;
CREATE TRIGGER tq_target_root_domain_projection AFTER INSERT OR UPDATE OF name,type,deleted_at ON public.target
 FOR EACH ROW EXECUTE FUNCTION public.tq_enqueue_root_domain_projection();
CREATE TRIGGER tq_asset_root_domain_projection AFTER INSERT ON public.asset_entity
 FOR EACH ROW WHEN (NEW.asset_type IN ('subdomain','service','endpoint'))
 EXECUTE FUNCTION public.tq_enqueue_root_domain_projection();
CREATE TRIGGER tq_asset_root_domain_projection_update AFTER UPDATE OF asset_type,label ON public.asset_entity
 FOR EACH ROW WHEN (NEW.asset_type IN ('subdomain','service','endpoint') OR OLD.asset_type IN ('subdomain','service','endpoint'))
 EXECUTE FUNCTION public.tq_enqueue_root_domain_projection();

-- A derived root remains visible only while at least one underlying asset
-- remains visible; deleting a target must not leave a ghost parent.
CREATE VIEW public.asset_root_domain_visible_source AS
 SELECT r.tenant_id,r.root_asset_id,r.source_asset_id
 FROM public.asset_root_domain_source r
 JOIN public.asset_entity child ON child.tenant_id=r.tenant_id AND child.id=r.source_asset_id
 WHERE child.is_manual
 OR EXISTS(SELECT 1 FROM public.asset_observation o JOIN public.target t ON t.id=o.target_id AND t.deleted_at IS NULL WHERE o.tenant_id=r.tenant_id AND o.asset_id=child.id)
 OR EXISTS(SELECT 1 FROM public.asset_subdomain_binding b JOIN public.subdomain a ON a.id=b.subdomain_id JOIN public.target t ON t.id=a.target_id AND t.deleted_at IS NULL WHERE b.asset_id=child.id)
 OR EXISTS(SELECT 1 FROM public.asset_subdomain_variant_binding b JOIN public.subdomain a ON a.id=b.subdomain_id JOIN public.target t ON t.id=a.target_id AND t.deleted_at IS NULL WHERE b.asset_id=child.id)
 OR EXISTS(SELECT 1 FROM public.asset_website_binding b JOIN public.website a ON a.id=b.website_id JOIN public.target t ON t.id=a.target_id AND t.deleted_at IS NULL WHERE b.asset_id=child.id)
 OR EXISTS(SELECT 1 FROM public.asset_endpoint_binding b JOIN public.endpoint a ON a.id=b.endpoint_id JOIN public.target t ON t.id=a.target_id AND t.deleted_at IS NULL WHERE b.asset_id=child.id)
 OR EXISTS(SELECT 1 FROM public.asset_endpoint_variant_binding b JOIN public.endpoint a ON a.id=b.endpoint_id JOIN public.target t ON t.id=a.target_id AND t.deleted_at IS NULL WHERE b.asset_id=child.id);

-- A derived root may be attached to a task only when the selected target is
-- exactly that root. A narrower child-domain target never grants parent scope.
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
     EXISTS(SELECT 1 FROM public.asset_root_domain_target r JOIN public.target t ON t.id=r.target_id JOIN public.asset_entity root ON root.tenant_id=r.tenant_id AND root.id=r.root_asset_id
       WHERE r.tenant_id=v_task.tenant_id AND r.root_asset_id=p_asset AND r.target_id=v_task.target_id AND t.deleted_at IS NULL AND t.type='domain' AND t.name=root.label AND root.asset_type='root_domain')
   ) THEN RAISE EXCEPTION 'AI task asset is outside the selected target'; END IF;
 END IF;
 RETURN v_task;
END;
$$;

COMMIT;

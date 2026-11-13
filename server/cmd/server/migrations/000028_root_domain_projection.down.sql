-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses a verified backup restore or an approved forward fix.
BEGIN;
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
     EXISTS(SELECT 1 FROM public.asset_observation o WHERE o.asset_id=p_asset AND o.tenant_id=v_task.tenant_id AND o.target_id=v_task.target_id)
   ) THEN RAISE EXCEPTION 'AI task asset is outside the selected target'; END IF;
 END IF;
 RETURN v_task;
END;
$$;
DROP TRIGGER IF EXISTS tq_target_root_domain_projection ON public.target;
DROP TRIGGER IF EXISTS tq_asset_root_domain_projection ON public.asset_entity;
DROP TRIGGER IF EXISTS tq_asset_root_domain_projection_update ON public.asset_entity;
DROP FUNCTION IF EXISTS public.tq_enqueue_root_domain_projection();
DROP INDEX IF EXISTS public.idx_asset_entity_root_projection;
DROP VIEW IF EXISTS public.asset_root_domain_visible_source;
DROP TABLE IF EXISTS public.asset_root_projection_queue;
DROP TABLE IF EXISTS public.asset_root_projection_progress;
DROP TABLE IF EXISTS public.asset_root_domain_target;
DROP TABLE IF EXISTS public.asset_root_domain_source;
COMMIT;

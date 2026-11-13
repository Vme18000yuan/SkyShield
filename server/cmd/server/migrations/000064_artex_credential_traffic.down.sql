-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
CREATE OR REPLACE FUNCTION tianqiong_ai.guard_credential_job() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_mode text;v_task public.ai_task%ROWTYPE;v_exp bigint;v_enabled boolean;
BEGIN
 IF TG_OP='DELETE' THEN
  IF pg_trigger_depth()>1 AND (NOT EXISTS(SELECT 1 FROM tianqiong_ai.task_intelligence WHERE id=OLD.intelligence_id) OR NOT EXISTS(SELECT 1 FROM public.ai_task WHERE id=OLD.task_id)) THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'AI credential job deletion rejected';
 END IF;
 v_mode:=current_setting('tianqiong.execution_mode',TRUE);
 SELECT exploration_id INTO v_exp FROM public.ai_task WHERE id=NEW.task_id;
 IF NOT EXISTS(SELECT 1 FROM tianqiong_ai.task_intelligence i JOIN public.ai_task t ON t.id=i.task_id WHERE i.id=NEW.intelligence_id AND i.kind='credential' AND i.fingerprint=NEW.fingerprint AND t.id=NEW.task_id AND t.tenant_id=NEW.tenant_id AND t.owner_user_id=NEW.owner_user_id) THEN RAISE EXCEPTION 'AI credential job identity rejected'; END IF;
 IF NOT COALESCE(TG_OP='UPDATE' AND v_mode='credential_worker' AND NEW.status='queued',FALSE) AND NOT EXISTS(SELECT 1 FROM public.ai_task t JOIN public.tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id JOIN public.tenant tenant ON tenant.id=t.tenant_id JOIN public.auth_user u ON u.id=t.owner_user_id WHERE t.id=NEW.task_id AND t.deleted_at IS NULL AND tenant.status='active' AND u.is_active AND m.role IN ('admin','analyst') AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM public.target WHERE id=t.target_id AND deleted_at IS NULL)) AND NOT EXISTS(SELECT 1 FROM public.ai_task_archive a WHERE a.task_id=t.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting'))) THEN RAISE EXCEPTION 'AI credential job scope rejected'; END IF;
 IF TG_OP='INSERT' THEN
  IF v_mode IS DISTINCT FROM 'credential_queue' OR NEW.status<>'queued' OR NEW.lease_id IS NOT NULL OR NEW.sealed_analysis IS NOT NULL OR NEW.metadata<>'{}'::jsonb OR NEW.force_extract THEN RAISE EXCEPTION 'AI credential job creation rejected'; END IF;
 ELSIF v_mode='credential_queue' THEN
  SELECT COALESCE(options->>'task_intelligence_enabled','false')='true' AND COALESCE(options->>'credential_agent_enabled','false')='true' INTO v_enabled FROM public.ai_runtime_setting WHERE tenant_id=NEW.tenant_id;
  IF NOT COALESCE(v_enabled,FALSE) OR NOT EXISTS(SELECT 1 FROM public.ai_custom_agent a WHERE a.tenant_id=NEW.tenant_id AND a.agent_key='credential_processor' AND a.enabled) OR NOT (OLD.status='queued' OR (OLD.status='processing' AND OLD.lease_until<=clock_timestamp())) OR NEW.status<>'processing' OR NEW.lease_id IS NULL OR NEW.lease_until<=clock_timestamp() OR (to_jsonb(NEW)-'status'-'lease_id'-'lease_until'-'updated_at') IS DISTINCT FROM (to_jsonb(OLD)-'status'-'lease_id'-'lease_until'-'updated_at') THEN RAISE EXCEPTION 'AI credential job claim rejected'; END IF;
 ELSIF v_mode='credential_worker' THEN
  IF OLD.status<>'processing' OR OLD.lease_id IS DISTINCT FROM NULLIF(current_setting('tianqiong.credential_lease',TRUE),'')::uuid OR OLD.lease_until<=clock_timestamp() OR NEW.status NOT IN ('queued','ready','error') OR NEW.lease_id IS NOT NULL OR NEW.lease_until IS NOT NULL OR (to_jsonb(NEW)-'status'-'lease_id'-'lease_until'-'force_extract'-'sealed_analysis'-'metadata'-'updated_at') IS DISTINCT FROM (to_jsonb(OLD)-'status'-'lease_id'-'lease_until'-'force_extract'-'sealed_analysis'-'metadata'-'updated_at') THEN RAISE EXCEPTION 'AI credential job completion rejected'; END IF;
  IF NEW.status IN ('ready','error') AND NOT EXISTS(SELECT 1 FROM public.ai_runtime_setting s JOIN public.ai_custom_agent a ON a.tenant_id=s.tenant_id AND a.agent_key='credential_processor' AND a.enabled WHERE s.tenant_id=NEW.tenant_id AND COALESCE(s.options->>'task_intelligence_enabled','false')='true' AND COALESCE(s.options->>'credential_agent_enabled','false')='true') THEN RAISE EXCEPTION 'AI credential processor disabled'; END IF;
  IF NEW.status='queued' AND (NEW.sealed_analysis IS DISTINCT FROM OLD.sealed_analysis OR NEW.metadata IS DISTINCT FROM OLD.metadata OR NEW.force_extract IS DISTINCT FROM OLD.force_extract) THEN RAISE EXCEPTION 'AI credential requeue cannot publish results'; END IF;
  IF NEW.status='ready' AND NEW.sealed_analysis IS NULL THEN RAISE EXCEPTION 'AI credential result missing'; END IF;
 ELSIF v_mode='human_credential' THEN
  v_task:=tianqiong_ai.assert_human_goal_scope(v_exp);
  IF NEW.task_id<>v_task.id OR (OLD.status='processing' AND OLD.lease_until>clock_timestamp()) OR NEW.status<>'queued' OR NOT NEW.force_extract OR NEW.lease_id IS NOT NULL OR NEW.lease_until IS NOT NULL OR NEW.metadata IS DISTINCT FROM ((OLD.metadata-'error'-'state')||'{"state":"queued"}'::jsonb) OR (to_jsonb(NEW)-'status'-'lease_id'-'lease_until'-'force_extract'-'metadata'-'updated_at') IS DISTINCT FROM (to_jsonb(OLD)-'status'-'lease_id'-'lease_until'-'force_extract'-'metadata'-'updated_at') THEN RAISE EXCEPTION 'AI credential retry rejected'; END IF;
 ELSE RAISE EXCEPTION 'AI credential job write rejected'; END IF;
 RETURN NEW;
END;
$$;
DROP TRIGGER tq_credential_binding_epoch ON public.ai_runtime_setting;
DROP FUNCTION public.tq_credential_binding_epoch();
ALTER TABLE public.ai_runtime_setting DROP COLUMN credential_binding_epoch;

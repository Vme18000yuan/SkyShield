-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
CREATE OR REPLACE FUNCTION tianqiong_ai.guard_finding_write() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_task public.ai_task%ROWTYPE;v_exp BIGINT;v_node BIGINT;v_id BIGINT;v_owner INTEGER;v_run UUID;
BEGIN
 IF TG_OP='UPDATE' AND current_setting('tianqiong.execution_mode',TRUE) IN ('human_evidence','reporter_evidence') THEN
  IF (to_jsonb(NEW)-'evidence_version') IS DISTINCT FROM (to_jsonb(OLD)-'evidence_version') OR NEW.evidence_version<>OLD.evidence_version+1 THEN RAISE EXCEPTION 'AI evidence scope can only advance the evidence version'; END IF;
  SELECT exploration_id INTO v_exp FROM public.ai_task WHERE id=OLD.task_id;
  v_task:=tianqiong_ai.assert_finding_evidence_scope(v_exp,OLD.id);
  IF v_task.id IS DISTINCT FROM OLD.task_id THEN RAISE EXCEPTION 'AI evidence task mismatch'; END IF;
  RETURN NEW;
 END IF;
 IF current_setting('tianqiong.execution_mode',TRUE)='human_intent_hard' THEN
  SELECT exploration_id INTO v_exp FROM public.ai_task WHERE id=NULLIF(current_setting('tianqiong.task_id',TRUE),'')::bigint;
  v_task:=tianqiong_ai.assert_human_goal_scope(v_exp);
  IF TG_OP<>'DELETE' OR OLD.task_id IS DISTINCT FROM v_task.id OR OLD.node_id IS NULL OR NOT (OLD.node_id=ANY(COALESCE(NULLIF(current_setting('tianqiong.delete_ids',TRUE),'')::bigint[],'{}'::bigint[]))) OR EXISTS(SELECT 1 FROM public.ai_finding_retest WHERE finding_id=OLD.id AND status IN ('pending','running')) THEN RAISE EXCEPTION 'AI hard intent finding deletion rejected'; END IF;
  RETURN OLD;
 END IF;
 IF TG_OP='UPDATE' AND current_setting('tianqiong.execution_mode',TRUE)='conversation_retest' THEN
   IF (to_jsonb(NEW)-'status') IS DISTINCT FROM (to_jsonb(OLD)-'status') OR NEW.status<>'fixed' OR pg_trigger_depth()<2 THEN
     RAISE EXCEPTION 'AI retest scope can only mark a finding fixed';
   END IF;
   v_id:=NULLIF(current_setting('tianqiong.task_id',TRUE),'')::BIGINT;
   v_owner:=NULLIF(current_setting('tianqiong.owner_user_id',TRUE),'')::INTEGER;
   v_run:=NULLIF(current_setting('tianqiong.execution_id',TRUE),'')::UUID;
   SELECT task.* INTO v_task FROM public.ai_task task
   JOIN public.tenant_membership m ON m.tenant_id=task.tenant_id AND m.user_id=task.owner_user_id
   JOIN public.auth_user u ON u.id=m.user_id JOIN public.tenant tenant ON tenant.id=m.tenant_id
   JOIN public.ai_finding_retest retest ON retest.finding_id=OLD.id AND retest.tenant_id=task.tenant_id AND retest.owner_user_id=task.owner_user_id
   JOIN public.ai_run run ON run.id=retest.run_id AND run.owner_user_id=task.owner_user_id AND run.tenant_id=task.tenant_id
   WHERE task.id=v_id AND task.id=OLD.task_id AND task.owner_user_id=v_owner AND task.deleted_at IS NULL
   AND m.role IN ('admin','analyst') AND u.is_active=TRUE AND tenant.status='active'
   AND (task.original_target_id IS NULL OR EXISTS(SELECT 1 FROM public.target WHERE id=task.target_id AND deleted_at IS NULL))
   AND run.id=v_run AND run.status='completed' AND retest.status='completed' AND retest.verdict='fixed' FOR SHARE OF task,m,u;
   IF NOT FOUND THEN RAISE EXCEPTION 'AI retest execution scope rejected'; END IF;
   RETURN NEW;
 END IF;
 IF TG_OP='UPDATE' AND current_setting('tianqiong.execution_mode',TRUE)='conversation_report' THEN
   IF (to_jsonb(NEW)-'report'-'report_evidence_version') IS DISTINCT FROM (to_jsonb(OLD)-'report'-'report_evidence_version') THEN
     RAISE EXCEPTION 'AI report scope can only update report fields';
   END IF;
   v_id:=NULLIF(current_setting('tianqiong.task_id',TRUE),'')::BIGINT;
   v_owner:=NULLIF(current_setting('tianqiong.owner_user_id',TRUE),'')::INTEGER;
   v_run:=NULLIF(current_setting('tianqiong.execution_id',TRUE),'')::UUID;
   SELECT task.* INTO v_task FROM public.ai_task task
   JOIN public.tenant_membership m ON m.tenant_id=task.tenant_id AND m.user_id=task.owner_user_id
   JOIN public.auth_user u ON u.id=m.user_id JOIN public.tenant tenant ON tenant.id=m.tenant_id
   JOIN tianqiong_ai.exploration_nodes n ON n.id=OLD.node_id AND n.exploration_id=task.exploration_id
   WHERE task.id=v_id AND task.id=OLD.task_id AND task.owner_user_id=v_owner AND task.deleted_at IS NULL
   AND u.is_active=TRUE AND tenant.status='active' AND m.role IN ('admin','analyst')
   AND (task.original_target_id IS NULL OR EXISTS(SELECT 1 FROM public.target WHERE id=task.target_id AND deleted_at IS NULL))
   AND (EXISTS(SELECT 1 FROM public.ai_run r WHERE r.tenant_id=task.tenant_id AND r.owner_user_id=v_owner AND r.id=v_run AND r.status='running' AND r.lease_until>clock_timestamp())
        OR EXISTS(SELECT 1 FROM public.ai_task source WHERE source.tenant_id=task.tenant_id AND source.owner_user_id=v_owner AND source.active_run=v_run AND source.status='running' AND source.lease_until>clock_timestamp() AND source.deleted_at IS NULL)
        OR EXISTS(SELECT 1 FROM public.ai_task_main_chat_run c WHERE c.task_id=task.id AND c.tenant_id=task.tenant_id AND c.owner_user_id=v_owner AND c.run_id=v_run AND c.status='running' AND c.lease_until>clock_timestamp() AND tianqiong_ai.task_run_live(task.id,v_owner,v_run)))
   FOR SHARE OF task,m,u;
   IF NOT FOUND THEN RAISE EXCEPTION 'AI report execution scope rejected'; END IF;
   RETURN NEW;
 END IF;
 IF TG_OP='DELETE' THEN v_node:=OLD.node_id; ELSE v_node:=NEW.node_id; END IF;
 SELECT exploration_id INTO v_exp FROM tianqiong_ai.exploration_nodes WHERE id=v_node;
 v_task:=tianqiong_ai.assert_task_write(v_exp,NULL);
 IF TG_OP<>'DELETE' AND NEW.task_id<>v_task.id THEN RAISE EXCEPTION 'AI finding crosses task boundary'; END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;

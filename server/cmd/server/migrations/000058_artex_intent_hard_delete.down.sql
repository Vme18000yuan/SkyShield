-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
DELETE FROM ai_task_intent_control WHERE mode='hard';
DROP INDEX ai_task_intent_delete_latest;
ALTER TABLE ai_task_intent_control DROP COLUMN mode;
CREATE OR REPLACE FUNCTION tianqiong_ai.guard_intent_control() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_task public.ai_task%ROWTYPE;v_exp BIGINT;v_run UUID;
BEGIN
 SELECT exploration_id INTO v_exp FROM public.ai_task WHERE id=NEW.task_id;
 IF TG_OP='INSERT' THEN
  IF current_setting('tianqiong.execution_mode',TRUE) IS DISTINCT FROM 'human_intent' THEN RAISE EXCEPTION 'AI intent control creation rejected'; END IF;
  v_task:=tianqiong_ai.assert_human_goal_scope(v_exp);
  IF NEW.tenant_id<>v_task.tenant_id OR NEW.owner_user_id<>v_task.owner_user_id OR NEW.run_id IS DISTINCT FROM v_task.active_run OR v_task.status<>'running' OR v_task.lease_until IS NULL OR v_task.lease_until<=clock_timestamp() OR NEW.status<>'pending' OR NEW.finished_at IS NOT NULL OR NOT EXISTS(SELECT 1 FROM tianqiong_ai.exploration_nodes WHERE exploration_id=v_exp AND id=NEW.intent_id AND kind='intent' AND state='running') OR (NEW.action='cancel' AND btrim(NEW.reason)='') OR (NEW.action='pause' AND NEW.reason<>'') THEN RAISE EXCEPTION 'AI intent control scope rejected'; END IF;
 ELSIF current_setting('tianqiong.execution_mode',TRUE)='human_intent' THEN
  v_task:=tianqiong_ai.assert_human_goal_scope(v_exp);
  IF OLD.action<>'cancel' OR OLD.status NOT IN ('pending','paused') OR NEW.status<>'deleted' OR NEW.finished_at IS NULL OR NOT EXISTS(SELECT 1 FROM tianqiong_ai.exploration_nodes WHERE exploration_id=v_exp AND id=OLD.intent_id AND kind='intent' AND state='deleted') OR (to_jsonb(NEW)-'status'-'finished_at') IS DISTINCT FROM (to_jsonb(OLD)-'status'-'finished_at') THEN RAISE EXCEPTION 'AI intent deletion acknowledgement rejected'; END IF;
 ELSE
  v_task:=tianqiong_ai.assert_task_write(v_exp,NULL);v_run:=NULLIF(current_setting('tianqiong.execution_id',TRUE),'')::UUID;
  IF OLD.status<>'pending' OR NEW.status NOT IN ('paused','conflict') OR NEW.finished_at IS NULL OR NEW.run_id IS DISTINCT FROM v_run OR (to_jsonb(NEW)-'status'-'finished_at') IS DISTINCT FROM (to_jsonb(OLD)-'status'-'finished_at') THEN RAISE EXCEPTION 'AI intent control acknowledgement rejected'; END IF;
 END IF;
 RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION tianqiong_ai.guard_goal_node_write() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_exp BIGINT;v_task public.ai_task%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN v_exp:=OLD.exploration_id; ELSE v_exp:=NEW.exploration_id; END IF;
 IF current_setting('tianqiong.execution_mode',TRUE)='human_goal' THEN
  v_task:=tianqiong_ai.assert_human_goal_scope(v_exp);
  IF TG_OP='INSERT' AND (NEW.kind<>'goal' OR NEW.state<>'open' OR NEW.origin IS DISTINCT FROM 'human' OR NEW.priority<>0 OR NEW.owner IS NOT NULL) THEN RAISE EXCEPTION 'AI human goal creation rejected'; END IF;
  IF TG_OP='UPDATE' AND (OLD.kind<>'goal' OR (to_jsonb(NEW)-'payload'-'updated_at') IS DISTINCT FROM (to_jsonb(OLD)-'payload'-'updated_at')) THEN RAISE EXCEPTION 'AI human goal update rejected'; END IF;
  IF TG_OP='DELETE' AND (OLD.kind<>'goal' OR OLD.id IS DISTINCT FROM NULLIF(current_setting('tianqiong.goal_id',TRUE),'')::BIGINT) THEN RAISE EXCEPTION 'AI human goal deletion rejected'; END IF;
 ELSIF current_setting('tianqiong.execution_mode',TRUE)='human_intent' THEN
  v_task:=tianqiong_ai.assert_human_goal_scope(v_exp);
  IF TG_OP<>'UPDATE' OR OLD.kind<>'intent' OR NEW.content_version<>OLD.content_version+1 OR
    (NEW.state='open' AND (OLD.state NOT IN ('blocked','exhausted','stopped','paused') OR (NULLIF(current_setting('tianqiong.intent_id',TRUE),'') IS NULL AND OLD.state<>'blocked') OR (NULLIF(current_setting('tianqiong.intent_id',TRUE),'') IS NOT NULL AND OLD.id IS DISTINCT FROM NULLIF(current_setting('tianqiong.intent_id',TRUE),'')::BIGINT) OR NEW.owner IS NOT NULL OR NEW.blocked_reason IS NOT NULL OR NEW.completed_at IS NOT NULL OR (to_jsonb(NEW)-'state'-'owner'-'blocked_reason'-'completed_at'-'content_version'-'updated_at') IS DISTINCT FROM (to_jsonb(OLD)-'state'-'owner'-'blocked_reason'-'completed_at'-'content_version'-'updated_at'))) OR
    (NEW.state='deleted' AND (OLD.state NOT IN ('open','paused','running') OR OLD.id IS DISTINCT FROM NULLIF(current_setting('tianqiong.intent_id',TRUE),'')::BIGINT OR (OLD.state='running' AND v_task.status='running' AND v_task.lease_until>clock_timestamp()) OR btrim(COALESCE(NEW.delete_reason,''))='' OR NEW.blocked_reason IS NOT NULL OR NEW.completed_at IS NULL OR (to_jsonb(NEW)-'state'-'delete_reason'-'blocked_reason'-'completed_at'-'content_version'-'updated_at') IS DISTINCT FROM (to_jsonb(OLD)-'state'-'delete_reason'-'blocked_reason'-'completed_at'-'content_version'-'updated_at'))) OR
    NEW.state NOT IN ('open','deleted')
  THEN RAISE EXCEPTION 'AI human intent update rejected'; END IF;
 ELSE v_task:=tianqiong_ai.assert_task_write(v_exp,NULL); END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION tianqiong_ai.guard_goal_edge_write() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_exp BIGINT;v_goal BIGINT;v_task public.ai_task%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN v_exp:=OLD.exploration_id; ELSE v_exp:=NEW.exploration_id; END IF;
 IF current_setting('tianqiong.execution_mode',TRUE)='human_goal' THEN
  v_task:=tianqiong_ai.assert_human_goal_scope(v_exp);v_goal:=NULLIF(current_setting('tianqiong.goal_id',TRUE),'')::BIGINT;
  IF TG_OP='INSERT' THEN
   IF NEW.rel<>'spawns' OR NEW.dst_id IS DISTINCT FROM v_goal OR NOT EXISTS(SELECT 1 FROM tianqiong_ai.exploration_nodes WHERE id=NEW.src_id AND exploration_id=v_exp AND kind='fact' AND state='origin') OR NOT EXISTS(SELECT 1 FROM tianqiong_ai.exploration_nodes WHERE id=NEW.dst_id AND exploration_id=v_exp AND kind='goal') THEN RAISE EXCEPTION 'AI human goal edge rejected'; END IF;
  ELSIF TG_OP='DELETE' THEN
   IF pg_trigger_depth()<2 OR (OLD.src_id IS DISTINCT FROM v_goal AND OLD.dst_id IS DISTINCT FROM v_goal) THEN RAISE EXCEPTION 'AI human goal edge deletion rejected'; END IF;
  ELSE RAISE EXCEPTION 'AI human goal edge update rejected'; END IF;
 ELSE
  v_task:=tianqiong_ai.assert_task_write(v_exp,NULL);
  IF TG_OP<>'DELETE' THEN
   IF NOT EXISTS(SELECT 1 FROM tianqiong_ai.exploration_nodes WHERE id=NEW.dst_id AND exploration_id=v_exp) THEN RAISE EXCEPTION 'AI task graph edge target crosses exploration boundary'; END IF;
   IF NOT EXISTS(SELECT 1 FROM tianqiong_ai.exploration_nodes WHERE id=NEW.src_id AND exploration_id=v_exp) AND NOT (((NEW.rel='derived_from' AND EXISTS(SELECT 1 FROM tianqiong_ai.exploration_nodes WHERE id=NEW.dst_id AND exploration_id=v_exp AND kind='intent')) OR (NEW.rel='proves' AND EXISTS(SELECT 1 FROM tianqiong_ai.exploration_nodes WHERE id=NEW.dst_id AND exploration_id=v_exp AND kind='goal'))) AND EXISTS(
    SELECT 1 FROM public.ai_task_relation r JOIN public.ai_task source ON source.id=r.source_task_id AND source.tenant_id=v_task.tenant_id AND source.owner_user_id=v_task.owner_user_id AND source.deleted_at IS NULL AND (source.original_target_id IS NULL OR EXISTS(SELECT 1 FROM public.target WHERE id=source.target_id AND deleted_at IS NULL))
    JOIN tianqiong_ai.exploration_nodes parent ON parent.exploration_id=source.exploration_id AND parent.id=NEW.src_id AND parent.kind IN ('fact','finding')
    WHERE r.task_id=v_task.id AND r.tenant_id=v_task.tenant_id
   )) THEN RAISE EXCEPTION 'AI task graph edge source crosses exploration boundary'; END IF;
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION tianqiong_ai.guard_goal_dependent_write() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_exp BIGINT;v_goal BIGINT;v_task public.ai_task%ROWTYPE;v_node BIGINT;v_asset BIGINT;
BEGIN
 IF current_setting('tianqiong.execution_mode',TRUE)='human_goal' THEN
  SELECT exploration_id INTO v_exp FROM public.ai_task WHERE id=NULLIF(current_setting('tianqiong.task_id',TRUE),'')::BIGINT;
  v_task:=tianqiong_ai.assert_human_goal_scope(v_exp);v_goal:=NULLIF(current_setting('tianqiong.goal_id',TRUE),'')::BIGINT;
  IF pg_trigger_depth()<2 THEN RAISE EXCEPTION 'AI goal dependent operation rejected'; END IF;
  IF TG_TABLE_NAME='exploration_anchors' THEN
   IF TG_OP<>'DELETE' OR OLD.node_id IS DISTINCT FROM v_goal THEN RAISE EXCEPTION 'AI goal anchor operation rejected'; END IF;
  ELSIF TG_TABLE_NAME IN ('activity','task_intelligence') THEN
   IF TG_OP<>'UPDATE' OR OLD.node_id IS DISTINCT FROM v_goal OR NEW.node_id IS NOT NULL OR (to_jsonb(NEW)-'node_id') IS DISTINCT FROM (to_jsonb(OLD)-'node_id') THEN RAISE EXCEPTION 'AI goal reference update rejected'; END IF;
  ELSE RAISE EXCEPTION 'AI goal dependent table rejected'; END IF;
 ELSE
  IF TG_TABLE_NAME='exploration_anchors' THEN
   IF TG_OP='DELETE' THEN v_node:=OLD.node_id;v_asset:=OLD.asset_id; ELSE v_node:=NEW.node_id;v_asset:=NEW.asset_id; END IF;
   SELECT exploration_id INTO v_exp FROM tianqiong_ai.exploration_nodes WHERE id=v_node;
  ELSIF TG_TABLE_NAME='task_intelligence' THEN
   IF TG_OP='DELETE' THEN SELECT exploration_id INTO v_exp FROM public.ai_task WHERE id=OLD.task_id; ELSE SELECT exploration_id INTO v_exp FROM public.ai_task WHERE id=NEW.task_id; END IF;
  ELSE
   IF TG_OP='DELETE' THEN v_exp:=OLD.exploration_id; ELSE v_exp:=NEW.exploration_id; END IF;
  END IF;
  v_task:=tianqiong_ai.assert_task_write(v_exp,v_asset);
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION tianqiong_ai.guard_finding_write() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_task public.ai_task%ROWTYPE;v_exp BIGINT;v_node BIGINT;v_id BIGINT;v_owner INTEGER;v_run UUID;
BEGIN
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
        OR EXISTS(SELECT 1 FROM public.ai_task source WHERE source.tenant_id=task.tenant_id AND source.owner_user_id=v_owner AND source.active_run=v_run AND source.status='running' AND source.lease_until>clock_timestamp() AND source.deleted_at IS NULL))
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

-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
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
 ELSIF current_setting('tianqiong.execution_mode',TRUE)='human_intent_hard' THEN
  SELECT exploration_id INTO v_exp FROM public.ai_task WHERE id=NULLIF(current_setting('tianqiong.task_id',TRUE),'')::bigint;
  v_task:=tianqiong_ai.assert_human_goal_scope(v_exp);
  IF TG_TABLE_NAME='exploration_anchors' THEN
   IF TG_OP<>'DELETE' OR pg_trigger_depth()<2 OR NOT (OLD.node_id=ANY(COALESCE(NULLIF(current_setting('tianqiong.delete_ids',TRUE),'')::bigint[],'{}'::bigint[]))) THEN RAISE EXCEPTION 'AI hard intent anchor deletion rejected'; END IF;
  ELSIF TG_TABLE_NAME='activity' THEN
   IF TG_OP='DELETE' THEN
    IF OLD.exploration_id<>v_exp OR OLD.node_id IS NULL OR NOT (OLD.node_id=ANY(COALESCE(NULLIF(current_setting('tianqiong.delete_ids',TRUE),'')::bigint[],'{}'::bigint[]))) THEN RAISE EXCEPTION 'AI hard intent activity deletion rejected'; END IF;
   ELSIF TG_OP='UPDATE' THEN
    IF pg_trigger_depth()<2 OR OLD.exploration_id<>v_exp OR OLD.node_id IS NULL OR NOT (OLD.node_id=ANY(COALESCE(NULLIF(current_setting('tianqiong.delete_ids',TRUE),'')::bigint[],'{}'::bigint[]))) OR NEW.node_id IS NOT NULL OR (to_jsonb(NEW)-'node_id') IS DISTINCT FROM (to_jsonb(OLD)-'node_id') THEN RAISE EXCEPTION 'AI hard intent activity reference rejected'; END IF;
   ELSIF TG_OP='INSERT' THEN
    IF NEW.exploration_id<>v_exp OR NEW.node_id IS NOT NULL OR NEW.worker IS DISTINCT FROM 'token-ledger' OR NEW.kind IS DISTINCT FROM 'result' OR NEW.metadata->>'token_rollup' IS DISTINCT FROM 'true' THEN RAISE EXCEPTION 'AI hard intent ledger rejected'; END IF;
   ELSE RAISE EXCEPTION 'AI hard intent activity rejected'; END IF;
  ELSIF TG_TABLE_NAME='task_intelligence' THEN
   IF TG_OP<>'UPDATE' OR pg_trigger_depth()<2 OR OLD.task_id<>v_task.id OR OLD.node_id IS NULL OR NOT (OLD.node_id=ANY(COALESCE(NULLIF(current_setting('tianqiong.delete_ids',TRUE),'')::bigint[],'{}'::bigint[]))) OR NEW.node_id IS NOT NULL OR (to_jsonb(NEW)-'node_id') IS DISTINCT FROM (to_jsonb(OLD)-'node_id') THEN RAISE EXCEPTION 'AI hard intent intelligence reference rejected'; END IF;
  ELSE RAISE EXCEPTION 'AI hard intent dependent table rejected'; END IF;
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
ALTER TABLE tianqiong_ai.task_intelligence DROP CONSTRAINT tq_intelligence_sealed_check;
ALTER TABLE tianqiong_ai.task_intelligence DROP COLUMN sealed_payload;

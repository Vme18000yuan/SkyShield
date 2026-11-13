-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
DELETE FROM ai_task_human_event WHERE kind='intent_reopened';
ALTER TABLE ai_task_human_event DROP CONSTRAINT ai_task_human_event_kind_check;
ALTER TABLE ai_task_human_event ADD CONSTRAINT ai_task_human_event_kind_check CHECK(kind IN ('goal','goal_edited','goal_deleted'));

CREATE OR REPLACE FUNCTION tianqiong_ai.guard_goal_node_write() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_exp BIGINT;v_task public.ai_task%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN v_exp:=OLD.exploration_id; ELSE v_exp:=NEW.exploration_id; END IF;
 IF current_setting('tianqiong.execution_mode',TRUE)='human_goal' THEN
  v_task:=tianqiong_ai.assert_human_goal_scope(v_exp);
  IF TG_OP='INSERT' AND (NEW.kind<>'goal' OR NEW.state<>'open' OR NEW.origin IS DISTINCT FROM 'human' OR NEW.priority<>0 OR NEW.owner IS NOT NULL) THEN RAISE EXCEPTION 'AI human goal creation rejected'; END IF;
  IF TG_OP='UPDATE' AND (OLD.kind<>'goal' OR (to_jsonb(NEW)-'payload'-'updated_at') IS DISTINCT FROM (to_jsonb(OLD)-'payload'-'updated_at')) THEN RAISE EXCEPTION 'AI human goal update rejected'; END IF;
  IF TG_OP='DELETE' AND (OLD.kind<>'goal' OR OLD.id IS DISTINCT FROM NULLIF(current_setting('tianqiong.goal_id',TRUE),'')::BIGINT) THEN RAISE EXCEPTION 'AI human goal deletion rejected'; END IF;
 ELSE v_task:=tianqiong_ai.assert_task_write(v_exp,NULL); END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION tianqiong_ai.guard_human_task_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_task public.ai_task%ROWTYPE;v_exp BIGINT;v_run UUID;
BEGIN
 SELECT exploration_id INTO v_exp FROM public.ai_task WHERE id=NEW.task_id;
 IF TG_OP='INSERT' THEN
  IF current_setting('tianqiong.execution_mode',TRUE) IS DISTINCT FROM 'human_goal' THEN RAISE EXCEPTION 'AI human event creation rejected'; END IF;
  v_task:=tianqiong_ai.assert_human_goal_scope(v_exp);
  IF NEW.tenant_id<>v_task.tenant_id OR NEW.owner_user_id<>v_task.owner_user_id OR NEW.processed_at IS NOT NULL OR NEW.processed_run IS NOT NULL THEN RAISE EXCEPTION 'AI human event scope rejected'; END IF;
 ELSE
  v_task:=tianqiong_ai.assert_task_write(v_exp,NULL);v_run:=NULLIF(current_setting('tianqiong.execution_id',TRUE),'')::UUID;
  IF OLD.processed_at IS NOT NULL OR NEW.processed_at IS NULL OR NEW.processed_run IS DISTINCT FROM v_run OR (to_jsonb(NEW)-'processed_at'-'processed_run') IS DISTINCT FROM (to_jsonb(OLD)-'processed_at'-'processed_run') THEN RAISE EXCEPTION 'AI human event acknowledgement rejected'; END IF;
 END IF;
 RETURN NEW;
END;
$$;

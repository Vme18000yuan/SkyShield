-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
DROP TRIGGER tq_guard_intent_control ON ai_task_intent_control;
DROP FUNCTION tianqiong_ai.guard_intent_control();
DROP TABLE ai_task_intent_control;

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
  IF TG_OP<>'UPDATE' OR OLD.kind<>'intent' OR NEW.state<>'open' OR OLD.state NOT IN ('blocked','exhausted','stopped')
   OR (NULLIF(current_setting('tianqiong.intent_id',TRUE),'') IS NULL AND OLD.state<>'blocked')
   OR (NULLIF(current_setting('tianqiong.intent_id',TRUE),'') IS NOT NULL AND OLD.id IS DISTINCT FROM NULLIF(current_setting('tianqiong.intent_id',TRUE),'')::BIGINT)
   OR NEW.owner IS NOT NULL OR NEW.blocked_reason IS NOT NULL OR NEW.completed_at IS NOT NULL OR NEW.content_version<>OLD.content_version+1
   OR (to_jsonb(NEW)-'state'-'owner'-'blocked_reason'-'completed_at'-'content_version'-'updated_at') IS DISTINCT FROM (to_jsonb(OLD)-'state'-'owner'-'blocked_reason'-'completed_at'-'content_version'-'updated_at')
  THEN RAISE EXCEPTION 'AI human intent update rejected'; END IF;
 ELSE v_task:=tianqiong_ai.assert_task_write(v_exp,NULL); END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
END;
$$;


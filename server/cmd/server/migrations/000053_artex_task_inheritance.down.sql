-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
DROP TRIGGER tq_related_task_owner ON ai_task;
DROP FUNCTION tianqiong_ai.guard_related_task_owner();
DROP TRIGGER tq_task_relation_scope ON ai_task_relation;
DROP FUNCTION tianqiong_ai.guard_task_relation_scope();

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
  IF TG_OP<>'DELETE' AND (NOT EXISTS(SELECT 1 FROM tianqiong_ai.exploration_nodes WHERE id=NEW.src_id AND exploration_id=v_exp) OR NOT EXISTS(SELECT 1 FROM tianqiong_ai.exploration_nodes WHERE id=NEW.dst_id AND exploration_id=v_exp)) THEN RAISE EXCEPTION 'AI task graph edge crosses exploration boundary'; END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
END;
$$;

CREATE OR REPLACE VIEW tianqiong_ai.tasks AS SELECT id,tenant_id,owner_user_id,name,description,goal,exploration_id,target_id,original_target_id,status,queued,worker_count,max_turns,timeout_seconds,active_run,lease_owner,lease_epoch,lease_until,first_run_at,deadline_at,paused_by_schedule,schedule_manual_until,error_message,created_at,updated_at,completed_at,deleted_at FROM public.ai_task;

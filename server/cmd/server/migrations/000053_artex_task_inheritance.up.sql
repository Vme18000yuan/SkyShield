DO $$
BEGIN
 IF EXISTS(SELECT 1 FROM ai_task_relation r JOIN ai_task child ON child.id=r.task_id JOIN ai_task source ON source.id=r.source_task_id WHERE child.tenant_id<>r.tenant_id OR source.tenant_id<>r.tenant_id OR child.owner_user_id<>source.owner_user_id) THEN
  RAISE EXCEPTION 'Existing AI task relation crosses user scope';
 END IF;
END;
$$;
CREATE FUNCTION tianqiong_ai.guard_task_relation_scope() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE child public.ai_task%ROWTYPE;source public.ai_task%ROWTYPE;
BEGIN
 SELECT * INTO child FROM public.ai_task WHERE id=NEW.task_id AND tenant_id=NEW.tenant_id AND deleted_at IS NULL FOR SHARE;
 SELECT * INTO source FROM public.ai_task WHERE id=NEW.source_task_id AND tenant_id=NEW.tenant_id AND deleted_at IS NULL AND (original_target_id IS NULL OR EXISTS(SELECT 1 FROM public.target WHERE id=ai_task.target_id AND deleted_at IS NULL)) FOR SHARE;
 IF child.id IS NULL OR source.id IS NULL OR child.id=source.id OR child.owner_user_id<>source.owner_user_id THEN RAISE EXCEPTION 'AI task relation scope rejected'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER tq_task_relation_scope BEFORE INSERT OR UPDATE ON ai_task_relation FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_task_relation_scope();
CREATE FUNCTION tianqiong_ai.guard_related_task_owner() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.owner_user_id IS DISTINCT FROM OLD.owner_user_id OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id) AND EXISTS(SELECT 1 FROM public.ai_task_relation r WHERE r.task_id=OLD.id OR r.source_task_id=OLD.id) THEN
  RAISE EXCEPTION 'AI related task owner cannot change';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER tq_related_task_owner BEFORE UPDATE OF tenant_id,owner_user_id ON ai_task FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_related_task_owner();

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

-- Hide source tasks whose associated target has been removed from the product.
CREATE OR REPLACE VIEW tianqiong_ai.tasks AS SELECT id,tenant_id,owner_user_id,name,description,goal,exploration_id,target_id,original_target_id,status,queued,worker_count,max_turns,timeout_seconds,active_run,lease_owner,lease_epoch,lease_until,first_run_at,deadline_at,paused_by_schedule,schedule_manual_until,error_message,created_at,updated_at,completed_at,deleted_at FROM public.ai_task t WHERE t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM public.target target WHERE target.id=t.target_id AND target.deleted_at IS NULL);

-- Human edits are limited to constraints of a currently accessible owned task.
-- Agent writes continue to require the original execution lease.
CREATE FUNCTION tianqiong_ai.guard_task_constraint_write() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_exp BIGINT;v_task public.ai_task%ROWTYPE;v_id BIGINT;v_owner INTEGER;
BEGIN
 IF TG_OP='DELETE' THEN v_exp:=OLD.exploration_id; ELSE v_exp:=NEW.exploration_id; END IF;
 IF current_setting('tianqiong.execution_mode',TRUE)='human_constraint' THEN
  IF TG_OP='INSERT' AND NEW.origin IS DISTINCT FROM 'human' THEN RAISE EXCEPTION 'AI constraint origin rejected'; END IF;
  IF TG_OP='UPDATE' AND (to_jsonb(NEW)-'kind'-'text'-'updated_at') IS DISTINCT FROM (to_jsonb(OLD)-'kind'-'text'-'updated_at') THEN RAISE EXCEPTION 'AI constraint identity cannot change'; END IF;
  v_id:=NULLIF(current_setting('tianqiong.task_id',TRUE),'')::BIGINT;
  v_owner:=NULLIF(current_setting('tianqiong.owner_user_id',TRUE),'')::INTEGER;
  SELECT task.* INTO v_task FROM public.ai_task task
  JOIN public.tenant_membership m ON m.tenant_id=task.tenant_id AND m.user_id=task.owner_user_id
  JOIN public.auth_user u ON u.id=m.user_id JOIN public.tenant tenant ON tenant.id=m.tenant_id
  WHERE task.id=v_id AND task.owner_user_id=v_owner AND task.exploration_id=v_exp AND task.deleted_at IS NULL
  AND u.is_active=TRUE AND tenant.status='active' AND m.role IN ('admin','analyst')
  AND (task.original_target_id IS NULL OR EXISTS(SELECT 1 FROM public.target WHERE id=task.target_id AND deleted_at IS NULL))
  FOR SHARE OF task,m,u;
  IF NOT FOUND THEN RAISE EXCEPTION 'AI constraint scope rejected'; END IF;
 ELSE
  v_task:=tianqiong_ai.assert_task_write(v_exp,NULL);
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
DROP TRIGGER tq_graph_constraint_fence ON tianqiong_ai.task_constraints;
CREATE TRIGGER tq_graph_constraint_fence BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.task_constraints FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_task_constraint_write();

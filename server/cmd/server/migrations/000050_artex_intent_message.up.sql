CREATE TABLE ai_task_intent_message (
 id UUID PRIMARY KEY,
 tenant_id UUID NOT NULL,
 owner_user_id INTEGER NOT NULL,
 task_id BIGINT NOT NULL REFERENCES ai_task(id) ON DELETE CASCADE,
 intent_id BIGINT NOT NULL,
 request_id TEXT NOT NULL,
 display_text TEXT NOT NULL,
 model_text TEXT NOT NULL,
 worker TEXT NOT NULL DEFAULT '',
 activity_id BIGINT,
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','delivered')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 delivered_at TIMESTAMPTZ,
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES tenant_membership(tenant_id,user_id) ON DELETE CASCADE,
 UNIQUE(task_id,request_id)
);
CREATE UNIQUE INDEX ai_task_intent_message_pending ON ai_task_intent_message(task_id,intent_id) WHERE status='pending';
CREATE FUNCTION tianqiong_ai.guard_intent_message() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_task public.ai_task%ROWTYPE;v_exp BIGINT;
BEGIN
 SELECT exploration_id INTO v_exp FROM public.ai_task WHERE id=NEW.task_id;
 IF TG_OP='INSERT' THEN
  IF current_setting('tianqiong.execution_mode',TRUE) IS DISTINCT FROM 'human_intent' THEN RAISE EXCEPTION 'AI intent message creation rejected'; END IF;
  v_task:=tianqiong_ai.assert_human_goal_scope(v_exp);
  IF NEW.tenant_id<>v_task.tenant_id OR NEW.owner_user_id<>v_task.owner_user_id OR v_task.status<>'running' OR v_task.lease_until IS NULL OR v_task.lease_until<=clock_timestamp() OR (v_task.deadline_at IS NOT NULL AND v_task.deadline_at<=clock_timestamp()) OR NEW.intent_id IS DISTINCT FROM NULLIF(current_setting('tianqiong.intent_id',TRUE),'')::BIGINT OR NEW.status<>'pending' OR NEW.delivered_at IS NOT NULL OR NEW.activity_id IS NOT NULL OR NOT EXISTS(SELECT 1 FROM tianqiong_ai.exploration_nodes WHERE exploration_id=v_exp AND id=NEW.intent_id AND kind='intent' AND state='open') THEN RAISE EXCEPTION 'AI intent message scope rejected'; END IF;
 ELSE
  v_task:=tianqiong_ai.assert_task_write(v_exp,NULL);
  IF OLD.status<>'pending' OR (to_jsonb(NEW)-'activity_id'-'status'-'delivered_at') IS DISTINCT FROM (to_jsonb(OLD)-'activity_id'-'status'-'delivered_at') THEN RAISE EXCEPTION 'AI intent message update rejected'; END IF;
  IF OLD.activity_id IS NULL AND NEW.activity_id IS NOT NULL AND NEW.status='pending' AND NEW.delivered_at IS NULL THEN
   IF NOT EXISTS(SELECT 1 FROM tianqiong_ai.activity WHERE id=NEW.activity_id AND exploration_id=v_exp AND node_id=NEW.intent_id AND worker='user' AND kind='user') THEN RAISE EXCEPTION 'AI intent message activity rejected'; END IF;
  ELSIF OLD.activity_id IS NOT NULL AND NEW.activity_id=OLD.activity_id AND NEW.status='delivered' AND NEW.delivered_at IS NOT NULL THEN NULL;
  ELSE RAISE EXCEPTION 'AI intent message transition rejected'; END IF;
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER tq_guard_intent_message BEFORE INSERT OR UPDATE ON ai_task_intent_message FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_intent_message();

CREATE TABLE ai_task_human_event (
 id BIGSERIAL PRIMARY KEY,
 tenant_id UUID NOT NULL,
 owner_user_id INTEGER NOT NULL,
 task_id BIGINT NOT NULL REFERENCES ai_task(id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK(kind IN ('goal','goal_edited','goal_deleted')),
 payload JSONB NOT NULL DEFAULT '{}'::jsonb,
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 processed_at TIMESTAMPTZ,
 processed_run UUID,
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES tenant_membership(tenant_id,user_id) ON DELETE CASCADE
);
CREATE INDEX ai_task_human_event_pending ON ai_task_human_event(task_id,id) WHERE processed_at IS NULL;

CREATE FUNCTION tianqiong_ai.assert_human_goal_scope(p_exp BIGINT) RETURNS public.ai_task LANGUAGE plpgsql AS $$
DECLARE v_id BIGINT;v_owner INTEGER;v_task public.ai_task%ROWTYPE;
BEGIN
 v_id:=NULLIF(current_setting('tianqiong.task_id',TRUE),'')::BIGINT;
 v_owner:=NULLIF(current_setting('tianqiong.owner_user_id',TRUE),'')::INTEGER;
 SELECT task.* INTO v_task FROM public.ai_task task JOIN public.tenant_membership m ON m.tenant_id=task.tenant_id AND m.user_id=task.owner_user_id JOIN public.auth_user u ON u.id=m.user_id JOIN public.tenant tenant ON tenant.id=m.tenant_id
 WHERE task.id=v_id AND task.owner_user_id=v_owner AND task.exploration_id=p_exp AND task.deleted_at IS NULL AND u.is_active=TRUE AND tenant.status='active' AND m.role IN ('admin','analyst')
 AND (task.original_target_id IS NULL OR EXISTS(SELECT 1 FROM public.target WHERE id=task.target_id AND deleted_at IS NULL)) FOR SHARE OF task,m,u;
 IF NOT FOUND THEN RAISE EXCEPTION 'AI human goal scope rejected'; END IF;
 RETURN v_task;
END;
$$;

CREATE FUNCTION tianqiong_ai.guard_goal_node_write() RETURNS trigger LANGUAGE plpgsql AS $$
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
DROP TRIGGER tq_graph_node_fence ON tianqiong_ai.exploration_nodes;
CREATE TRIGGER tq_graph_node_fence BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.exploration_nodes FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_goal_node_write();

CREATE FUNCTION tianqiong_ai.guard_goal_edge_write() RETURNS trigger LANGUAGE plpgsql AS $$
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
DROP TRIGGER tq_graph_edge_fence ON tianqiong_ai.exploration_edges;
CREATE TRIGGER tq_graph_edge_fence BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.exploration_edges FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_goal_edge_write();

CREATE FUNCTION tianqiong_ai.guard_goal_dependent_write() RETURNS trigger LANGUAGE plpgsql AS $$
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
DROP TRIGGER tq_graph_anchor_fence ON tianqiong_ai.exploration_anchors;
CREATE TRIGGER tq_graph_anchor_fence BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.exploration_anchors FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_goal_dependent_write();
DROP TRIGGER tq_graph_activity_fence ON tianqiong_ai.activity;
CREATE TRIGGER tq_graph_activity_fence BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.activity FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_goal_dependent_write();
DROP TRIGGER tq_graph_intelligence_fence ON tianqiong_ai.task_intelligence;
CREATE TRIGGER tq_graph_intelligence_fence BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.task_intelligence FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_goal_dependent_write();

CREATE FUNCTION tianqiong_ai.guard_human_task_event() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER tq_guard_human_task_event BEFORE INSERT OR UPDATE ON ai_task_human_event FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_human_task_event();

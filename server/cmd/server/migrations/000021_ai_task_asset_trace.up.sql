BEGIN;
-- Existing anchors were already checked against their task's tenant and target.
INSERT INTO public.ai_task_asset(tenant_id,task_id,asset_id,source)
 SELECT DISTINCT t.tenant_id,t.id,a.asset_id,'anchored' FROM tianqiong_ai.exploration_anchors a
 JOIN tianqiong_ai.exploration_nodes n ON n.id=a.node_id JOIN public.ai_task t ON t.exploration_id=n.exploration_id
 JOIN public.asset_entity e ON e.id=a.asset_id AND e.tenant_id=t.tenant_id
 ON CONFLICT(task_id,asset_id) DO NOTHING;
CREATE FUNCTION tianqiong_ai.link_anchored_asset() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_exp BIGINT; v_task public.ai_task%ROWTYPE;
BEGIN
 SELECT exploration_id INTO v_exp FROM tianqiong_ai.exploration_nodes WHERE id=NEW.node_id;
 v_task:=tianqiong_ai.assert_task_write(v_exp,NEW.asset_id);
 INSERT INTO public.ai_task_asset(tenant_id,task_id,asset_id,source) VALUES(v_task.tenant_id,v_task.id,NEW.asset_id,'anchored') ON CONFLICT(task_id,asset_id) DO NOTHING;
 RETURN NEW;
END;
$$;
CREATE TRIGGER tq_anchor_task_asset AFTER INSERT ON tianqiong_ai.exploration_anchors
 FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.link_anchored_asset();
COMMIT;

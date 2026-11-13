-- Cross-task Auto hints have a conversation lease, rather than the target
-- task's own worker lease. Keep this as a narrow insert-only graph mode.
CREATE FUNCTION tianqiong_ai.assert_auto_hint_scope(p_exp BIGINT,p_asset BIGINT DEFAULT NULL) RETURNS public.ai_task LANGUAGE plpgsql AS $$
DECLARE v_task public.ai_task%ROWTYPE; v_id BIGINT; v_owner INTEGER; v_run UUID;
BEGIN
 v_id:=NULLIF(current_setting('tianqiong.task_id',TRUE),'')::BIGINT;
 v_owner:=NULLIF(current_setting('tianqiong.owner_user_id',TRUE),'')::INTEGER;
 v_run:=NULLIF(current_setting('tianqiong.execution_id',TRUE),'')::UUID;
 SELECT task.* INTO v_task FROM public.ai_task task
 JOIN public.tenant_membership m ON m.tenant_id=task.tenant_id AND m.user_id=task.owner_user_id
 JOIN public.auth_user u ON u.id=m.user_id JOIN public.tenant tenant ON tenant.id=m.tenant_id
 JOIN public.ai_run run ON run.tenant_id=task.tenant_id AND run.owner_user_id=task.owner_user_id
 JOIN public.ai_conversation c ON c.id=run.conversation_id AND c.tenant_id=run.tenant_id AND c.owner_user_id=run.owner_user_id
 JOIN public.ai_tool_catalog tool ON tool.tenant_id=task.tenant_id AND tool.key='add_task_hint' AND tool.system AND tool.enabled AND tool.agents @> jsonb_build_array(CASE c.agent_key WHEN 'assistant' THEN 'mainagent' ELSE c.agent_key END)
 WHERE task.id=v_id AND task.owner_user_id=v_owner AND task.exploration_id=p_exp AND task.deleted_at IS NULL
 AND tenant.status='active' AND u.is_active AND m.role IN ('admin','analyst') AND run.id=v_run AND run.status='running' AND run.lease_until>clock_timestamp()
 AND tianqiong_ai.finding_case_review_conversation_visible(c.id)
 AND (task.original_target_id IS NULL OR EXISTS(SELECT 1 FROM public.target x WHERE x.id=task.target_id AND x.deleted_at IS NULL))
 AND NOT EXISTS(SELECT 1 FROM public.ai_task_archive a WHERE a.task_id=task.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting'))
 AND NOT EXISTS(SELECT 1 FROM public.ai_agent_trigger_job job LEFT JOIN public.ai_task source ON source.id=job.source_task_id AND source.tenant_id=job.tenant_id AND source.owner_user_id=job.owner_user_id
  WHERE job.run_id=run.id AND job.source_task_id IS NOT NULL AND (job.status<>'running' OR source.id IS NULL OR source.deleted_at IS NOT NULL OR (source.original_target_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM public.target x WHERE x.id=source.target_id AND x.deleted_at IS NULL)) OR EXISTS(SELECT 1 FROM public.ai_task_archive a WHERE a.task_id=source.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting'))))
 FOR SHARE OF task,m,u,run,c,tool;
 IF NOT FOUND THEN RAISE EXCEPTION 'AI Auto hint execution scope rejected'; END IF;
 IF p_asset IS NOT NULL THEN
  IF NOT EXISTS(SELECT 1 FROM public.asset_entity a WHERE a.id=p_asset AND a.tenant_id=v_task.tenant_id) THEN RAISE EXCEPTION 'AI Auto hint asset scope rejected'; END IF;
  IF v_task.target_id IS NOT NULL AND NOT (
   EXISTS(SELECT 1 FROM public.asset_website_binding b JOIN public.website a ON a.id=b.website_id WHERE b.asset_id=p_asset AND a.target_id=v_task.target_id) OR
   EXISTS(SELECT 1 FROM public.asset_endpoint_binding b JOIN public.endpoint a ON a.id=b.endpoint_id WHERE b.asset_id=p_asset AND a.target_id=v_task.target_id) OR
   EXISTS(SELECT 1 FROM public.asset_subdomain_binding b JOIN public.subdomain a ON a.id=b.subdomain_id WHERE b.asset_id=p_asset AND a.target_id=v_task.target_id) OR
   EXISTS(SELECT 1 FROM public.asset_host_port_binding b JOIN public.host_port_mapping a ON a.id=b.host_port_id WHERE b.asset_id=p_asset AND a.target_id=v_task.target_id) OR
   EXISTS(SELECT 1 FROM public.asset_observation o WHERE o.asset_id=p_asset AND o.tenant_id=v_task.tenant_id AND o.target_id=v_task.target_id) OR
   EXISTS(SELECT 1 FROM public.asset_application_visible_website app JOIN public.website w ON w.id=app.website_id WHERE app.application_id=p_asset AND app.tenant_id=v_task.tenant_id AND w.target_id=v_task.target_id) OR
   EXISTS(SELECT 1 FROM public.asset_root_domain_target root JOIN public.asset_entity entity ON entity.id=root.root_asset_id JOIN public.target target ON target.id=root.target_id WHERE root.tenant_id=v_task.tenant_id AND root.root_asset_id=p_asset AND root.target_id=v_task.target_id AND target.deleted_at IS NULL AND target.type='domain' AND target.name=entity.label AND entity.asset_type='root_domain')
  ) THEN RAISE EXCEPTION 'AI Auto hint asset is outside the selected target'; END IF;
 END IF;
 RETURN v_task;
END $$;

CREATE TABLE tianqiong_ai.auto_hint_staging (
 node_id BIGINT PRIMARY KEY REFERENCES tianqiong_ai.exploration_nodes(id) ON DELETE CASCADE,
 task_id BIGINT NOT NULL REFERENCES public.ai_task(id) ON DELETE CASCADE,
 run_id UUID NOT NULL,
 transaction_id BIGINT NOT NULL
);

CREATE FUNCTION tianqiong_ai.guard_auto_hint_node() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_task public.ai_task%ROWTYPE; v_binding BOOLEAN;
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'AI Auto hint node modification rejected'; END IF;
 v_task:=tianqiong_ai.assert_auto_hint_scope(NEW.exploration_id,NULL);
 IF NEW.kind<>'hint' OR NEW.state<>'active' OR NEW.origin IS DISTINCT FROM 'human' OR NEW.priority<>0 OR NEW.owner IS NOT NULL OR jsonb_typeof(NEW.payload) IS DISTINCT FROM 'object' OR jsonb_typeof(NEW.payload->'text') IS DISTINCT FROM 'string' OR btrim(COALESCE(NEW.payload->>'text',''))='' OR octet_length(NEW.payload->>'text')>8000 OR (NEW.payload ? 'traffic_refs' AND jsonb_typeof(NEW.payload->'traffic_refs') IS DISTINCT FROM 'array') OR (NEW.payload - 'text' - 'traffic_refs')<>'{}'::jsonb THEN RAISE EXCEPTION 'AI Auto hint node rejected'; END IF;
 IF NEW.payload ? 'traffic_refs' AND jsonb_array_length(NEW.payload->'traffic_refs')>0 THEN
  SELECT COALESCE(options->>'agent_traffic_binding','false')='true' INTO v_binding FROM public.ai_runtime_setting WHERE tenant_id=v_task.tenant_id FOR SHARE;
  IF NOT COALESCE(v_binding,FALSE) THEN RAISE EXCEPTION 'AI Auto hint traffic binding disabled'; END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE FUNCTION tianqiong_ai.guard_auto_hint_staging() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_exp BIGINT; v_task public.ai_task%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' AND pg_trigger_depth()>1 THEN RETURN OLD; END IF;
 IF current_setting('tianqiong.execution_mode',TRUE) IS DISTINCT FROM 'auto_hint' THEN RAISE EXCEPTION 'AI Auto hint staging rejected'; END IF;
 IF TG_OP='INSERT' THEN
  IF pg_trigger_depth()<2 OR NEW.transaction_id<>txid_current() OR NEW.run_id IS DISTINCT FROM NULLIF(current_setting('tianqiong.execution_id',TRUE),'')::UUID THEN RAISE EXCEPTION 'AI Auto hint staging insertion rejected'; END IF;
  SELECT exploration_id INTO v_exp FROM tianqiong_ai.exploration_nodes WHERE id=NEW.node_id AND kind='hint' AND state='active' AND origin='human';
  v_task:=tianqiong_ai.assert_auto_hint_scope(v_exp,NULL);
  IF NEW.task_id<>v_task.id THEN RAISE EXCEPTION 'AI Auto hint staging scope rejected'; END IF;
  RETURN NEW;
 ELSIF TG_OP='DELETE' THEN
  IF OLD.transaction_id<>txid_current() OR OLD.run_id IS DISTINCT FROM NULLIF(current_setting('tianqiong.execution_id',TRUE),'')::UUID THEN RAISE EXCEPTION 'AI Auto hint staging cleanup rejected'; END IF;
  SELECT exploration_id INTO v_exp FROM public.ai_task WHERE id=OLD.task_id;
  v_task:=tianqiong_ai.assert_auto_hint_scope(v_exp,NULL);
  RETURN OLD;
 END IF;
 RAISE EXCEPTION 'AI Auto hint staging update rejected';
END $$;
CREATE TRIGGER tq_guard_auto_hint_staging BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.auto_hint_staging FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_auto_hint_staging();
CREATE FUNCTION tianqiong_ai.stage_auto_hint_node() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_task public.ai_task%ROWTYPE;
BEGIN
 v_task:=tianqiong_ai.assert_auto_hint_scope(NEW.exploration_id,NULL);
 INSERT INTO tianqiong_ai.auto_hint_staging(node_id,task_id,run_id,transaction_id) VALUES(NEW.id,v_task.id,NULLIF(current_setting('tianqiong.execution_id',TRUE),'')::UUID,txid_current());
 RETURN NEW;
END $$;
CREATE FUNCTION tianqiong_ai.guard_auto_hint_anchor() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_exp BIGINT; v_task public.ai_task%ROWTYPE;
BEGIN
 IF TG_OP<>'INSERT' OR NEW.node_id IS DISTINCT FROM NULLIF(current_setting('tianqiong.hint_id',TRUE),'')::BIGINT THEN RAISE EXCEPTION 'AI Auto hint anchor rejected'; END IF;
 SELECT n.exploration_id INTO v_exp FROM tianqiong_ai.exploration_nodes n JOIN tianqiong_ai.auto_hint_staging staged ON staged.node_id=n.id AND staged.task_id=NULLIF(current_setting('tianqiong.task_id',TRUE),'')::BIGINT AND staged.run_id=NULLIF(current_setting('tianqiong.execution_id',TRUE),'')::UUID AND staged.transaction_id=txid_current() WHERE n.id=NEW.node_id AND n.kind='hint' AND n.state='active' AND n.origin='human';
 v_task:=tianqiong_ai.assert_auto_hint_scope(v_exp,NEW.asset_id);
 RETURN NEW;
END $$;
CREATE FUNCTION tianqiong_ai.guard_auto_hint_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_exp BIGINT; v_task public.ai_task%ROWTYPE;
BEGIN
 IF TG_OP<>'INSERT' OR NEW.kind<>'hint' OR NEW.processed_at IS NOT NULL OR NEW.processed_run IS NOT NULL OR jsonb_typeof(NEW.payload->'hints') IS DISTINCT FROM 'array' OR jsonb_array_length(NEW.payload->'hints') NOT BETWEEN 1 AND 100 OR (NEW.payload-'hints')<>'{}'::jsonb THEN RAISE EXCEPTION 'AI Auto hint event rejected'; END IF;
 IF EXISTS(SELECT 1 FROM jsonb_array_elements(NEW.payload->'hints') x WHERE jsonb_typeof(x) IS DISTINCT FROM 'string' OR btrim(x#>>'{}')='' OR octet_length(x#>>'{}')>8000) THEN RAISE EXCEPTION 'AI Auto hint event content rejected'; END IF;
 SELECT exploration_id INTO v_exp FROM public.ai_task WHERE id=NEW.task_id;
 v_task:=tianqiong_ai.assert_auto_hint_scope(v_exp,NULL);
 IF NEW.tenant_id<>v_task.tenant_id OR NEW.owner_user_id<>v_task.owner_user_id THEN RAISE EXCEPTION 'AI Auto hint event scope rejected'; END IF;
 IF NEW.payload->'hints' IS DISTINCT FROM (SELECT COALESCE(jsonb_agg(n.payload->'text' ORDER BY n.id),'[]'::jsonb) FROM tianqiong_ai.auto_hint_staging staged JOIN tianqiong_ai.exploration_nodes n ON n.id=staged.node_id WHERE staged.task_id=v_task.id AND staged.run_id=NULLIF(current_setting('tianqiong.execution_id',TRUE),'')::UUID AND staged.transaction_id=txid_current()) THEN RAISE EXCEPTION 'AI Auto hint event does not match its new nodes'; END IF;
 RETURN NEW;
END $$;
CREATE FUNCTION tianqiong_ai.link_auto_hint_asset() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_exp BIGINT; v_task public.ai_task%ROWTYPE;
BEGIN
 SELECT exploration_id INTO v_exp FROM tianqiong_ai.exploration_nodes WHERE id=NEW.node_id;
 v_task:=tianqiong_ai.assert_auto_hint_scope(v_exp,NEW.asset_id);
 INSERT INTO public.ai_task_asset(tenant_id,task_id,asset_id,source) VALUES(v_task.tenant_id,v_task.id,NEW.asset_id,'anchored') ON CONFLICT(task_id,asset_id) DO NOTHING;
 RETURN NEW;
END $$;

DROP TRIGGER tq_graph_node_fence ON tianqiong_ai.exploration_nodes;
CREATE TRIGGER tq_graph_node_fence BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.exploration_nodes FOR EACH ROW WHEN (current_setting('tianqiong.execution_mode',TRUE) IS DISTINCT FROM 'auto_hint') EXECUTE FUNCTION tianqiong_ai.guard_goal_node_write();
CREATE TRIGGER tq_auto_hint_node BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.exploration_nodes FOR EACH ROW WHEN (current_setting('tianqiong.execution_mode',TRUE)='auto_hint') EXECUTE FUNCTION tianqiong_ai.guard_auto_hint_node();
CREATE TRIGGER tq_stage_auto_hint_node AFTER INSERT ON tianqiong_ai.exploration_nodes FOR EACH ROW WHEN (current_setting('tianqiong.execution_mode',TRUE)='auto_hint') EXECUTE FUNCTION tianqiong_ai.stage_auto_hint_node();
DROP TRIGGER tq_graph_anchor_fence ON tianqiong_ai.exploration_anchors;
CREATE TRIGGER tq_graph_anchor_fence BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.exploration_anchors FOR EACH ROW WHEN (current_setting('tianqiong.execution_mode',TRUE) IS DISTINCT FROM 'auto_hint') EXECUTE FUNCTION tianqiong_ai.guard_goal_dependent_write();
CREATE TRIGGER tq_auto_hint_anchor BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.exploration_anchors FOR EACH ROW WHEN (current_setting('tianqiong.execution_mode',TRUE)='auto_hint') EXECUTE FUNCTION tianqiong_ai.guard_auto_hint_anchor();
DROP TRIGGER tq_anchor_task_asset ON tianqiong_ai.exploration_anchors;
CREATE TRIGGER tq_anchor_task_asset AFTER INSERT ON tianqiong_ai.exploration_anchors FOR EACH ROW WHEN (current_setting('tianqiong.execution_mode',TRUE) IS DISTINCT FROM 'auto_hint') EXECUTE FUNCTION tianqiong_ai.link_anchored_asset();
CREATE TRIGGER tq_auto_hint_link AFTER INSERT ON tianqiong_ai.exploration_anchors FOR EACH ROW WHEN (current_setting('tianqiong.execution_mode',TRUE)='auto_hint') EXECUTE FUNCTION tianqiong_ai.link_auto_hint_asset();
DROP TRIGGER tq_guard_human_task_event ON public.ai_task_human_event;
CREATE TRIGGER tq_guard_human_task_event BEFORE INSERT OR UPDATE ON public.ai_task_human_event FOR EACH ROW WHEN (current_setting('tianqiong.execution_mode',TRUE) IS DISTINCT FROM 'auto_hint') EXECUTE FUNCTION tianqiong_ai.guard_human_task_event();
CREATE TRIGGER tq_auto_hint_event BEFORE INSERT OR UPDATE ON public.ai_task_human_event FOR EACH ROW WHEN (current_setting('tianqiong.execution_mode',TRUE)='auto_hint') EXECUTE FUNCTION tianqiong_ai.guard_auto_hint_event();

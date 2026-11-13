-- MainAgent turns have their own fenced lease, independent of planner/worker runs.
CREATE TABLE public.ai_task_main_chat_run (
 task_id bigint PRIMARY KEY REFERENCES public.ai_task(id) ON DELETE CASCADE,
 tenant_id uuid NOT NULL,
 owner_user_id integer NOT NULL,
 run_id uuid NOT NULL UNIQUE,
 status text NOT NULL CHECK(status IN ('running','stopping')),
 lease_until timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(tenant_id,task_id) REFERENCES public.ai_task(tenant_id,id) ON DELETE CASCADE,
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES public.tenant_membership(tenant_id,user_id) ON DELETE CASCADE
);
CREATE TABLE public.ai_task_main_chat_wake (
 node_id bigint PRIMARY KEY REFERENCES tianqiong_ai.exploration_nodes(id) ON DELETE CASCADE,
 task_id bigint NOT NULL REFERENCES public.ai_task(id) ON DELETE CASCADE,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX ai_task_main_chat_wake_task ON public.ai_task_main_chat_wake(task_id,node_id);

CREATE FUNCTION tianqiong_ai.task_run_live(p_task bigint,p_owner integer,p_run uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM public.ai_task t JOIN public.tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id JOIN public.tenant tenant ON tenant.id=m.tenant_id JOIN public.auth_user u ON u.id=m.user_id
 WHERE t.id=p_task AND t.owner_user_id=p_owner AND t.deleted_at IS NULL AND u.is_active AND tenant.status='active' AND m.role IN ('admin','analyst')
 AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM public.target x WHERE x.id=t.target_id AND x.deleted_at IS NULL))
 AND NOT EXISTS(SELECT 1 FROM public.ai_task_archive a WHERE a.task_id=t.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting'))
 AND ((t.active_run=p_run AND t.status='running' AND t.lease_until>clock_timestamp()) OR EXISTS(SELECT 1 FROM public.ai_task_main_chat_run c WHERE c.task_id=t.id AND c.tenant_id=t.tenant_id AND c.owner_user_id=t.owner_user_id AND c.run_id=p_run AND c.status='running' AND c.lease_until>clock_timestamp())))
$$;
CREATE FUNCTION tianqiong_ai.task_run_mode(p_task bigint,p_run uuid) RETURNS text LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN EXISTS(SELECT 1 FROM public.ai_task_main_chat_run c WHERE c.task_id=p_task AND c.run_id=p_run AND c.status='running' AND c.lease_until>clock_timestamp()) THEN 'main_chat' ELSE 'runtime' END
$$;

CREATE OR REPLACE FUNCTION tianqiong_ai.assert_task_write(p_exploration BIGINT,p_asset BIGINT DEFAULT NULL)
RETURNS public.ai_task LANGUAGE plpgsql AS $$
DECLARE v_task public.ai_task%ROWTYPE; v_id BIGINT; v_owner INTEGER; v_run UUID; v_mode TEXT;
BEGIN
 v_id:=NULLIF(current_setting('tianqiong.task_id',TRUE),'')::BIGINT;
 v_owner:=NULLIF(current_setting('tianqiong.owner_user_id',TRUE),'')::INTEGER;
 v_run:=NULLIF(current_setting('tianqiong.execution_id',TRUE),'')::UUID;
 v_mode:=current_setting('tianqiong.execution_mode',TRUE);
 SELECT task.* INTO v_task FROM public.ai_task task
 JOIN public.tenant_membership m ON m.tenant_id=task.tenant_id AND m.user_id=task.owner_user_id
 JOIN public.auth_user u ON u.id=m.user_id JOIN public.tenant tenant ON tenant.id=m.tenant_id
 WHERE task.id=v_id AND task.owner_user_id=v_owner AND task.exploration_id=p_exploration
 AND task.deleted_at IS NULL AND u.is_active=TRUE
 AND tenant.status='active' AND m.role IN ('admin','analyst') FOR SHARE OF task,m,u;
 IF NOT FOUND THEN RAISE EXCEPTION 'AI task execution scope rejected'; END IF;
 IF v_mode='setup' THEN
   IF v_task.active_run IS DISTINCT FROM v_run OR v_task.status<>'created' OR v_task.lease_epoch<>0 THEN RAISE EXCEPTION 'AI task setup scope rejected'; END IF;
 ELSIF v_mode='runtime' THEN
   IF v_task.active_run IS DISTINCT FROM v_run OR v_task.status<>'running' OR v_task.lease_until IS NULL OR v_task.lease_until<=clock_timestamp() THEN RAISE EXCEPTION 'AI task execution lease rejected'; END IF;
 ELSIF v_mode='main_chat' THEN
   IF NOT EXISTS(SELECT 1 FROM public.ai_task_main_chat_run c WHERE c.task_id=v_task.id AND c.tenant_id=v_task.tenant_id AND c.owner_user_id=v_owner AND c.run_id=v_run AND c.status='running' AND c.lease_until>clock_timestamp()) OR EXISTS(SELECT 1 FROM public.ai_task_archive a WHERE a.task_id=v_task.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting')) THEN RAISE EXCEPTION 'AI task chat lease rejected'; END IF;
 ELSE RAISE EXCEPTION 'AI task execution mode rejected';
 END IF;
 IF v_task.original_target_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM public.target WHERE id=v_task.target_id AND deleted_at IS NULL) THEN
   RAISE EXCEPTION 'AI task target is inactive';
 END IF;
 IF p_asset IS NOT NULL THEN
   IF NOT EXISTS(SELECT 1 FROM public.asset_entity a WHERE a.id=p_asset AND a.tenant_id=v_task.tenant_id) THEN RAISE EXCEPTION 'AI task asset scope rejected'; END IF;
   IF v_task.target_id IS NOT NULL AND NOT (
     EXISTS(SELECT 1 FROM public.asset_website_binding b JOIN public.website a ON a.id=b.website_id WHERE b.asset_id=p_asset AND a.target_id=v_task.target_id) OR
     EXISTS(SELECT 1 FROM public.asset_endpoint_binding b JOIN public.endpoint a ON a.id=b.endpoint_id WHERE b.asset_id=p_asset AND a.target_id=v_task.target_id) OR
     EXISTS(SELECT 1 FROM public.asset_subdomain_binding b JOIN public.subdomain a ON a.id=b.subdomain_id WHERE b.asset_id=p_asset AND a.target_id=v_task.target_id) OR
     EXISTS(SELECT 1 FROM public.asset_host_port_binding b JOIN public.host_port_mapping a ON a.id=b.host_port_id WHERE b.asset_id=p_asset AND a.target_id=v_task.target_id) OR
     EXISTS(SELECT 1 FROM public.asset_observation o WHERE o.asset_id=p_asset AND o.tenant_id=v_task.tenant_id AND o.target_id=v_task.target_id) OR
     EXISTS(SELECT 1 FROM public.asset_application_visible_website app JOIN public.website w ON w.id=app.website_id WHERE app.application_id=p_asset AND app.tenant_id=v_task.tenant_id AND w.target_id=v_task.target_id) OR
     EXISTS(SELECT 1 FROM public.asset_root_domain_target r JOIN public.target t ON t.id=r.target_id JOIN public.asset_entity root ON root.tenant_id=r.tenant_id AND root.id=r.root_asset_id
       WHERE r.tenant_id=v_task.tenant_id AND r.root_asset_id=p_asset AND r.target_id=v_task.target_id AND t.deleted_at IS NULL AND t.type='domain' AND t.name=root.label AND root.asset_type='root_domain')
   ) THEN RAISE EXCEPTION 'AI task asset is outside the selected target'; END IF;
 END IF;
 RETURN v_task;
END;
$$;

ALTER TABLE public.ai_task_human_event DROP CONSTRAINT ai_task_human_event_kind_check;
ALTER TABLE public.ai_task_human_event ADD CONSTRAINT ai_task_human_event_kind_check CHECK(kind IN ('goal','goal_edited','goal_deleted','intent_reopened','cancelled','hint','intent_added'));
CREATE OR REPLACE FUNCTION tianqiong_ai.guard_human_task_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_task public.ai_task%ROWTYPE;v_exp BIGINT;v_run UUID;v_mode text;
BEGIN
 SELECT exploration_id INTO v_exp FROM public.ai_task WHERE id=NEW.task_id;
 v_mode:=current_setting('tianqiong.execution_mode',TRUE);
 IF TG_OP='INSERT' THEN
  IF v_mode='main_chat' AND pg_trigger_depth()>1 AND NEW.kind IN ('goal','hint','intent_added') THEN
   v_task:=tianqiong_ai.assert_task_write(v_exp,NULL);
  ELSIF COALESCE((v_mode='human_goal' AND NEW.kind IN ('goal','goal_edited','goal_deleted')) OR (v_mode='human_intent' AND NEW.kind IN ('intent_reopened','cancelled')),FALSE) THEN
   v_task:=tianqiong_ai.assert_human_goal_scope(v_exp);
  ELSE RAISE EXCEPTION 'AI human event creation rejected'; END IF;
  IF NEW.tenant_id<>v_task.tenant_id OR NEW.owner_user_id<>v_task.owner_user_id OR NEW.processed_at IS NOT NULL OR NEW.processed_run IS NOT NULL THEN RAISE EXCEPTION 'AI human event scope rejected'; END IF;
 ELSE
  v_task:=tianqiong_ai.assert_task_write(v_exp,NULL);v_run:=NULLIF(current_setting('tianqiong.execution_id',TRUE),'')::UUID;
  IF v_mode IS DISTINCT FROM 'runtime' OR OLD.processed_at IS NOT NULL OR NEW.processed_at IS NULL OR NEW.processed_run IS DISTINCT FROM v_run OR (to_jsonb(NEW)-'processed_at'-'processed_run') IS DISTINCT FROM (to_jsonb(OLD)-'processed_at'-'processed_run') THEN RAISE EXCEPTION 'AI human event acknowledgement rejected'; END IF;
 END IF;
 RETURN NEW;
END;
$$;
CREATE FUNCTION tianqiong_ai.main_chat_node_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_task public.ai_task%ROWTYPE; v_kind text; v_payload jsonb;
BEGIN
 IF current_setting('tianqiong.execution_mode',TRUE) IS DISTINCT FROM 'main_chat' OR NEW.kind NOT IN ('goal','intent','hint') THEN RETURN NEW; END IF;
 v_task:=tianqiong_ai.assert_task_write(NEW.exploration_id,NULL);
 IF NEW.kind='goal' THEN
  v_kind:='goal';v_payload:=jsonb_build_object('goals',jsonb_build_array(COALESCE(NEW.payload->>'text','')));
 ELSIF NEW.kind='hint' THEN
  v_kind:='hint';v_payload:=jsonb_build_object('hints',jsonb_build_array(COALESCE(NEW.payload->>'text','')));
 ELSE
  v_kind:='intent_added';v_payload:=jsonb_build_object('intent_id',NEW.id,'summary',COALESCE(NEW.payload->>'summary',''));
 END IF;
 INSERT INTO public.ai_task_human_event(tenant_id,owner_user_id,task_id,kind,payload) VALUES(v_task.tenant_id,v_task.owner_user_id,v_task.id,v_kind,v_payload);
 IF NEW.kind IN ('goal','intent') THEN INSERT INTO public.ai_task_main_chat_wake(node_id,task_id) VALUES(NEW.id,v_task.id); END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER tq_main_chat_node_event AFTER INSERT ON tianqiong_ai.exploration_nodes FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.main_chat_node_event();

CREATE OR REPLACE FUNCTION tianqiong_ai.guard_finding_write() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_task public.ai_task%ROWTYPE;v_exp BIGINT;v_node BIGINT;v_id BIGINT;v_owner INTEGER;v_run UUID;
BEGIN
 IF current_setting('tianqiong.execution_mode',TRUE)='human_intent_hard' THEN
  SELECT exploration_id INTO v_exp FROM public.ai_task WHERE id=NULLIF(current_setting('tianqiong.task_id',TRUE),'')::bigint;
  v_task:=tianqiong_ai.assert_human_goal_scope(v_exp);
  IF TG_OP<>'DELETE' OR OLD.task_id IS DISTINCT FROM v_task.id OR OLD.node_id IS NULL OR NOT (OLD.node_id=ANY(COALESCE(NULLIF(current_setting('tianqiong.delete_ids',TRUE),'')::bigint[],'{}'::bigint[]))) OR EXISTS(SELECT 1 FROM public.ai_finding_retest WHERE finding_id=OLD.id AND status IN ('pending','running')) THEN RAISE EXCEPTION 'AI hard intent finding deletion rejected'; END IF;
  RETURN OLD;
 END IF;
 IF TG_OP='UPDATE' AND current_setting('tianqiong.execution_mode',TRUE)='conversation_retest' THEN
   IF (to_jsonb(NEW)-'status') IS DISTINCT FROM (to_jsonb(OLD)-'status') OR NEW.status<>'fixed' OR pg_trigger_depth()<2 THEN
     RAISE EXCEPTION 'AI retest scope can only mark a finding fixed';
   END IF;
   v_id:=NULLIF(current_setting('tianqiong.task_id',TRUE),'')::BIGINT;
   v_owner:=NULLIF(current_setting('tianqiong.owner_user_id',TRUE),'')::INTEGER;
   v_run:=NULLIF(current_setting('tianqiong.execution_id',TRUE),'')::UUID;
   SELECT task.* INTO v_task FROM public.ai_task task
   JOIN public.tenant_membership m ON m.tenant_id=task.tenant_id AND m.user_id=task.owner_user_id
   JOIN public.auth_user u ON u.id=m.user_id JOIN public.tenant tenant ON tenant.id=m.tenant_id
   JOIN public.ai_finding_retest retest ON retest.finding_id=OLD.id AND retest.tenant_id=task.tenant_id AND retest.owner_user_id=task.owner_user_id
   JOIN public.ai_run run ON run.id=retest.run_id AND run.owner_user_id=task.owner_user_id AND run.tenant_id=task.tenant_id
   WHERE task.id=v_id AND task.id=OLD.task_id AND task.owner_user_id=v_owner AND task.deleted_at IS NULL
   AND m.role IN ('admin','analyst') AND u.is_active=TRUE AND tenant.status='active'
   AND (task.original_target_id IS NULL OR EXISTS(SELECT 1 FROM public.target WHERE id=task.target_id AND deleted_at IS NULL))
   AND run.id=v_run AND run.status='completed' AND retest.status='completed' AND retest.verdict='fixed' FOR SHARE OF task,m,u;
   IF NOT FOUND THEN RAISE EXCEPTION 'AI retest execution scope rejected'; END IF;
   RETURN NEW;
 END IF;
 IF TG_OP='UPDATE' AND current_setting('tianqiong.execution_mode',TRUE)='conversation_report' THEN
   IF (to_jsonb(NEW)-'report'-'report_evidence_version') IS DISTINCT FROM (to_jsonb(OLD)-'report'-'report_evidence_version') THEN
     RAISE EXCEPTION 'AI report scope can only update report fields';
   END IF;
   v_id:=NULLIF(current_setting('tianqiong.task_id',TRUE),'')::BIGINT;
   v_owner:=NULLIF(current_setting('tianqiong.owner_user_id',TRUE),'')::INTEGER;
   v_run:=NULLIF(current_setting('tianqiong.execution_id',TRUE),'')::UUID;
   SELECT task.* INTO v_task FROM public.ai_task task
   JOIN public.tenant_membership m ON m.tenant_id=task.tenant_id AND m.user_id=task.owner_user_id
   JOIN public.auth_user u ON u.id=m.user_id JOIN public.tenant tenant ON tenant.id=m.tenant_id
   JOIN tianqiong_ai.exploration_nodes n ON n.id=OLD.node_id AND n.exploration_id=task.exploration_id
   WHERE task.id=v_id AND task.id=OLD.task_id AND task.owner_user_id=v_owner AND task.deleted_at IS NULL
   AND u.is_active=TRUE AND tenant.status='active' AND m.role IN ('admin','analyst')
   AND (task.original_target_id IS NULL OR EXISTS(SELECT 1 FROM public.target WHERE id=task.target_id AND deleted_at IS NULL))
   AND (EXISTS(SELECT 1 FROM public.ai_run r WHERE r.tenant_id=task.tenant_id AND r.owner_user_id=v_owner AND r.id=v_run AND r.status='running' AND r.lease_until>clock_timestamp())
        OR EXISTS(SELECT 1 FROM public.ai_task source WHERE source.tenant_id=task.tenant_id AND source.owner_user_id=v_owner AND source.active_run=v_run AND source.status='running' AND source.lease_until>clock_timestamp() AND source.deleted_at IS NULL)
        OR EXISTS(SELECT 1 FROM public.ai_task_main_chat_run c WHERE c.task_id=task.id AND c.tenant_id=task.tenant_id AND c.owner_user_id=v_owner AND c.run_id=v_run AND c.status='running' AND c.lease_until>clock_timestamp() AND tianqiong_ai.task_run_live(task.id,v_owner,v_run)))
   FOR SHARE OF task,m,u;
   IF NOT FOUND THEN RAISE EXCEPTION 'AI report execution scope rejected'; END IF;
   RETURN NEW;
 END IF;
 IF TG_OP='DELETE' THEN v_node:=OLD.node_id; ELSE v_node:=NEW.node_id; END IF;
 SELECT exploration_id INTO v_exp FROM tianqiong_ai.exploration_nodes WHERE id=v_node;
 v_task:=tianqiong_ai.assert_task_write(v_exp,NULL);
 IF TG_OP<>'DELETE' AND NEW.task_id<>v_task.id THEN RAISE EXCEPTION 'AI finding crosses task boundary'; END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;

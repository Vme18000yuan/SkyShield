CREATE TABLE ai_tool_mutation_receipt (
 tenant_id UUID NOT NULL,
 owner_user_id INTEGER NOT NULL,
 execution_id UUID NOT NULL,
 tool_use_id TEXT NOT NULL CHECK(octet_length(tool_use_id) BETWEEN 1 AND 256),
 operation TEXT NOT NULL,
 request_hash TEXT NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),
 result JSONB NOT NULL DEFAULT '{}',
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES tenant_membership(tenant_id,user_id) ON DELETE CASCADE,
 PRIMARY KEY(tenant_id,execution_id,tool_use_id)
);

-- A reporter conversation can update the report of an owned finding even
-- after the source task has finished. Other graph writes retain the task fence.
CREATE FUNCTION tianqiong_ai.guard_finding_write() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_task public.ai_task%ROWTYPE;v_exp BIGINT;v_node BIGINT;v_id BIGINT;v_owner INTEGER;v_run UUID;
BEGIN
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
        OR EXISTS(SELECT 1 FROM public.ai_task source WHERE source.tenant_id=task.tenant_id AND source.owner_user_id=v_owner AND source.active_run=v_run AND source.status='running' AND source.lease_until>clock_timestamp() AND source.deleted_at IS NULL))
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
DROP TRIGGER tq_graph_finding_fence ON tianqiong_ai.findings;
CREATE TRIGGER tq_graph_finding_fence BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.findings FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_finding_write();

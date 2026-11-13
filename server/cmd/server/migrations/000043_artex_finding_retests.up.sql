CREATE TABLE ai_finding_retest (
 id BIGSERIAL PRIMARY KEY,
 tenant_id UUID NOT NULL,
 owner_user_id INTEGER NOT NULL,
 finding_id BIGINT NOT NULL REFERENCES tianqiong_ai.findings(id) ON DELETE CASCADE,
 conversation_id UUID REFERENCES ai_conversation(id) ON DELETE SET NULL,
 run_id UUID REFERENCES ai_run(id) ON DELETE SET NULL,
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','running','completed','failed','stopped')),
 verdict TEXT NOT NULL DEFAULT '' CHECK(verdict IN ('','reproduced','fixed','inconclusive')),
 notes TEXT NOT NULL DEFAULT '',
 snapshot JSONB NOT NULL,
 summary TEXT NOT NULL DEFAULT '',
 evidence TEXT NOT NULL DEFAULT '',
 error TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 started_at TIMESTAMPTZ,
 finished_at TIMESTAMPTZ,
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES tenant_membership(tenant_id,user_id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX ai_finding_retest_active ON ai_finding_retest(finding_id) WHERE status IN ('pending','running');
CREATE UNIQUE INDEX ai_finding_retest_conversation ON ai_finding_retest(conversation_id) WHERE conversation_id IS NOT NULL;
CREATE INDEX ai_finding_retest_history ON ai_finding_retest(tenant_id,owner_user_id,finding_id,id DESC);

CREATE OR REPLACE FUNCTION tianqiong_ai.guard_finding_write() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_task public.ai_task%ROWTYPE;v_exp BIGINT;v_node BIGINT;v_id BIGINT;v_owner INTEGER;v_run UUID;
BEGIN
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

CREATE FUNCTION tq_begin_finding_retest() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE ai_finding_retest SET status='running',run_id=NEW.id,started_at=CURRENT_TIMESTAMP
 WHERE conversation_id=NEW.conversation_id AND tenant_id=NEW.tenant_id AND owner_user_id=NEW.owner_user_id AND status='pending';
 RETURN NEW;
END;
$$;
CREATE TRIGGER tq_begin_finding_retest AFTER INSERT ON ai_run FOR EACH ROW EXECUTE FUNCTION tq_begin_finding_retest();

CREATE FUNCTION tq_finish_finding_retest() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE f_id BIGINT;t_id BIGINT;v_status TEXT;v_verdict TEXT;v_summary TEXT;v_safe BOOLEAN;
BEGIN
 IF OLD.status<>'running' OR NEW.status='running' THEN RETURN NEW; END IF;
 SELECT f.id,f.task_id INTO f_id,t_id FROM tianqiong_ai.findings f JOIN ai_finding_retest r ON r.finding_id=f.id
 WHERE r.run_id=NEW.id AND r.status='running' FOR UPDATE OF f;
 IF NOT FOUND THEN RETURN NEW; END IF;
 SELECT EXISTS(SELECT 1 FROM ai_task t JOIN tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id
 JOIN auth_user u ON u.id=m.user_id JOIN tenant tenant ON tenant.id=m.tenant_id
 WHERE t.id=t_id AND t.tenant_id=NEW.tenant_id AND t.owner_user_id=NEW.owner_user_id AND t.deleted_at IS NULL
 AND m.role IN ('admin','analyst') AND u.is_active=TRUE AND tenant.status='active'
 AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=t.target_id AND deleted_at IS NULL))) INTO v_safe;
 UPDATE ai_finding_retest SET
 status=CASE WHEN NOT v_safe OR (NEW.status='completed' AND verdict='') THEN 'failed'
             WHEN NEW.status='completed' THEN 'completed' WHEN NEW.status IN ('stopped','interrupted') THEN 'stopped' ELSE 'failed' END,
 error=CASE WHEN NOT v_safe THEN '复测来源或账号已不可用' WHEN NEW.status='completed' AND verdict='' THEN 'Agent 未保存复测结论，请查看会话后重新复测' ELSE NEW.error_message END,
 finished_at=CURRENT_TIMESTAMP WHERE run_id=NEW.id AND status='running' RETURNING status,verdict,summary INTO v_status,v_verdict,v_summary;
 IF v_status='completed' AND v_verdict='fixed' THEN
  PERFORM set_config('tianqiong.task_id',t_id::text,TRUE),set_config('tianqiong.owner_user_id',NEW.owner_user_id::text,TRUE),set_config('tianqiong.execution_id',NEW.id::text,TRUE),set_config('tianqiong.execution_mode','conversation_retest',TRUE);
  UPDATE tianqiong_ai.findings SET status='fixed' WHERE id=f_id;
 END IF;
 IF v_status='completed' THEN
  UPDATE vulnerability v SET raw_output=jsonb_set(CASE WHEN jsonb_typeof(v.raw_output)='object' THEN v.raw_output ELSE jsonb_build_object('original',v.raw_output) END,'{aiRetests}',COALESCE(CASE WHEN jsonb_typeof(v.raw_output->'aiRetests')='object' THEN v.raw_output->'aiRetests' END,'{}'::jsonb)||jsonb_build_object(f_id::text,jsonb_build_object('verdict',v_verdict,'summary',v_summary,'finishedAt',CURRENT_TIMESTAMP)),TRUE)
  FROM ai_finding_risk_link l WHERE l.vulnerability_id=v.id AND l.finding_id=f_id;
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER tq_finish_finding_retest AFTER UPDATE OF status ON ai_run FOR EACH ROW EXECUTE FUNCTION tq_finish_finding_retest();

CREATE FUNCTION tq_cancel_deleted_retest_conversation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE ai_finding_retest SET status='stopped',error='复测会话已删除',finished_at=CURRENT_TIMESTAMP WHERE conversation_id=OLD.id AND status IN ('pending','running');
 UPDATE ai_agent_trigger_job SET status='cancelled',error_message='复测会话已删除',updated_at=CURRENT_TIMESTAMP WHERE conversation_id=OLD.id AND event_key LIKE 'retest:%' AND status IN ('queued','starting','running');
 RETURN OLD;
END;
$$;
CREATE TRIGGER tq_cancel_deleted_retest_conversation BEFORE DELETE ON ai_conversation FOR EACH ROW EXECUTE FUNCTION tq_cancel_deleted_retest_conversation();

-- Historical finding review is a durable, task-scoped Reporter job.  The
-- selected IDs and completed conclusions are immutable audit records.
CREATE TABLE public.ai_finding_case_review (
 id BIGSERIAL PRIMARY KEY,
 tenant_id UUID NOT NULL,
 owner_user_id INTEGER NOT NULL,
 task_id BIGINT REFERENCES public.ai_task(id) ON DELETE SET NULL,
 origin_task_id BIGINT NOT NULL,
 conversation_id UUID REFERENCES public.ai_conversation(id) ON DELETE SET NULL,
 job_id BIGINT REFERENCES public.ai_agent_trigger_job(id) ON DELETE SET NULL,
 run_id UUID REFERENCES public.ai_run(id) ON DELETE SET NULL,
 finding_ids JSONB NOT NULL CHECK(jsonb_typeof(finding_ids)='array' AND jsonb_array_length(finding_ids) BETWEEN 1 AND 200),
 reviewed_ids JSONB NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(reviewed_ids)='array'),
 reviewed_fingerprints JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(reviewed_fingerprints)='object'),
 conclusion JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(conclusion)='object'),
 state TEXT NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','running','completed','failed','stopped')),
 error TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 started_at TIMESTAMPTZ,
 finished_at TIMESTAMPTZ,
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES public.tenant_membership(tenant_id,user_id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX ai_finding_case_review_active ON public.ai_finding_case_review(task_id) WHERE state IN ('queued','running');
CREATE UNIQUE INDEX ai_finding_case_review_conversation ON public.ai_finding_case_review(conversation_id) WHERE conversation_id IS NOT NULL;
CREATE UNIQUE INDEX ai_finding_case_review_job ON public.ai_finding_case_review(job_id) WHERE job_id IS NOT NULL;
CREATE INDEX ai_finding_case_review_owner ON public.ai_finding_case_review(tenant_id,owner_user_id,task_id,id DESC);

CREATE FUNCTION tianqiong_ai.guard_finding_case_review() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_task public.ai_task%ROWTYPE; v_exp BIGINT; v_count INTEGER; v_distinct INTEGER; v_mode TEXT;
BEGIN
 IF TG_OP='DELETE' AND pg_trigger_depth()>1 THEN RETURN OLD; END IF;
 IF TG_OP='UPDATE' AND pg_trigger_depth()>1 AND
    (to_jsonb(NEW)-'state'-'task_id'-'run_id'-'conversation_id'-'job_id'-'error'-'started_at'-'finished_at') IS NOT DISTINCT FROM
    (to_jsonb(OLD)-'state'-'task_id'-'run_id'-'conversation_id'-'job_id'-'error'-'started_at'-'finished_at') AND
    (NEW.task_id IS NOT DISTINCT FROM OLD.task_id OR (OLD.task_id IS NOT NULL AND NEW.task_id IS NULL)) AND
    (NEW.state=OLD.state OR (OLD.state='queued' AND NEW.state IN ('running','failed','stopped')) OR (OLD.state='running' AND NEW.state IN ('completed','failed','stopped'))) THEN
  RETURN NEW;
 END IF;
 v_mode:=current_setting('tianqiong.execution_mode',TRUE);
 IF TG_OP='INSERT' AND v_mode='human_finding_case_review' THEN
  SELECT exploration_id INTO v_exp FROM public.ai_task WHERE id=NEW.task_id;
  v_task:=tianqiong_ai.assert_human_goal_scope(v_exp);
  IF NEW.task_id IS DISTINCT FROM v_task.id OR NEW.origin_task_id IS DISTINCT FROM v_task.id OR NEW.tenant_id IS DISTINCT FROM v_task.tenant_id OR NEW.owner_user_id IS DISTINCT FROM v_task.owner_user_id OR
     NEW.state<>'queued' OR NEW.run_id IS NOT NULL OR NEW.conclusion<>'{}'::jsonb OR NEW.reviewed_ids<>'[]'::jsonb OR NEW.reviewed_fingerprints<>'{}'::jsonb OR
     NOT EXISTS(SELECT 1 FROM public.ai_agent_trigger_job j WHERE j.id=NEW.job_id AND j.tenant_id=NEW.tenant_id AND j.owner_user_id=NEW.owner_user_id AND j.source_task_id=NEW.task_id AND j.conversation_id=NEW.conversation_id AND j.agent_key='reporter' AND j.status='queued') OR
     NOT EXISTS(SELECT 1 FROM public.ai_conversation c WHERE c.id=NEW.conversation_id AND c.tenant_id=NEW.tenant_id AND c.owner_user_id=NEW.owner_user_id AND c.agent_key='reporter') OR
     EXISTS(SELECT 1 FROM public.ai_task_archive a WHERE a.task_id=v_task.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting')) THEN
   RAISE EXCEPTION 'AI finding case review creation scope rejected';
  END IF;
  IF EXISTS(SELECT 1 FROM jsonb_array_elements(NEW.finding_ids) x WHERE jsonb_typeof(x.value)<>'number' OR x.value::text !~ '^[1-9][0-9]{0,18}$') THEN RAISE EXCEPTION 'AI finding case review IDs rejected'; END IF;
  SELECT count(*),count(DISTINCT value) INTO v_count,v_distinct FROM jsonb_array_elements_text(NEW.finding_ids);
  IF v_count<>v_distinct OR EXISTS(SELECT 1 FROM jsonb_array_elements_text(NEW.finding_ids) x WHERE NOT EXISTS(SELECT 1 FROM tianqiong_ai.findings f WHERE f.id=x.value::bigint AND f.task_id=v_task.id)) THEN
   RAISE EXCEPTION 'AI finding case review crosses task boundary';
  END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='UPDATE' AND v_mode='reporter_finding_case' THEN
  v_task:=tianqiong_ai.assert_finding_case_scope();
  IF OLD.task_id IS DISTINCT FROM v_task.id OR OLD.state<>'running' OR OLD.run_id IS DISTINCT FROM NULLIF(current_setting('tianqiong.execution_id',TRUE),'')::uuid OR
     (to_jsonb(NEW)-'reviewed_ids'-'reviewed_fingerprints'-'conclusion') IS DISTINCT FROM (to_jsonb(OLD)-'reviewed_ids'-'reviewed_fingerprints'-'conclusion') OR
     NOT (NEW.reviewed_ids @> OLD.reviewed_ids) OR NOT (OLD.finding_ids @> NEW.reviewed_ids) OR
     (OLD.conclusion<>'{}'::jsonb AND (NEW.conclusion IS DISTINCT FROM OLD.conclusion OR NEW.reviewed_fingerprints IS DISTINCT FROM OLD.reviewed_fingerprints)) THEN
   RAISE EXCEPTION 'AI finding case review update rejected';
  END IF;
  IF EXISTS(SELECT 1 FROM jsonb_each(NEW.reviewed_fingerprints) x WHERE x.key !~ '^[1-9][0-9]{0,18}$' OR jsonb_typeof(x.value)<>'string' OR (x.value#>>'{}') !~ '^[0-9a-f]{64}$' OR NOT (OLD.finding_ids @> jsonb_build_array(x.key::bigint))) THEN
   RAISE EXCEPTION 'AI finding case review fingerprints rejected';
  END IF;
  SELECT count(*),count(DISTINCT value) INTO v_count,v_distinct FROM jsonb_array_elements_text(NEW.reviewed_ids);
  IF v_count<>v_distinct THEN RAISE EXCEPTION 'AI finding case review reads duplicated'; END IF;
  RETURN NEW;
 END IF;
 RAISE EXCEPTION 'AI finding case review mutation rejected';
END $$;
CREATE TRIGGER ai_finding_case_review_fence BEFORE INSERT OR UPDATE OR DELETE ON public.ai_finding_case_review
 FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_finding_case_review();

CREATE FUNCTION tianqiong_ai.finding_case_review_conversation_visible(p_conversation UUID) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
 SELECT NOT EXISTS(SELECT 1 FROM public.ai_finding_case_review r LEFT JOIN public.ai_task t ON t.id=r.task_id AND t.tenant_id=r.tenant_id AND t.owner_user_id=r.owner_user_id
  WHERE r.conversation_id=p_conversation AND (t.id IS NULL OR t.deleted_at IS NOT NULL OR
   (t.original_target_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM public.target x WHERE x.id=t.target_id AND x.deleted_at IS NULL)) OR
   EXISTS(SELECT 1 FROM public.ai_task_archive a WHERE a.task_id=t.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting'))))
$$;

CREATE FUNCTION tianqiong_ai.cancel_deleted_finding_case_review_task() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE public.ai_finding_case_review SET state='stopped',error='审查任务已删除',finished_at=now() WHERE task_id=OLD.id AND state IN ('queued','running');
 UPDATE public.ai_agent_trigger_job SET status='cancelled',error_message='审查任务已删除',updated_at=now() WHERE source_task_id=OLD.id AND event_key LIKE 'finding-case-review:%' AND status IN ('queued','starting','running');
 RETURN OLD;
END $$;
CREATE TRIGGER ai_cancel_deleted_finding_case_review_task BEFORE DELETE ON public.ai_task FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.cancel_deleted_finding_case_review_task();

-- For a historical review, group and suggestion mutations must stay inside
-- the frozen selection and stop once a conclusion has been submitted.
CREATE FUNCTION tianqiong_ai.guard_finding_case_review_selection() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r public.ai_finding_case_review%ROWTYPE; v_run UUID; v_case BIGINT; v_suggestion BIGINT; v_finding BIGINT;
BEGIN
 IF current_setting('tianqiong.execution_mode',TRUE) IS DISTINCT FROM 'reporter_finding_case' THEN
  IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
 END IF;
 v_run:=NULLIF(current_setting('tianqiong.execution_id',TRUE),'')::uuid;
 SELECT * INTO r FROM public.ai_finding_case_review WHERE run_id=v_run;
 IF NOT FOUND THEN IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW; END IF;
 IF r.state<>'running' OR r.conclusion<>'{}'::jsonb THEN RAISE EXCEPTION 'AI finding case review is no longer writable'; END IF;
 IF TG_TABLE_NAME='finding_cases' THEN
  IF TG_OP='INSERT' THEN v_case:=NEW.id; ELSE v_case:=OLD.id; END IF;
 ELSIF TG_TABLE_NAME='finding_case_members' THEN
  IF TG_OP='DELETE' THEN v_finding:=OLD.finding_id;v_case:=OLD.case_id; ELSE v_finding:=NEW.finding_id;v_case:=NEW.case_id; END IF;
 ELSIF TG_TABLE_NAME='finding_case_events' THEN
  IF TG_OP='DELETE' THEN v_case:=OLD.case_id; ELSE v_case:=NEW.case_id; END IF;
 ELSIF TG_TABLE_NAME='finding_case_suggestions' THEN
  IF TG_OP='INSERT' THEN v_suggestion:=NEW.id; ELSE v_suggestion:=OLD.id; END IF;
 ELSIF TG_TABLE_NAME='finding_case_suggestion_members' THEN
  IF TG_OP='DELETE' THEN v_finding:=OLD.finding_id;v_suggestion:=OLD.suggestion_id; ELSE v_finding:=NEW.finding_id;v_suggestion:=NEW.suggestion_id; END IF;
 END IF;
 IF v_finding IS NOT NULL AND NOT (r.finding_ids @> jsonb_build_array(v_finding)) THEN RAISE EXCEPTION 'AI finding case review selected scope rejected'; END IF;
 IF v_case IS NOT NULL AND EXISTS(SELECT 1 FROM tianqiong_ai.finding_case_members m WHERE m.case_id=v_case AND NOT (r.finding_ids @> jsonb_build_array(m.finding_id))) THEN
  RAISE EXCEPTION 'AI finding case review group contains unselected findings';
 END IF;
 IF v_suggestion IS NOT NULL AND EXISTS(SELECT 1 FROM tianqiong_ai.finding_case_suggestion_members m WHERE m.suggestion_id=v_suggestion AND NOT (r.finding_ids @> jsonb_build_array(m.finding_id))) THEN
  RAISE EXCEPTION 'AI finding case review suggestion contains unselected findings';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
END $$;
CREATE TRIGGER ai_review_finding_cases BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.finding_cases FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_finding_case_review_selection();
CREATE TRIGGER ai_review_finding_case_members BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.finding_case_members FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_finding_case_review_selection();
CREATE TRIGGER ai_review_finding_case_events BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.finding_case_events FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_finding_case_review_selection();
CREATE TRIGGER ai_review_finding_case_suggestions BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.finding_case_suggestions FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_finding_case_review_selection();
CREATE TRIGGER ai_review_finding_case_suggestion_members BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.finding_case_suggestion_members FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_finding_case_review_selection();

CREATE FUNCTION tianqiong_ai.finding_case_review_suggestion_allowed(p_suggestion BIGINT) RETURNS BOOLEAN LANGUAGE plpgsql AS $$
DECLARE r public.ai_finding_case_review%ROWTYPE; v_run UUID;
BEGIN
 IF current_setting('tianqiong.execution_mode',TRUE) IS DISTINCT FROM 'reporter_finding_case' THEN RETURN TRUE; END IF;
 v_run:=NULLIF(current_setting('tianqiong.execution_id',TRUE),'')::uuid;
 SELECT * INTO r FROM public.ai_finding_case_review WHERE run_id=v_run;
 IF NOT FOUND THEN RETURN TRUE; END IF;
 RETURN r.state='running' AND r.conclusion='{}'::jsonb AND NOT EXISTS(
  SELECT 1 FROM tianqiong_ai.finding_case_suggestion_members m WHERE m.suggestion_id=p_suggestion AND NOT (r.finding_ids @> jsonb_build_array(m.finding_id)));
END $$;

CREATE FUNCTION tianqiong_ai.guard_review_original_finding() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_run UUID;
BEGIN
 IF current_setting('tianqiong.execution_mode',TRUE) IN ('conversation_report','reporter_evidence') THEN
  v_run:=NULLIF(current_setting('tianqiong.execution_id',TRUE),'')::uuid;
  IF EXISTS(SELECT 1 FROM public.ai_finding_case_review WHERE run_id=v_run) THEN RAISE EXCEPTION 'AI finding case review cannot change original findings'; END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER ai_review_original_finding BEFORE UPDATE ON tianqiong_ai.findings FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_review_original_finding();

-- Review conclusions must still match current findings at the moment the run
-- completes.  A model ending without accepted per-finding decisions is failed.
CREATE FUNCTION tianqiong_ai.finding_case_review_valid(p_id BIGINT) RETURNS BOOLEAN LANGUAGE plpgsql AS $$
DECLARE r public.ai_finding_case_review%ROWTYPE; v_id BIGINT; decision JSONB; verdict TEXT; v_count INTEGER; v_case BIGINT;
BEGIN
 SELECT * INTO r FROM public.ai_finding_case_review WHERE id=p_id;
 IF NOT FOUND OR r.state<>'running' OR jsonb_typeof(r.conclusion->'summary') IS DISTINCT FROM 'string' OR btrim(r.conclusion->>'summary')='' OR
    jsonb_typeof(r.conclusion->'decisions') IS DISTINCT FROM 'array' THEN RETURN FALSE; END IF;
 IF jsonb_array_length(r.conclusion->'decisions')<>jsonb_array_length(r.finding_ids) OR
    NOT EXISTS(SELECT 1 FROM public.ai_task t JOIN public.tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id JOIN public.auth_user u ON u.id=m.user_id JOIN public.tenant tenant ON tenant.id=m.tenant_id
      WHERE t.id=r.task_id AND t.tenant_id=r.tenant_id AND t.owner_user_id=r.owner_user_id AND t.deleted_at IS NULL AND tenant.status='active' AND u.is_active AND m.role IN ('admin','analyst')
      AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM public.target x WHERE x.id=t.target_id AND x.deleted_at IS NULL))
      AND NOT EXISTS(SELECT 1 FROM public.ai_task_archive a WHERE a.task_id=t.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting'))) THEN RETURN FALSE; END IF;
 FOR v_id IN SELECT value::bigint FROM jsonb_array_elements_text(r.finding_ids) LOOP
  IF NOT (r.reviewed_ids @> jsonb_build_array(v_id)) OR NOT EXISTS(SELECT 1 FROM tianqiong_ai.findings f WHERE f.id=v_id AND f.task_id=r.task_id AND encode(sha256(convert_to(to_jsonb(f)::text,'UTF8')),'hex')=r.reviewed_fingerprints->>v_id::text) THEN RETURN FALSE; END IF;
  SELECT count(*),min(value::text)::jsonb INTO v_count,decision FROM jsonb_array_elements(r.conclusion->'decisions') WHERE value->>'finding_id'=v_id::text;
  IF v_count<>1 OR jsonb_typeof(decision->'reason') IS DISTINCT FROM 'string' OR btrim(decision->>'reason')='' OR jsonb_typeof(decision->'root_cause_evidence') IS DISTINCT FROM 'string' OR btrim(decision->>'root_cause_evidence')='' THEN RETURN FALSE; END IF;
  verdict:=decision->>'verdict';
  SELECT c.id INTO v_case FROM tianqiong_ai.finding_case_members m JOIN tianqiong_ai.finding_cases c ON c.id=m.case_id AND c.active AND c.task_id=r.task_id WHERE m.finding_id=v_id;
  IF v_case IS NOT NULL AND EXISTS(SELECT 1 FROM tianqiong_ai.finding_case_members m WHERE m.case_id=v_case AND NOT (r.finding_ids @> jsonb_build_array(m.finding_id))) THEN RETURN FALSE; END IF;
  IF verdict='merged' AND v_case IS NULL THEN RETURN FALSE; END IF;
  IF verdict='independent' AND v_case IS NOT NULL THEN RETURN FALSE; END IF;
  IF verdict='suggested' AND NOT EXISTS(SELECT 1 FROM tianqiong_ai.finding_case_suggestions s JOIN tianqiong_ai.finding_case_suggestion_members m ON m.suggestion_id=s.id WHERE s.task_id=r.task_id AND s.state='pending' AND m.finding_id=v_id AND NOT EXISTS(SELECT 1 FROM tianqiong_ai.finding_case_suggestion_members other WHERE other.suggestion_id=s.id AND NOT (r.finding_ids @> jsonb_build_array(other.finding_id)))) THEN RETURN FALSE; END IF;
  IF verdict NOT IN ('merged','independent','suggested') OR verdict IS NULL THEN RETURN FALSE; END IF;
 END LOOP;
 RETURN TRUE;
EXCEPTION WHEN data_exception THEN RETURN FALSE;
END $$;

CREATE FUNCTION tianqiong_ai.begin_finding_case_review() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM public.ai_finding_case_review WHERE conversation_id=NEW.conversation_id AND state='queued') THEN
  UPDATE public.ai_finding_case_review r SET state='running',run_id=NEW.id,started_at=now()
  FROM public.ai_agent_trigger_job j WHERE r.conversation_id=NEW.conversation_id AND r.tenant_id=NEW.tenant_id AND r.owner_user_id=NEW.owner_user_id AND r.state='queued'
   AND j.id=r.job_id AND j.id=NULLIF(current_setting('tianqiong.trigger_job_id',TRUE),'')::bigint AND j.status='starting' AND j.conversation_id=NEW.conversation_id;
  IF NOT FOUND THEN RAISE EXCEPTION 'AI finding case review dispatcher rejected'; END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER ai_begin_finding_case_review AFTER INSERT ON public.ai_run FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.begin_finding_case_review();

CREATE FUNCTION tianqiong_ai.validate_finishing_finding_case_review() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_id BIGINT;
BEGIN
 IF OLD.status='running' AND NEW.status='completed' THEN
  SELECT id INTO v_id FROM public.ai_finding_case_review WHERE run_id=NEW.id AND state='running';
  IF FOUND AND NOT tianqiong_ai.finding_case_review_valid(v_id) THEN
   NEW.status:='failed';
   NEW.error_message:='审查未通过验收：结论缺失或发现状态已变化';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER ai_validate_finishing_finding_case_review BEFORE UPDATE OF status ON public.ai_run FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.validate_finishing_finding_case_review();

CREATE FUNCTION tianqiong_ai.finish_finding_case_review() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_id BIGINT; v_valid BOOLEAN;
BEGIN
 IF OLD.status<>'running' OR NEW.status='running' THEN RETURN NEW; END IF;
 SELECT id INTO v_id FROM public.ai_finding_case_review WHERE run_id=NEW.id AND state='running';
 IF NOT FOUND THEN RETURN NEW; END IF;
 v_valid:=NEW.status='completed' AND tianqiong_ai.finding_case_review_valid(v_id);
 UPDATE public.ai_finding_case_review SET
  state=CASE WHEN v_valid THEN 'completed' WHEN NEW.status IN ('stopped','interrupted') THEN 'stopped' ELSE 'failed' END,
  error=CASE WHEN NEW.status='completed' AND NOT v_valid THEN '审查未通过验收：结论缺失或发现状态已变化' ELSE NEW.error_message END,
  finished_at=now() WHERE id=v_id AND state='running';
 RETURN NEW;
END $$;
CREATE TRIGGER ai_finish_finding_case_review AFTER UPDATE OF status ON public.ai_run FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.finish_finding_case_review();

CREATE FUNCTION tianqiong_ai.fail_queued_finding_case_review() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status IN ('failed','cancelled') THEN
  UPDATE public.ai_finding_case_review SET state=CASE WHEN NEW.status='cancelled' THEN 'stopped' ELSE 'failed' END,error=NEW.error_message,finished_at=now()
  WHERE job_id=NEW.id AND state='queued';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER ai_fail_queued_finding_case_review AFTER UPDATE OF status ON public.ai_agent_trigger_job FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.fail_queued_finding_case_review();

CREATE FUNCTION tianqiong_ai.cancel_deleted_finding_case_review() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE public.ai_finding_case_review SET state='stopped',error='审查会话已删除',finished_at=now() WHERE conversation_id=OLD.id AND state IN ('queued','running');
 UPDATE public.ai_agent_trigger_job SET status='cancelled',error_message='审查会话已删除',updated_at=now() WHERE conversation_id=OLD.id AND event_key LIKE 'finding-case-review:%' AND status IN ('queued','starting','running');
 RETURN OLD;
END $$;
CREATE TRIGGER ai_cancel_deleted_finding_case_review BEFORE DELETE ON public.ai_conversation FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.cancel_deleted_finding_case_review();

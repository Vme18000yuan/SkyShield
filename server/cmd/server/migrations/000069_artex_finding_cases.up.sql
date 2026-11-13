-- ARTEX finding groups retain individual findings and their evidence.  The
-- task remains the ownership boundary; orphaned history is never listed.
CREATE TABLE tianqiong_ai.finding_cases (
 id BIGSERIAL PRIMARY KEY,
 task_id BIGINT REFERENCES public.ai_task(id) ON DELETE SET NULL,
 origin_task_id BIGINT NOT NULL,
 tenant_id UUID NOT NULL,
 owner_user_id INTEGER NOT NULL,
 title TEXT NOT NULL,
 reason TEXT NOT NULL,
 severity TEXT NOT NULL DEFAULT '',
 severity_reason TEXT NOT NULL DEFAULT '',
 report TEXT NOT NULL DEFAULT '',
 version BIGINT NOT NULL DEFAULT 1 CHECK(version > 0),
 report_version BIGINT NOT NULL DEFAULT 0 CHECK(report_version >= 0),
 active BOOLEAN NOT NULL DEFAULT TRUE,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ai_finding_case_task ON tianqiong_ai.finding_cases(task_id,id DESC) WHERE active;

CREATE TABLE tianqiong_ai.finding_case_members (
 finding_id BIGINT PRIMARY KEY REFERENCES tianqiong_ai.findings(id) ON DELETE CASCADE,
 case_id BIGINT NOT NULL REFERENCES tianqiong_ai.finding_cases(id) ON DELETE CASCADE,
 joined_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ai_finding_case_members_case ON tianqiong_ai.finding_case_members(case_id,finding_id);

CREATE TABLE tianqiong_ai.finding_case_events (
 id BIGSERIAL PRIMARY KEY,
 case_id BIGINT NOT NULL REFERENCES tianqiong_ai.finding_cases(id) ON DELETE CASCADE,
 action TEXT NOT NULL,
 actor TEXT NOT NULL,
 reason TEXT NOT NULL,
 finding_ids JSONB NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(finding_ids)='array'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ai_finding_case_events_case ON tianqiong_ai.finding_case_events(case_id,id DESC);

CREATE TABLE tianqiong_ai.finding_case_blocks (
 left_id BIGINT NOT NULL REFERENCES tianqiong_ai.findings(id) ON DELETE CASCADE,
 right_id BIGINT NOT NULL REFERENCES tianqiong_ai.findings(id) ON DELETE CASCADE,
 reason TEXT NOT NULL,
 PRIMARY KEY(left_id,right_id), CHECK(left_id < right_id)
);

CREATE TABLE tianqiong_ai.finding_case_suggestions (
 id BIGSERIAL PRIMARY KEY,
 task_id BIGINT NOT NULL REFERENCES public.ai_task(id) ON DELETE CASCADE,
 tenant_id UUID NOT NULL,
 owner_user_id INTEGER NOT NULL,
 title TEXT NOT NULL,
 reason TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','accepted','rejected','superseded','obsolete')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ai_finding_case_suggestions_pending ON tianqiong_ai.finding_case_suggestions(task_id,id DESC) WHERE state='pending';
CREATE TABLE tianqiong_ai.finding_case_suggestion_members (
 suggestion_id BIGINT NOT NULL REFERENCES tianqiong_ai.finding_case_suggestions(id) ON DELETE CASCADE,
 finding_id BIGINT NOT NULL REFERENCES tianqiong_ai.findings(id) ON DELETE CASCADE,
 PRIMARY KEY(suggestion_id,finding_id)
);
CREATE INDEX ai_finding_case_suggestion_members_finding ON tianqiong_ai.finding_case_suggestion_members(finding_id,suggestion_id);

CREATE FUNCTION tianqiong_ai.assert_finding_case_scope() RETURNS public.ai_task LANGUAGE plpgsql AS $$
DECLARE v_exp BIGINT; v_task public.ai_task%ROWTYPE; v_mode TEXT; v_id BIGINT; v_owner INTEGER; v_run UUID;
BEGIN
 v_mode:=current_setting('tianqiong.execution_mode',TRUE);
 v_id:=NULLIF(current_setting('tianqiong.task_id',TRUE),'')::bigint;
 v_owner:=NULLIF(current_setting('tianqiong.owner_user_id',TRUE),'')::integer;
 IF v_mode='human_finding_case' THEN
  SELECT exploration_id INTO v_exp FROM public.ai_task WHERE id=v_id;
  v_task:=tianqiong_ai.assert_human_goal_scope(v_exp);
 ELSIF v_mode='reporter_finding_case' THEN
  v_run:=NULLIF(current_setting('tianqiong.execution_id',TRUE),'')::uuid;
  SELECT t.* INTO v_task FROM public.ai_task t
  JOIN public.ai_agent_trigger_job j ON j.source_task_id=t.id AND j.tenant_id=t.tenant_id AND j.owner_user_id=t.owner_user_id
  JOIN public.ai_run r ON r.id=j.run_id AND r.tenant_id=j.tenant_id AND r.owner_user_id=j.owner_user_id AND r.conversation_id=j.conversation_id
  JOIN public.tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id
  JOIN public.auth_user u ON u.id=m.user_id JOIN public.tenant tenant ON tenant.id=m.tenant_id
  WHERE t.id=v_id AND t.owner_user_id=v_owner AND t.deleted_at IS NULL AND tenant.status='active' AND u.is_active AND m.role IN ('admin','analyst')
   AND j.agent_key='reporter' AND j.status='running' AND j.run_id=v_run AND r.status='running' AND r.lease_until>clock_timestamp()
   AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM public.target x WHERE x.id=t.target_id AND x.deleted_at IS NULL))
  FOR SHARE OF t,j,r,m,u;
  IF NOT FOUND THEN RAISE EXCEPTION 'AI Reporter finding case scope rejected'; END IF;
 ELSE
  RAISE EXCEPTION 'AI finding case mode rejected';
 END IF;
 IF EXISTS(SELECT 1 FROM public.ai_task_archive a WHERE a.task_id=v_task.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting')) THEN
  RAISE EXCEPTION 'AI finding case task is being archived';
 END IF;
 RETURN v_task;
END $$;

CREATE FUNCTION tianqiong_ai.guard_finding_case_write() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_task public.ai_task%ROWTYPE;
BEGIN
 -- Only internal triggers may advance a group's version or dissolve it when
 -- a previously authorized finding changes or a member is removed.
 IF TG_OP='UPDATE' AND pg_trigger_depth()>1 AND
    (to_jsonb(NEW)-'version'-'active') IS NOT DISTINCT FROM (to_jsonb(OLD)-'version'-'active') AND
    NEW.version BETWEEN OLD.version AND OLD.version+1 AND (OLD.active OR NOT NEW.active) THEN
  RETURN NEW;
 END IF;
 -- The task foreign key may detach retained case history during task deletion.
 IF TG_OP='UPDATE' AND pg_trigger_depth()>1 AND NEW.task_id IS NULL AND OLD.task_id IS NOT NULL AND
    (to_jsonb(NEW)-'task_id') IS NOT DISTINCT FROM (to_jsonb(OLD)-'task_id') THEN RETURN NEW; END IF;
 IF TG_OP='DELETE' AND pg_trigger_depth()>1 THEN RETURN OLD; END IF;
 v_task:=tianqiong_ai.assert_finding_case_scope();
 IF TG_OP='DELETE' OR NEW.task_id IS DISTINCT FROM v_task.id OR NEW.origin_task_id IS DISTINCT FROM v_task.id OR
    NEW.tenant_id IS DISTINCT FROM v_task.tenant_id OR NEW.owner_user_id IS DISTINCT FROM v_task.owner_user_id THEN
  RAISE EXCEPTION 'AI finding case crosses task boundary';
 END IF;
 IF TG_OP='UPDATE' AND (NEW.id IS DISTINCT FROM OLD.id OR NEW.task_id IS DISTINCT FROM OLD.task_id OR
    NEW.origin_task_id IS DISTINCT FROM OLD.origin_task_id OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR
    NEW.owner_user_id IS DISTINCT FROM OLD.owner_user_id OR NEW.created_at IS DISTINCT FROM OLD.created_at) THEN
  RAISE EXCEPTION 'AI finding case identity is immutable';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER ai_finding_case_fence BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.finding_cases
 FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_finding_case_write();

CREATE FUNCTION tianqiong_ai.guard_finding_case_member_write() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_task public.ai_task%ROWTYPE; v_case_task BIGINT; v_finding_task BIGINT;
BEGIN
 IF TG_OP='DELETE' AND pg_trigger_depth()>1 THEN RETURN OLD; END IF;
 v_task:=tianqiong_ai.assert_finding_case_scope();
 IF TG_OP='DELETE' THEN
  SELECT task_id INTO v_case_task FROM tianqiong_ai.finding_cases WHERE id=OLD.case_id;
  SELECT task_id INTO v_finding_task FROM tianqiong_ai.findings WHERE id=OLD.finding_id;
 ELSE
  SELECT task_id INTO v_case_task FROM tianqiong_ai.finding_cases WHERE id=NEW.case_id AND active;
  SELECT task_id INTO v_finding_task FROM tianqiong_ai.findings WHERE id=NEW.finding_id;
  IF TG_OP='UPDATE' AND NEW.finding_id IS DISTINCT FROM OLD.finding_id THEN RAISE EXCEPTION 'AI case member identity is immutable'; END IF;
  IF TG_OP='UPDATE' AND NOT EXISTS(SELECT 1 FROM tianqiong_ai.finding_cases WHERE id=OLD.case_id AND task_id=v_task.id AND active) THEN
   RAISE EXCEPTION 'AI finding case original member scope rejected';
  END IF;
 END IF;
 IF v_case_task IS DISTINCT FROM v_task.id OR v_finding_task IS DISTINCT FROM v_task.id THEN
  RAISE EXCEPTION 'AI finding case member crosses task boundary';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
END $$;
CREATE TRIGGER ai_finding_case_member_fence BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.finding_case_members
 FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_finding_case_member_write();

CREATE FUNCTION tianqiong_ai.guard_finding_case_event_write() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_task public.ai_task%ROWTYPE; v_case_task BIGINT;
BEGIN
 IF TG_OP='DELETE' AND pg_trigger_depth()>1 THEN RETURN OLD; END IF;
 IF TG_OP='INSERT' AND pg_trigger_depth()>1 AND NEW.actor='system' THEN RETURN NEW; END IF;
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'AI finding case events are immutable'; END IF;
 v_task:=tianqiong_ai.assert_finding_case_scope();
 SELECT task_id INTO v_case_task FROM tianqiong_ai.finding_cases WHERE id=NEW.case_id;
 IF v_case_task IS DISTINCT FROM v_task.id OR
    (current_setting('tianqiong.execution_mode',TRUE)='human_finding_case' AND NEW.actor<>'human') OR
    (current_setting('tianqiong.execution_mode',TRUE)='reporter_finding_case' AND NEW.actor<>'reporter') THEN RAISE EXCEPTION 'AI finding case event scope rejected'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER ai_finding_case_event_fence BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.finding_case_events
 FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_finding_case_event_write();

CREATE FUNCTION tianqiong_ai.guard_finding_case_block_write() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_task public.ai_task%ROWTYPE; v_left BIGINT; v_right BIGINT;
BEGIN
 IF TG_OP='DELETE' AND pg_trigger_depth()>1 THEN RETURN OLD; END IF;
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'AI finding case veto is immutable'; END IF;
 IF current_setting('tianqiong.execution_mode',TRUE) IS DISTINCT FROM 'human_finding_case' THEN RAISE EXCEPTION 'AI finding case veto requires a human'; END IF;
 v_task:=tianqiong_ai.assert_finding_case_scope();
 SELECT task_id INTO v_left FROM tianqiong_ai.findings WHERE id=NEW.left_id;
 SELECT task_id INTO v_right FROM tianqiong_ai.findings WHERE id=NEW.right_id;
 IF v_left IS DISTINCT FROM v_task.id OR v_right IS DISTINCT FROM v_task.id THEN RAISE EXCEPTION 'AI finding case veto crosses task boundary'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER ai_finding_case_block_fence BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.finding_case_blocks
 FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_finding_case_block_write();

CREATE FUNCTION tianqiong_ai.guard_finding_case_suggestion_write() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_task public.ai_task%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' AND pg_trigger_depth()>1 THEN RETURN OLD; END IF;
 IF TG_OP='UPDATE' AND pg_trigger_depth()>1 AND OLD.state='pending' AND NEW.state='obsolete' AND
    (to_jsonb(NEW)-'state'-'updated_at') IS NOT DISTINCT FROM (to_jsonb(OLD)-'state'-'updated_at') THEN RETURN NEW; END IF;
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'AI finding case suggestion deletion rejected'; END IF;
 v_task:=tianqiong_ai.assert_finding_case_scope();
 IF NEW.task_id IS DISTINCT FROM v_task.id OR NEW.tenant_id IS DISTINCT FROM v_task.tenant_id OR NEW.owner_user_id IS DISTINCT FROM v_task.owner_user_id THEN
  RAISE EXCEPTION 'AI finding case suggestion crosses task boundary';
 END IF;
 IF TG_OP='INSERT' AND NEW.state<>'pending' THEN RAISE EXCEPTION 'AI finding case suggestion must start pending'; END IF;
 IF TG_OP='UPDATE' AND (NEW.id IS DISTINCT FROM OLD.id OR NEW.task_id IS DISTINCT FROM OLD.task_id OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.owner_user_id IS DISTINCT FROM OLD.owner_user_id OR NEW.created_at IS DISTINCT FROM OLD.created_at OR OLD.state<>'pending') THEN
  RAISE EXCEPTION 'AI finding case suggestion identity or state rejected';
 END IF;
 IF current_setting('tianqiong.execution_mode',TRUE)='reporter_finding_case' AND NEW.state='rejected' THEN RAISE EXCEPTION 'AI suggestion rejection requires a human'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER ai_finding_case_suggestion_fence BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.finding_case_suggestions
 FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_finding_case_suggestion_write();

CREATE FUNCTION tianqiong_ai.guard_finding_case_suggestion_member_write() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_task public.ai_task%ROWTYPE; v_suggestion_task BIGINT; v_finding_task BIGINT;
BEGIN
 IF TG_OP='DELETE' AND pg_trigger_depth()>1 THEN RETURN OLD; END IF;
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'AI finding case suggestion member mutation rejected'; END IF;
 v_task:=tianqiong_ai.assert_finding_case_scope();
 SELECT task_id INTO v_suggestion_task FROM tianqiong_ai.finding_case_suggestions WHERE id=NEW.suggestion_id AND state='pending';
 SELECT task_id INTO v_finding_task FROM tianqiong_ai.findings WHERE id=NEW.finding_id;
 IF v_suggestion_task IS DISTINCT FROM v_task.id OR v_finding_task IS DISTINCT FROM v_task.id THEN
  RAISE EXCEPTION 'AI finding case suggestion member crosses task boundary';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER ai_finding_case_suggestion_member_fence BEFORE INSERT OR UPDATE OR DELETE ON tianqiong_ai.finding_case_suggestion_members
 FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_finding_case_suggestion_member_write();

CREATE FUNCTION tianqiong_ai.finding_case_suggestion_member_removed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE tianqiong_ai.finding_case_suggestions s SET state='obsolete',updated_at=now()
 WHERE s.id=OLD.suggestion_id AND s.state='pending' AND
  (SELECT count(*) FROM tianqiong_ai.finding_case_suggestion_members m WHERE m.suggestion_id=s.id)<2;
 RETURN OLD;
END $$;
CREATE TRIGGER ai_finding_case_suggestion_member_removed AFTER DELETE ON tianqiong_ai.finding_case_suggestion_members
 FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.finding_case_suggestion_member_removed();

CREATE FUNCTION tianqiong_ai.finding_case_finding_changed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD IS DISTINCT FROM NEW THEN
  UPDATE tianqiong_ai.finding_cases c SET version=c.version+1
  FROM tianqiong_ai.finding_case_members m WHERE m.finding_id=NEW.id AND m.case_id=c.id AND c.active;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER ai_finding_case_content_changed AFTER UPDATE ON tianqiong_ai.findings
 FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.finding_case_finding_changed();

CREATE FUNCTION tianqiong_ai.finding_case_member_removed() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_active BOOLEAN; v_remaining INTEGER;
BEGIN
 UPDATE tianqiong_ai.finding_cases SET version=version+1 WHERE id=OLD.case_id RETURNING active INTO v_active;
 IF NOT FOUND THEN RETURN OLD; END IF;
 INSERT INTO tianqiong_ai.finding_case_events(case_id,action,actor,reason,finding_ids)
 VALUES(OLD.case_id,'member_deleted','system','成员移出或删除',jsonb_build_array(OLD.finding_id));
 IF v_active THEN
  SELECT count(*) INTO v_remaining FROM tianqiong_ai.finding_case_members WHERE case_id=OLD.case_id;
  IF v_remaining<2 THEN
   UPDATE tianqiong_ai.finding_cases SET active=FALSE WHERE id=OLD.case_id;
   DELETE FROM tianqiong_ai.finding_case_members WHERE case_id=OLD.case_id;
  END IF;
 END IF;
 RETURN OLD;
END $$;
CREATE TRIGGER ai_finding_case_member_removed AFTER DELETE ON tianqiong_ai.finding_case_members
 FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.finding_case_member_removed();

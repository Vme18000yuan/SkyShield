-- File cleanup survives a committed deletion and is retried after a restart.
CREATE TABLE public.ai_storage_cleanup (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    owner_user_id integer NOT NULL CHECK (owner_user_id > 0),
    kind text NOT NULL CHECK (kind IN ('conversation','task','task_tools','traffic')),
    scope_id text NOT NULL,
    exploration_id bigint NOT NULL DEFAULT 0,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX ai_storage_cleanup_due ON public.ai_storage_cleanup(next_attempt_at);

-- Enforce the parent lifecycle for new recordings, including late callbacks.
-- NOT VALID preserves pre-existing orphan history rather than purging it during
-- a schema upgrade; new writes and parent deletions are still enforced.
ALTER TABLE public.ai_llm_record ADD CONSTRAINT ai_llm_record_conversation_fk
 FOREIGN KEY(conversation_id) REFERENCES public.ai_conversation(id) ON DELETE CASCADE NOT VALID;
ALTER TABLE public.ai_llm_record ADD CONSTRAINT ai_llm_record_task_fk
 FOREIGN KEY(task_id) REFERENCES public.ai_task(id) ON DELETE CASCADE NOT VALID;
ALTER TABLE public.ai_info_gather_run ADD CONSTRAINT ai_info_gather_execution_fk
 FOREIGN KEY(execution_id) REFERENCES public.ai_run(id) ON DELETE CASCADE NOT VALID;

-- Registered vulnerability reports are durable derived records, not chat
-- history. Keep their saved evidence when the originating run disappears.
ALTER TABLE public.ai_conversation_vulnerability_report ALTER COLUMN conversation_id DROP NOT NULL;
ALTER TABLE public.ai_conversation_vulnerability_report ALTER COLUMN run_id DROP NOT NULL;
DO $$
DECLARE constraint_name text;
BEGIN
 SELECT conname INTO constraint_name FROM pg_constraint
 WHERE conrelid='public.ai_conversation_vulnerability_report'::regclass AND confrelid='public.ai_run'::regclass;
 EXECUTE format('ALTER TABLE public.ai_conversation_vulnerability_report DROP CONSTRAINT %I',constraint_name);
END $$;
ALTER TABLE public.ai_conversation_vulnerability_report ADD CONSTRAINT ai_vulnerability_report_run_fk
 FOREIGN KEY(tenant_id,conversation_id,owner_user_id,run_id)
 REFERENCES public.ai_run(tenant_id,conversation_id,owner_user_id,id) ON DELETE SET NULL (conversation_id,run_id);

-- A cleanup transaction can delete only rows belonging to its authorized job.
-- Normal graph writers retain their existing lease and ownership fences.
CREATE FUNCTION tianqiong_ai.storage_cleanup_allows(tbl text, row_data jsonb) RETURNS boolean
LANGUAGE plpgsql AS $$
DECLARE job public.ai_storage_cleanup%ROWTYPE; node_exp bigint;
BEGIN
 IF current_setting('tianqiong.execution_mode',TRUE) IS DISTINCT FROM 'storage_cleanup' THEN RETURN FALSE; END IF;
 SELECT j.* INTO job FROM public.ai_storage_cleanup j
 JOIN public.tenant_membership m ON m.tenant_id=j.tenant_id AND m.user_id=j.owner_user_id
 JOIN public.tenant t ON t.id=m.tenant_id JOIN public.auth_user u ON u.id=m.user_id
 WHERE j.id=NULLIF(current_setting('tianqiong.storage_cleanup_id',TRUE),'')::uuid
 AND t.status='active' AND u.is_active AND m.role IN ('admin','analyst')
 AND j.owner_user_id=NULLIF(current_setting('tianqiong.owner_user_id',TRUE),'')::integer;
 IF NOT FOUND THEN RETURN FALSE; END IF;
 IF job.kind='task_tools' THEN
  RETURN tbl='activity' AND (row_data->>'exploration_id')::bigint=job.exploration_id
   AND job.payload->'activity_ids' @> jsonb_build_array((row_data->>'id')::bigint);
 END IF;
 IF job.kind<>'task' THEN RETURN FALSE; END IF;
 IF tbl='finding_traffic_bindings' THEN RETURN job.payload->'finding_ids' @> jsonb_build_array((row_data->>'finding_id')::bigint); END IF;
 IF EXISTS(SELECT 1 FROM public.ai_task t WHERE t.id=job.scope_id::bigint
   AND (t.tenant_id<>job.tenant_id OR t.owner_user_id<>job.owner_user_id OR t.status='running')) THEN RETURN FALSE; END IF;
 IF tbl='explorations' THEN RETURN (row_data->>'id')::bigint=job.exploration_id; END IF;
 IF tbl IN ('findings','task_intelligence') THEN RETURN row_data->>'task_id'=job.scope_id; END IF;
 IF tbl='exploration_anchors' THEN
  SELECT exploration_id INTO node_exp FROM tianqiong_ai.exploration_nodes WHERE id=(row_data->>'node_id')::bigint;
  RETURN node_exp=job.exploration_id;
 END IF;
 RETURN tbl IN ('exploration_nodes','exploration_edges','task_constraints','activity')
  AND (row_data->>'exploration_id')::bigint=job.exploration_id;
END;
$$;

DO $$
DECLARE fn regprocedure; body text;
BEGIN
 FOREACH fn IN ARRAY ARRAY[
  'tianqiong_ai.guard_graph_write()'::regprocedure,
  'tianqiong_ai.guard_goal_node_write()'::regprocedure,
  'tianqiong_ai.guard_goal_edge_write()'::regprocedure,
  'tianqiong_ai.guard_task_constraint_write()'::regprocedure,
  'tianqiong_ai.guard_finding_traffic_binding()'::regprocedure,
  'tianqiong_ai.guard_goal_dependent_write()'::regprocedure,
  'tianqiong_ai.guard_finding_write()'::regprocedure
 ] LOOP
  body:=pg_get_functiondef(fn);
  body:=regexp_replace(body, E'BEGIN\n', E'BEGIN\n IF TG_OP=''DELETE'' AND tianqiong_ai.storage_cleanup_allows(TG_TABLE_NAME,to_jsonb(OLD)) THEN RETURN OLD; END IF;\n');
  EXECUTE body;
 END LOOP;
END $$;

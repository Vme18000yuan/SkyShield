-- DESTRUCTIVE TEST TEARDOWN ONLY.
ALTER TABLE public.ai_llm_record DROP CONSTRAINT ai_llm_record_conversation_fk;
ALTER TABLE public.ai_llm_record DROP CONSTRAINT ai_llm_record_task_fk;
ALTER TABLE public.ai_info_gather_run DROP CONSTRAINT ai_info_gather_execution_fk;
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
  body:=replace(body, E' IF TG_OP=''DELETE'' AND tianqiong_ai.storage_cleanup_allows(TG_TABLE_NAME,to_jsonb(OLD)) THEN RETURN OLD; END IF;\n', '');
  EXECUTE body;
 END LOOP;
END $$;
DROP FUNCTION tianqiong_ai.storage_cleanup_allows(text,jsonb);
DROP TABLE public.ai_storage_cleanup;
ALTER TABLE public.ai_conversation_vulnerability_report DROP CONSTRAINT ai_vulnerability_report_run_fk;
DELETE FROM public.ai_conversation_vulnerability_report WHERE conversation_id IS NULL OR run_id IS NULL;
ALTER TABLE public.ai_conversation_vulnerability_report ALTER COLUMN conversation_id SET NOT NULL;
ALTER TABLE public.ai_conversation_vulnerability_report ALTER COLUMN run_id SET NOT NULL;
ALTER TABLE public.ai_conversation_vulnerability_report ADD CONSTRAINT ai_vulnerability_report_run_fk
 FOREIGN KEY(tenant_id,conversation_id,owner_user_id,run_id)
 REFERENCES public.ai_run(tenant_id,conversation_id,owner_user_id,id) ON DELETE CASCADE;

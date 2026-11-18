-- DESTRUCTIVE TEST TEARDOWN ONLY.
BEGIN;
ALTER TABLE public.ai_conversation_vulnerability_report DROP COLUMN source_activity_id, DROP COLUMN evidence_activity_ids;
DROP INDEX public.ai_conversation_step_evidence;
DROP INDEX public.ai_conversation_step_order;
DROP INDEX public.ai_conversation_tool_occurrence;
ALTER TABLE public.ai_conversation_activity
    DROP COLUMN source_activity_id, DROP COLUMN tool_interrupted, DROP COLUMN tool_finished_at,
    DROP COLUMN tool_started_at, DROP COLUMN evidence_ref, DROP COLUMN tool_batch, DROP COLUMN model_round, DROP COLUMN trace_call_id;
COMMIT;

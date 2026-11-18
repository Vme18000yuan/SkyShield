BEGIN;
ALTER TABLE public.ai_conversation_activity
    ADD COLUMN trace_call_id text,
    ADD COLUMN model_round integer,
    ADD COLUMN tool_batch integer,
    ADD COLUMN evidence_ref text,
    ADD COLUMN tool_started_at timestamptz,
    ADD COLUMN tool_finished_at timestamptz,
    ADD COLUMN tool_interrupted boolean NOT NULL DEFAULT false,
    ADD COLUMN source_activity_id bigint REFERENCES public.ai_conversation_activity(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX ai_conversation_tool_occurrence
    ON public.ai_conversation_activity(run_id, trace_call_id, kind)
    WHERE trace_call_id IS NOT NULL AND kind IN ('tool_use', 'tool_result');
CREATE INDEX ai_conversation_step_order
    ON public.ai_conversation_activity(run_id, tool_use_id, id)
    WHERE kind IN ('tool_use', 'tool_result');
CREATE INDEX ai_conversation_step_evidence ON public.ai_conversation_activity(run_id,evidence_ref,id)
    WHERE evidence_ref IS NOT NULL;
ALTER TABLE public.ai_conversation_vulnerability_report
    ADD COLUMN source_activity_id bigint REFERENCES public.ai_conversation_activity(id) ON DELETE SET NULL,
    ADD COLUMN evidence_activity_ids bigint[] NOT NULL DEFAULT '{}';
COMMIT;

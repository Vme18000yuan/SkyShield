ALTER TABLE ai_run DROP CONSTRAINT ai_run_status_check;
ALTER TABLE ai_run ADD CONSTRAINT ai_run_status_check CHECK
    (status IN ('running','stopping','completed','failed','stopped','interrupted','timeout','exhausted'));
DROP INDEX idx_ai_run_one_active;
CREATE UNIQUE INDEX idx_ai_run_one_active ON ai_run(conversation_id) WHERE status IN ('running','stopping');
ALTER TABLE ai_run ADD COLUMN terminal_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE ai_run ADD COLUMN last_progress_at TIMESTAMPTZ;
ALTER TABLE ai_run ADD COLUMN live_text TEXT NOT NULL DEFAULT '';
ALTER TABLE ai_run ADD COLUMN live_kind TEXT NOT NULL DEFAULT '';

-- A lease is a permit, not an instruction to replay a command.
CREATE TABLE ai_execution_permit (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    owner_user_id INTEGER NOT NULL REFERENCES auth_user(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    lease_owner UUID NOT NULL,
    lease_until TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX idx_ai_execution_permit_scope ON ai_execution_permit(tenant_id,owner_user_id,lease_until);
CREATE INDEX idx_ai_execution_permit_expiry ON ai_execution_permit(lease_until);

-- Stopping is not a terminal acknowledgement for review/retest projections.
DO $$
DECLARE fn regprocedure; body text;
BEGIN
 FOREACH fn IN ARRAY ARRAY['public.tq_finish_finding_retest()'::regprocedure,'tianqiong_ai.finish_finding_case_review()'::regprocedure] LOOP
  body:=pg_get_functiondef(fn);
  body:=replace(body, 'IF OLD.status<>''running'' OR NEW.status=''running'' THEN RETURN NEW; END IF;',
    'IF OLD.status NOT IN (''running'',''stopping'') OR NEW.status IN (''running'',''stopping'') THEN RETURN NEW; END IF;');
  EXECUTE body;
 END LOOP;
END $$;

-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
DROP TABLE ai_execution_permit;
ALTER TABLE ai_run DROP COLUMN live_kind, DROP COLUMN live_text, DROP COLUMN last_progress_at, DROP COLUMN terminal_reason;
DROP INDEX idx_ai_run_one_active;
ALTER TABLE ai_run DROP CONSTRAINT ai_run_status_check;
UPDATE ai_run SET status=CASE WHEN status='stopping' THEN 'stopped' ELSE 'failed' END
    WHERE status IN ('stopping','timeout','exhausted');
ALTER TABLE ai_run ADD CONSTRAINT ai_run_status_check CHECK
    (status IN ('running','completed','failed','stopped','interrupted'));
CREATE UNIQUE INDEX idx_ai_run_one_active ON ai_run(conversation_id) WHERE status='running';

DO $$
DECLARE fn regprocedure; body text;
BEGIN
 FOREACH fn IN ARRAY ARRAY['public.tq_finish_finding_retest()'::regprocedure,'tianqiong_ai.finish_finding_case_review()'::regprocedure] LOOP
  body:=pg_get_functiondef(fn);
  body:=replace(body, 'IF OLD.status NOT IN (''running'',''stopping'') OR NEW.status IN (''running'',''stopping'') THEN RETURN NEW; END IF;',
    'IF OLD.status<>''running'' OR NEW.status=''running'' THEN RETURN NEW; END IF;');
  EXECUTE body;
 END LOOP;
END $$;

-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup or forward fix.
BEGIN;
DROP INDEX tianqiong_ai.idx_tq_activity_execution_tool;
DROP TRIGGER tq_activity_execution ON tianqiong_ai.activity;
DROP FUNCTION tianqiong_ai.assign_activity_execution();
ALTER TABLE tianqiong_ai.activity DROP COLUMN execution_id;
COMMIT;

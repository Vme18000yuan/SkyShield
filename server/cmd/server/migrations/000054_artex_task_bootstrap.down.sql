-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
ALTER TABLE ai_task DROP COLUMN bootstrap_complete;

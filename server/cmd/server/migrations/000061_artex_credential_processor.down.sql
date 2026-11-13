-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
DROP TRIGGER tq_guard_credential_job ON ai_credential_job;
DROP FUNCTION tianqiong_ai.guard_credential_job();
DROP TABLE ai_credential_job;
DROP INDEX tianqiong_ai.tq_task_intelligence_credentials;

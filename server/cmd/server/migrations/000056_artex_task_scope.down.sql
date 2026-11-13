-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
DROP FUNCTION tianqiong_ai.task_scope_assets(bigint,integer);
DROP FUNCTION tianqiong_ai.scope_inet(text);
DROP FUNCTION tianqiong_ai.scope_host(text);
DROP TRIGGER tq_guard_task_scope ON ai_task_scope;
DROP FUNCTION tianqiong_ai.guard_task_scope();
DROP TABLE ai_task_scope;
ALTER TABLE ai_task DROP COLUMN coverage_enabled;

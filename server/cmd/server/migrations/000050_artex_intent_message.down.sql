-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
DROP TRIGGER tq_guard_intent_message ON ai_task_intent_message;
DROP FUNCTION tianqiong_ai.guard_intent_message();
DROP TABLE ai_task_intent_message;

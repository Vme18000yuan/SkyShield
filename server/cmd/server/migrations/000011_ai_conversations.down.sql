-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup or forward fix.
DROP TABLE IF EXISTS ai_conversation_activity;
DROP TABLE IF EXISTS ai_run;
DROP TABLE IF EXISTS ai_conversation;

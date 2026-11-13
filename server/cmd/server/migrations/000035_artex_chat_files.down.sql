-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
DROP INDEX idx_ai_conversation_history_reverse;
DROP TABLE ai_chat_attachment;

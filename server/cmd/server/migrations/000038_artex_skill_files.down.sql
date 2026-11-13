-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
DROP TRIGGER tq_ai_task_skill_usage ON tianqiong_ai.activity;
DROP TRIGGER tq_ai_chat_skill_usage ON ai_conversation_activity;
DROP FUNCTION tq_capture_ai_skill_usage();
DROP TABLE ai_skill_usage;
DROP TABLE ai_skill_file;
ALTER TABLE ai_skill DROP COLUMN mcps,DROP COLUMN compatibility,DROP COLUMN license;
ALTER TABLE ai_skill DROP CONSTRAINT ai_skill_instructions_check;
ALTER TABLE ai_skill ADD CONSTRAINT ai_skill_instructions_check CHECK(octet_length(instructions)<=65536 AND btrim(instructions)<>'');

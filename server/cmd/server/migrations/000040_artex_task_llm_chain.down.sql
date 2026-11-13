-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
DROP TABLE ai_task_llm_history;
DROP TABLE ai_task_llm_profile;
ALTER TABLE ai_task DROP COLUMN llm_chain_revision,DROP COLUMN active_llm_profile_id;

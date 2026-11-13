-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
DROP TABLE ai_llm_health;
DROP TABLE ai_runtime_setting;
ALTER TABLE ai_llm_profile DROP COLUMN encrypted_proxy_url;
ALTER TABLE ai_llm_profile DROP COLUMN api_key_hint;
ALTER TABLE ai_llm_profile DROP COLUMN options;
ALTER TABLE ai_llm_profile DROP COLUMN runtime_id;

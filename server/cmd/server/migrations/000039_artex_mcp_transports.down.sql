-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup restore or a forward fix.
ALTER TABLE ai_mcp_server DROP COLUMN tools_cache,DROP COLUMN encrypted_env,DROP COLUMN args,DROP COLUMN command,DROP COLUMN transport;

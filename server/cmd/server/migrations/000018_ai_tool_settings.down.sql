-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup or forward fix.
BEGIN;
DROP TABLE public.ai_agent_tool_setting;
COMMIT;

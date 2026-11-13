-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup or forward fix.
BEGIN;
DROP TABLE public.ai_mcp_server_agent;
DROP TABLE public.ai_mcp_server;
COMMIT;

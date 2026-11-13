-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses a verified backup restore or an approved forward fix.
BEGIN;
DELETE FROM public.ai_skill_agent WHERE agent_key NOT IN ('assistant','planner','worker');
DELETE FROM public.ai_mcp_server_agent WHERE agent_key NOT IN ('assistant','planner','worker');
DELETE FROM public.ai_skill WHERE builtin_key IS NOT NULL;
ALTER TABLE public.ai_skill_agent DROP CONSTRAINT IF EXISTS ai_skill_agent_agent_key_check;
ALTER TABLE public.ai_skill_agent ALTER COLUMN agent_key TYPE VARCHAR(20);
ALTER TABLE public.ai_skill_agent ADD CONSTRAINT ai_skill_agent_agent_key_check CHECK (agent_key IN ('assistant','planner','worker'));
ALTER TABLE public.ai_mcp_server_agent DROP CONSTRAINT IF EXISTS ai_mcp_server_agent_agent_key_check;
ALTER TABLE public.ai_mcp_server_agent ALTER COLUMN agent_key TYPE VARCHAR(20);
ALTER TABLE public.ai_mcp_server_agent ADD CONSTRAINT ai_mcp_server_agent_agent_key_check CHECK (agent_key IN ('assistant','planner','worker'));
DROP INDEX IF EXISTS public.idx_ai_skill_builtin_key;
ALTER TABLE public.ai_skill DROP COLUMN IF EXISTS builtin_key;
ALTER TABLE public.ai_conversation DROP COLUMN IF EXISTS agent_key;
DROP TABLE IF EXISTS public.ai_custom_agent;
COMMIT;

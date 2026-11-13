-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses a verified backup restore or an approved forward fix.
BEGIN;
DROP TABLE IF EXISTS public.ai_auto_task_action;
DROP TABLE IF EXISTS public.ai_conversation_llm_binding;
DROP TABLE IF EXISTS public.ai_agent_prompt_version;
DROP TABLE IF EXISTS public.ai_agent_llm_binding;
DROP TABLE IF EXISTS public.ai_agent_wrapup_config;
DROP TABLE IF EXISTS public.ai_builtin_agent_config;
ALTER TABLE public.ai_conversation DROP COLUMN IF EXISTS pinned;
COMMIT;

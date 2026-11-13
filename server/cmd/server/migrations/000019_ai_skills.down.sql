-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup or forward fix.
BEGIN;
DROP TABLE public.ai_skill_agent;
DROP TABLE public.ai_skill;
COMMIT;

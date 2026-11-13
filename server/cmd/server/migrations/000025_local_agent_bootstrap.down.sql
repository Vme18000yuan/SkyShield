-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses a verified backup restore or an approved forward fix.
BEGIN;
DROP TABLE IF EXISTS public.deployment_local_agent_bootstrap;
COMMIT;

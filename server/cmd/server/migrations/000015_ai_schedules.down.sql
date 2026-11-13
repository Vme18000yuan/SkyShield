-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup or forward fix.
BEGIN;
DROP TABLE public.ai_schedule_history;
DROP TABLE public.ai_schedule_task;
DROP TABLE public.ai_schedule;
COMMIT;

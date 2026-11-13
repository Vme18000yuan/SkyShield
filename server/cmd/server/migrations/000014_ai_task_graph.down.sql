-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup or forward fix.
BEGIN;
DROP TRIGGER IF EXISTS tq_target_ai_task_cleanup ON public.target;
DROP FUNCTION IF EXISTS public.tq_pause_tasks_for_deleted_target();
DROP TABLE IF EXISTS public.ai_finding_risk_link;
DROP VIEW IF EXISTS tianqiong_ai.task_archives;
DROP TABLE IF EXISTS public.ai_task_archive;
DROP VIEW IF EXISTS tianqiong_ai.task_relations;
DROP VIEW IF EXISTS tianqiong_ai.tasks;
DROP TABLE IF EXISTS public.ai_task_relation;
DROP TABLE IF EXISTS public.ai_task_asset;
DROP TABLE IF EXISTS public.ai_task CASCADE;
DROP SCHEMA IF EXISTS tianqiong_ai CASCADE;
COMMIT;

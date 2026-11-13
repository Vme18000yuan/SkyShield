-- DESTRUCTIVE TEST TEARDOWN ONLY.
DROP VIEW IF EXISTS tianqiong_ai.notification_events CASCADE;
DROP TRIGGER IF EXISTS ai_emit_finding_status_changed ON tianqiong_ai.findings;
DROP TRIGGER IF EXISTS ai_emit_finding_created ON tianqiong_ai.findings;
DROP FUNCTION IF EXISTS tianqiong_ai.accept_finding_notification_adapter();
DROP FUNCTION IF EXISTS tianqiong_ai.emit_finding_notification();
DROP FUNCTION IF EXISTS tianqiong_ai.finding_notification_event_id(TEXT,BIGINT,TEXT,TEXT,BIGINT);
DROP FUNCTION IF EXISTS tianqiong_ai.finding_notification_visible(BIGINT,INTEGER);
DELETE FROM public.notification_outbox WHERE kind IN ('ai-finding-created','ai-finding-status-changed');
DELETE FROM public.notification_fact WHERE kind IN ('ai-finding-created','ai-finding-status-changed');
ALTER TABLE public.notification_outbox DROP CONSTRAINT notification_outbox_kind_check;
ALTER TABLE public.notification_outbox ADD CONSTRAINT notification_outbox_kind_check CHECK(kind IN ('scan-succeeded','scan-failed','vulnerability-observed','agent-offline','nuclei-poc-sync-succeeded','nuclei-poc-sync-failed'));
ALTER TABLE public.notification_fact DROP CONSTRAINT notification_fact_kind_check;
ALTER TABLE public.notification_fact ADD CONSTRAINT notification_fact_kind_check CHECK(kind IN ('scan-succeeded','scan-failed','vulnerability-observed','agent-offline','nuclei-poc-sync-succeeded','nuclei-poc-sync-failed'));
ALTER TABLE public.notification_inbox DROP CONSTRAINT notification_inbox_kind_check;
ALTER TABLE public.notification_inbox ADD CONSTRAINT notification_inbox_kind_check CHECK(kind IN ('scan-succeeded','scan-failed','vulnerability-observed','agent-offline','nuclei-poc-sync-succeeded','nuclei-poc-sync-failed'));

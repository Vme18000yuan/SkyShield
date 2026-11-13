-- DESTRUCTIVE TEST TEARDOWN ONLY.
DROP TRIGGER IF EXISTS ai_enqueue_finding_outbound_event ON public.notification_outbox;
DROP FUNCTION IF EXISTS tianqiong_ai.enqueue_finding_outbound_event();
DROP TABLE IF EXISTS public.ai_notification_deliveries;
DROP FUNCTION IF EXISTS tianqiong_ai.outbound_notification_visible(TEXT,INTEGER);
DROP TABLE IF EXISTS public.ai_notification_events;
DROP TABLE IF EXISTS public.ai_notification_channels;
DROP TABLE IF EXISTS public.ai_notification_settings;

-- DESTRUCTIVE TEST TEARDOWN ONLY.
BEGIN;
DROP TRIGGER ai_notify_chat_report_insert ON public.ai_conversation_vulnerability_report;
DROP FUNCTION tianqiong_ai.notify_chat_report_insert();
DROP TRIGGER ai_notify_gather_risk_import ON public.ai_info_gather_record;
DROP FUNCTION tianqiong_ai.notify_gather_risk_import();
DROP TRIGGER ai_notify_inventory_insert ON public.vulnerability;
DROP FUNCTION tianqiong_ai.notify_inventory_insert();
DROP FUNCTION tianqiong_ai.queue_inventory_notification(integer,integer,uuid,uuid,text,text);
DROP FUNCTION tianqiong_ai.outbound_notification_visible(text,integer);
ALTER FUNCTION tianqiong_ai.outbound_task_notification_visible(text,integer) RENAME TO outbound_notification_visible;
DELETE FROM public.ai_notification_events WHERE vulnerability_id IS NOT NULL;
ALTER TABLE public.ai_notification_deliveries
 DROP CONSTRAINT ai_notification_deliveries_event_id_fkey,
 ADD CONSTRAINT ai_notification_deliveries_event_id_fkey FOREIGN KEY(event_id)
   REFERENCES public.ai_notification_events(event_id);
ALTER TABLE public.ai_notification_events DROP CONSTRAINT ai_notification_event_source,
 DROP COLUMN vulnerability_id, DROP COLUMN conversation_id, DROP COLUMN gather_run_id,
 ALTER COLUMN task_id SET NOT NULL, ALTER COLUMN finding_id SET NOT NULL;
DROP FUNCTION public.tq_inventory_cdn_asset(text,text,uuid,integer,integer);
DROP FUNCTION public.tq_inventory_is_cdn(inet,uuid,integer,integer);
DROP INDEX public.asset_imported_cdn_ip;
DROP TABLE public.asset_cdn_prefix;
COMMIT;

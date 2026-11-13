-- AI finding notifications use the existing durable inbox outbox.  They are
-- owner-scoped and deliberately excluded from installation-wide destinations.
ALTER TABLE public.notification_outbox DROP CONSTRAINT notification_outbox_kind_check;
ALTER TABLE public.notification_outbox ADD CONSTRAINT notification_outbox_kind_check CHECK(kind IN ('scan-succeeded','scan-failed','vulnerability-observed','agent-offline','nuclei-poc-sync-succeeded','nuclei-poc-sync-failed','ai-finding-created','ai-finding-status-changed'));
ALTER TABLE public.notification_fact DROP CONSTRAINT notification_fact_kind_check;
ALTER TABLE public.notification_fact ADD CONSTRAINT notification_fact_kind_check CHECK(kind IN ('scan-succeeded','scan-failed','vulnerability-observed','agent-offline','nuclei-poc-sync-succeeded','nuclei-poc-sync-failed','ai-finding-created','ai-finding-status-changed'));
ALTER TABLE public.notification_inbox DROP CONSTRAINT notification_inbox_kind_check;
ALTER TABLE public.notification_inbox ADD CONSTRAINT notification_inbox_kind_check CHECK(kind IN ('scan-succeeded','scan-failed','vulnerability-observed','agent-offline','nuclei-poc-sync-succeeded','nuclei-poc-sync-failed','ai-finding-created','ai-finding-status-changed'));

CREATE FUNCTION tianqiong_ai.finding_notification_visible(p_fact BIGINT,p_user INTEGER) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
 SELECT COALESCE((SELECT CASE WHEN f.kind NOT IN ('ai-finding-created','ai-finding-status-changed') THEN TRUE ELSE EXISTS(
  SELECT 1 FROM public.ai_task task JOIN public.tenant_membership m ON m.tenant_id=task.tenant_id AND m.user_id=task.owner_user_id
  JOIN public.auth_user u ON u.id=m.user_id JOIN public.tenant tenant ON tenant.id=m.tenant_id
  WHERE task.id::text=f.payload->>'taskId' AND task.tenant_id::text=f.payload->>'tenantId' AND task.owner_user_id=p_user AND f.payload->>'ownerUserId'=p_user::text
   AND task.deleted_at IS NULL AND u.is_active AND tenant.status='active' AND m.role IN ('admin','analyst')
   AND (task.original_target_id IS NULL OR EXISTS(SELECT 1 FROM public.target x WHERE x.id=task.target_id AND x.deleted_at IS NULL))
   AND NOT EXISTS(SELECT 1 FROM public.ai_task_archive a WHERE a.task_id=task.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting')))
 END FROM public.notification_fact f WHERE f.id=p_fact),FALSE)
$$;

CREATE FUNCTION tianqiong_ai.finding_notification_event_id(p_kind TEXT,p_finding BIGINT,p_from TEXT,p_to TEXT,p_xid BIGINT) RETURNS TEXT LANGUAGE sql IMMUTABLE AS $$
 SELECT CASE WHEN p_kind='finding_created' THEN 'ai-finding:'||p_finding::text||':created'
  WHEN p_kind='finding_status_changed' THEN 'ai-finding:'||p_finding::text||':status:'||p_xid::text||':'||encode(sha256(convert_to(jsonb_build_array(COALESCE(p_from,''),COALESCE(p_to,''))::text,'UTF8')),'hex')
  ELSE NULL END
$$;

CREATE FUNCTION tianqiong_ai.emit_finding_notification() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_task public.ai_task%ROWTYPE; v_kind TEXT; v_event TEXT; v_from TEXT; v_to TEXT; v_name TEXT; v_severity TEXT; v_priority TEXT;
BEGIN
 IF TG_OP='UPDATE' AND NEW.status IS NOT DISTINCT FROM OLD.status THEN RETURN NEW; END IF;
 BEGIN
  SELECT * INTO v_task FROM public.ai_task WHERE id=NEW.task_id AND deleted_at IS NULL;
  IF NOT FOUND THEN RETURN NEW; END IF;
  IF TG_OP='INSERT' THEN v_kind:='finding_created';v_from:='';v_to:='';
  ELSE v_kind:='finding_status_changed';v_from:=COALESCE(OLD.status,'');v_to:=COALESCE(NEW.status,''); END IF;
  v_event:=tianqiong_ai.finding_notification_event_id(v_kind,NEW.id,v_from,v_to,txid_current());
  v_name:=left(COALESCE(NULLIF(btrim(NEW.name),''),NULLIF(btrim(NEW.vulnclass),''),'未命名发现'),200);
  v_severity:=left(lower(COALESCE(NEW.severity,'')),32);
  v_priority:=CASE v_severity WHEN 'critical' THEN 'critical' WHEN 'high' THEN 'high' ELSE 'normal' END;
  INSERT INTO public.notification_outbox(event_id,kind,payload_version,subject_name,occurred_at,priority,payload)
  VALUES(v_event,CASE v_kind WHEN 'finding_created' THEN 'ai-finding-created' ELSE 'ai-finding-status-changed' END,1,
   'aiTasks/'||v_task.id::text||'/findings/'||NEW.id::text,clock_timestamp(),v_priority,
   jsonb_build_object('tenantId',v_task.tenant_id::text,'ownerUserId',v_task.owner_user_id,'taskId',v_task.id,'taskName',left(COALESCE(NULLIF(btrim(v_task.name),''),'AI 任务'),200),'findingId',NEW.id,
    'name',v_name,'severity',v_severity,'fromStatus',left(v_from,64),'toStatus',left(v_to,64)))
  ON CONFLICT(event_id) DO NOTHING;
 EXCEPTION WHEN OTHERS THEN
  -- Notification failure must not roll back the finding or its status.
  RAISE WARNING 'AI finding notification could not be queued';
 END;
 RETURN NEW;
END $$;
CREATE TRIGGER ai_emit_finding_created AFTER INSERT ON tianqiong_ai.findings FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.emit_finding_notification();
CREATE TRIGGER ai_emit_finding_status_changed AFTER UPDATE OF status ON tianqiong_ai.findings FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.emit_finding_notification();

-- The vendored ARTEX writer also inserts notification_events after a finding
-- mutation. This adapter acknowledges only an event already queued by the
-- host trigger, avoiding duplicate delivery without hiding an enqueue failure.
CREATE VIEW tianqiong_ai.notification_events AS SELECT ''::text AS kind,0::bigint AS finding_id,'{}'::jsonb AS snapshot WHERE FALSE;
CREATE FUNCTION tianqiong_ai.accept_finding_notification_adapter() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_event TEXT;
BEGIN
 v_event:=tianqiong_ai.finding_notification_event_id(NEW.kind,NEW.finding_id,NEW.snapshot->>'from_status',NEW.snapshot->>'to_status',txid_current());
 IF v_event IS NULL OR NOT EXISTS(SELECT 1 FROM public.notification_outbox WHERE event_id=v_event) THEN
  RAISE EXCEPTION 'AI finding notification was not queued';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER ai_finding_notification_adapter INSTEAD OF INSERT ON tianqiong_ai.notification_events
 FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.accept_finding_notification_adapter();

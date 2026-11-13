-- AI notification destinations belong to an account.  They deliberately do
-- not use the installation-wide destinations in the native inbox subsystem.
CREATE TABLE public.ai_notification_settings (
 tenant_id UUID NOT NULL REFERENCES public.tenant(id),
 owner_user_id INTEGER NOT NULL REFERENCES public.auth_user(id),
 enabled BOOLEAN NOT NULL DEFAULT TRUE,
 public_base_url TEXT NOT NULL DEFAULT '',
 digest_interval_min INTEGER NOT NULL DEFAULT 30 CHECK(digest_interval_min BETWEEN 1 AND 10080),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,owner_user_id)
);

CREATE TABLE public.ai_notification_channels (
 id BIGSERIAL PRIMARY KEY,
 tenant_id UUID NOT NULL REFERENCES public.tenant(id),
 owner_user_id INTEGER NOT NULL REFERENCES public.auth_user(id),
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 200),
 kind TEXT NOT NULL CHECK(kind IN ('dingtalk','feishu','wecom','webhook','telegram','email')),
 enabled BOOLEAN NOT NULL DEFAULT FALSE,
 enabled_at TIMESTAMPTZ,
 mode TEXT NOT NULL CHECK(mode IN ('realtime','digest')),
 config_cipher BYTEA NOT NULL,
 filter JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(filter)='object'),
 rate_per_min INTEGER NOT NULL DEFAULT 0 CHECK(rate_per_min BETWEEN 0 AND 100000),
 next_send_at TIMESTAMPTZ NOT NULL DEFAULT '-infinity',
 version BIGINT NOT NULL DEFAULT 1,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 deleted_at TIMESTAMPTZ
);
CREATE INDEX ai_notification_channels_owner ON public.ai_notification_channels(tenant_id,owner_user_id,id) WHERE deleted_at IS NULL;

CREATE TABLE public.ai_notification_events (
 event_id TEXT PRIMARY KEY,
 tenant_id UUID NOT NULL,
 owner_user_id INTEGER NOT NULL,
 task_id BIGINT NOT NULL,
 finding_id BIGINT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('finding_created','finding_status_changed')),
 snapshot JSONB NOT NULL CHECK(jsonb_typeof(snapshot)='object'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 fanned_at TIMESTAMPTZ
);
CREATE INDEX ai_notification_events_unfanned ON public.ai_notification_events(created_at,event_id) WHERE fanned_at IS NULL;

CREATE FUNCTION tianqiong_ai.outbound_notification_visible(p_event TEXT,p_user INTEGER) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM public.ai_notification_events e JOIN public.ai_task t ON t.id=e.task_id AND t.tenant_id=e.tenant_id AND t.owner_user_id=e.owner_user_id
  JOIN public.tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id
  JOIN public.auth_user u ON u.id=m.user_id JOIN public.tenant tenant ON tenant.id=m.tenant_id
  WHERE e.event_id=p_event AND e.owner_user_id=p_user AND t.deleted_at IS NULL AND m.role IN ('admin','analyst') AND u.is_active AND tenant.status='active'
   AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM public.target x WHERE x.id=t.target_id AND x.deleted_at IS NULL))
   AND NOT EXISTS(SELECT 1 FROM public.ai_task_archive a WHERE a.task_id=t.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting')))
$$;

CREATE TABLE public.ai_notification_deliveries (
 id BIGSERIAL PRIMARY KEY,
 event_id TEXT NOT NULL REFERENCES public.ai_notification_events(event_id),
 channel_id BIGINT NOT NULL REFERENCES public.ai_notification_channels(id),
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','sending','sent','failed','skipped')),
 attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts >= 0),
 next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 lease_token TEXT,
 last_error TEXT NOT NULL DEFAULT '',
 batch_id TEXT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 sent_at TIMESTAMPTZ,
 UNIQUE(event_id,channel_id)
);
CREATE INDEX ai_notification_deliveries_due ON public.ai_notification_deliveries(channel_id,next_attempt_at,id) WHERE state IN ('pending','sending');

-- Take a separate outbound snapshot without changing the strict inbox payload.
-- Failure must never roll back the underlying finding or inbox event.
CREATE FUNCTION tianqiong_ai.enqueue_finding_outbound_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_kind TEXT; v_finding tianqiong_ai.findings%ROWTYPE; v_task public.ai_task%ROWTYPE; v_assets JSONB;
BEGIN
 IF NEW.kind NOT IN ('ai-finding-created','ai-finding-status-changed') THEN RETURN NEW; END IF;
 BEGIN
  SELECT * INTO v_finding FROM tianqiong_ai.findings WHERE id::text=NEW.payload->>'findingId';
  IF NOT FOUND THEN RETURN NEW; END IF;
  SELECT * INTO v_task FROM public.ai_task WHERE id=v_finding.task_id AND tenant_id::text=NEW.payload->>'tenantId' AND owner_user_id::text=NEW.payload->>'ownerUserId';
  IF NOT FOUND THEN RETURN NEW; END IF;
  -- Do not duplicate finding summaries for accounts that have never opted in
  -- to an outbound channel. Events created before a channel is enabled are
  -- intentionally not replayed to that new destination.
  IF NOT EXISTS(SELECT 1 FROM public.ai_notification_channels c WHERE c.tenant_id=v_task.tenant_id AND c.owner_user_id=v_task.owner_user_id AND c.enabled AND c.deleted_at IS NULL) THEN RETURN NEW; END IF;
  SELECT COALESCE(jsonb_agg(value),'[]'::jsonb) INTO v_assets FROM (
   SELECT value FROM jsonb_array_elements(CASE WHEN jsonb_typeof(v_finding.asset_ids)='array' THEN v_finding.asset_ids ELSE '[]'::jsonb END) LIMIT 500
  ) a;
  v_kind:=CASE NEW.kind WHEN 'ai-finding-created' THEN 'finding_created' ELSE 'finding_status_changed' END;
  INSERT INTO public.ai_notification_events(event_id,tenant_id,owner_user_id,task_id,finding_id,kind,snapshot,created_at)
  VALUES(NEW.event_id,v_task.tenant_id,v_task.owner_user_id,v_task.id,v_finding.id,v_kind,
   jsonb_build_object('kind',v_kind,'finding_id',v_finding.id,'task_id',v_task.id,'name',left(COALESCE(v_finding.name,''),200),
    'vulnclass',left(COALESCE(v_finding.vulnclass,''),500),'severity',left(COALESCE(v_finding.severity,''),32),
    'summary',left(COALESCE(v_finding.summary,''),8000),'asset_ids',v_assets,
    'from_status',COALESCE(NEW.payload->>'fromStatus',''),'to_status',COALESCE(NEW.payload->>'toStatus','')),NEW.occurred_at)
  ON CONFLICT(event_id) DO NOTHING;
 EXCEPTION WHEN OTHERS THEN
  RAISE WARNING 'AI outbound notification could not be queued';
 END;
 RETURN NEW;
END $$;
CREATE TRIGGER ai_enqueue_finding_outbound_event AFTER INSERT ON public.notification_outbox
 FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.enqueue_finding_outbound_event();

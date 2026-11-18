BEGIN;

-- Presentation data only. A provider prefix identifies shared infrastructure;
-- it never changes target scope, scan inputs, or the identity of an asset.
CREATE TABLE public.asset_cdn_prefix (
    network cidr PRIMARY KEY,
    provider text NOT NULL
);
CREATE INDEX asset_cdn_prefix_lookup ON public.asset_cdn_prefix USING gist(network inet_ops);
-- Provider-published snapshot, 2026-11-18. Only GLOBAL/CLOUDFRONT ranges
-- from AWS are used, not general cloud hosting ranges.
-- https://api.cloudflare.com/client/v4/ips
-- https://api.fastly.com/public-ip-list
-- https://ip-ranges.amazonaws.com/ip-ranges.json
INSERT INTO public.asset_cdn_prefix(network, provider) VALUES
    ('103.21.244.0/22', 'cloudflare'),
    ('103.22.200.0/22', 'cloudflare'),
    ('103.31.4.0/22', 'cloudflare'),
    ('104.16.0.0/13', 'cloudflare'),
    ('104.24.0.0/14', 'cloudflare'),
    ('108.162.192.0/18', 'cloudflare'),
    ('131.0.72.0/22', 'cloudflare'),
    ('141.101.64.0/18', 'cloudflare'),
    ('162.158.0.0/15', 'cloudflare'),
    ('172.64.0.0/13', 'cloudflare'),
    ('173.245.48.0/20', 'cloudflare'),
    ('188.114.96.0/20', 'cloudflare'),
    ('190.93.240.0/20', 'cloudflare'),
    ('197.234.240.0/22', 'cloudflare'),
    ('198.41.128.0/17', 'cloudflare'),
    ('2400:cb00::/32', 'cloudflare'),
    ('2405:8100::/32', 'cloudflare'),
    ('2405:b500::/32', 'cloudflare'),
    ('2606:4700::/32', 'cloudflare'),
    ('2803:f800::/32', 'cloudflare'),
    ('2a06:98c0::/29', 'cloudflare'),
    ('2c0f:f248::/32', 'cloudflare'),
    ('103.244.50.0/24', 'fastly'),
    ('103.245.222.0/23', 'fastly'),
    ('103.245.224.0/24', 'fastly'),
    ('104.156.80.0/20', 'fastly'),
    ('140.248.128.0/17', 'fastly'),
    ('140.248.64.0/18', 'fastly'),
    ('146.75.0.0/17', 'fastly'),
    ('151.101.0.0/16', 'fastly'),
    ('157.52.64.0/18', 'fastly'),
    ('167.82.0.0/17', 'fastly'),
    ('167.82.128.0/20', 'fastly'),
    ('167.82.160.0/20', 'fastly'),
    ('167.82.224.0/20', 'fastly'),
    ('172.111.64.0/18', 'fastly'),
    ('185.31.16.0/22', 'fastly'),
    ('199.232.0.0/16', 'fastly'),
    ('199.27.72.0/21', 'fastly'),
    ('23.235.32.0/20', 'fastly'),
    ('2a04:4e40::/32', 'fastly'),
    ('2a04:4e42::/31', 'fastly'),
    ('2a04:4e44::/32', 'fastly'),
    ('43.249.72.0/22', 'fastly'),
    ('90.98.0.0/17', 'fastly'),
    ('90.98.128.0/18', 'fastly'),
    ('108.138.0.0/15', 'cloudfront'),
    ('108.156.0.0/14', 'cloudfront'),
    ('111.13.171.128/26', 'cloudfront'),
    ('111.13.171.192/26', 'cloudfront'),
    ('111.13.185.32/27', 'cloudfront'),
    ('111.13.185.64/27', 'cloudfront'),
    ('116.129.226.0/25', 'cloudfront'),
    ('116.129.226.128/26', 'cloudfront'),
    ('118.193.97.128/25', 'cloudfront'),
    ('118.193.97.64/26', 'cloudfront'),
    ('119.147.182.0/25', 'cloudfront'),
    ('119.147.182.128/26', 'cloudfront'),
    ('120.232.236.0/25', 'cloudfront'),
    ('120.232.236.128/26', 'cloudfront'),
    ('120.253.240.192/26', 'cloudfront'),
    ('120.253.241.160/27', 'cloudfront'),
    ('120.253.245.128/26', 'cloudfront'),
    ('120.253.245.192/27', 'cloudfront'),
    ('120.52.12.64/26', 'cloudfront'),
    ('120.52.153.192/26', 'cloudfront'),
    ('120.52.22.96/27', 'cloudfront'),
    ('120.52.39.128/27', 'cloudfront'),
    ('13.224.0.0/14', 'cloudfront'),
    ('13.249.0.0/16', 'cloudfront'),
    ('13.32.0.0/15', 'cloudfront'),
    ('13.35.0.0/16', 'cloudfront'),
    ('130.176.0.0/17', 'cloudfront'),
    ('130.176.128.0/18', 'cloudfront'),
    ('130.176.192.0/19', 'cloudfront'),
    ('130.176.224.0/20', 'cloudfront'),
    ('143.204.0.0/16', 'cloudfront'),
    ('144.220.0.0/16', 'cloudfront'),
    ('15.158.0.0/16', 'cloudfront'),
    ('18.154.0.0/15', 'cloudfront'),
    ('18.160.0.0/15', 'cloudfront'),
    ('18.164.0.0/15', 'cloudfront'),
    ('18.172.0.0/15', 'cloudfront'),
    ('18.238.0.0/15', 'cloudfront'),
    ('18.244.0.0/15', 'cloudfront'),
    ('18.64.0.0/14', 'cloudfront'),
    ('18.68.0.0/16', 'cloudfront'),
    ('180.163.57.0/25', 'cloudfront'),
    ('180.163.57.128/26', 'cloudfront'),
    ('2001:3fc6:20::/43', 'cloudfront'),
    ('204.246.164.0/22', 'cloudfront'),
    ('204.246.168.0/22', 'cloudfront'),
    ('204.246.172.0/24', 'cloudfront'),
    ('204.246.173.0/24', 'cloudfront'),
    ('204.246.174.0/23', 'cloudfront'),
    ('204.246.176.0/20', 'cloudfront'),
    ('205.251.202.0/23', 'cloudfront'),
    ('205.251.204.0/23', 'cloudfront'),
    ('205.251.206.0/23', 'cloudfront'),
    ('205.251.208.0/20', 'cloudfront'),
    ('205.251.249.0/24', 'cloudfront'),
    ('205.251.250.0/23', 'cloudfront'),
    ('205.251.252.0/23', 'cloudfront'),
    ('205.251.254.0/24', 'cloudfront'),
    ('216.137.32.0/19', 'cloudfront'),
    ('23.228.212.0/24', 'cloudfront'),
    ('23.228.213.0/24', 'cloudfront'),
    ('23.228.214.0/24', 'cloudfront'),
    ('23.228.220.0/24', 'cloudfront'),
    ('23.228.221.0/24', 'cloudfront'),
    ('23.228.222.0/24', 'cloudfront'),
    ('23.228.223.0/24', 'cloudfront'),
    ('23.228.244.0/24', 'cloudfront'),
    ('23.228.246.0/24', 'cloudfront'),
    ('23.228.247.0/24', 'cloudfront'),
    ('23.228.248.0/24', 'cloudfront'),
    ('23.228.249.0/24', 'cloudfront'),
    ('23.228.250.0/24', 'cloudfront'),
    ('23.228.251.0/24', 'cloudfront'),
    ('23.234.192.0/18', 'cloudfront'),
    ('23.91.0.0/19', 'cloudfront'),
    ('24.110.128.0/17', 'cloudfront'),
    ('24.110.32.0/19', 'cloudfront'),
    ('2400:7fc0:500::/40', 'cloudfront'),
    ('2404:c2c0:500::/40', 'cloudfront'),
    ('2409:8c00:2421:300::/56', 'cloudfront'),
    ('2409:8c00:2421:400::/56', 'cloudfront'),
    ('2600:9000:1000::/36', 'cloudfront'),
    ('2600:9000:2000::/36', 'cloudfront'),
    ('2600:9000:3000::/36', 'cloudfront'),
    ('2600:9000:4000::/36', 'cloudfront'),
    ('2600:9000:5200::/40', 'cloudfront'),
    ('2600:9000:5308::/45', 'cloudfront'),
    ('2600:9000:5310::/44', 'cloudfront'),
    ('2600:9000:5320::/43', 'cloudfront'),
    ('2600:9000:5340::/42', 'cloudfront'),
    ('2600:9000:5380::/41', 'cloudfront'),
    ('2600:9000:6000::/36', 'cloudfront'),
    ('2600:9000:ddd::/48', 'cloudfront'),
    ('2600:9000:eee::/48', 'cloudfront'),
    ('2600:9000:f000::/38', 'cloudfront'),
    ('2600:9000:f400::/40', 'cloudfront'),
    ('2600:9000:f500::/43', 'cloudfront'),
    ('2600:9000:f520::/44', 'cloudfront'),
    ('2600:9000:f534::/46', 'cloudfront'),
    ('2600:9000:f538::/45', 'cloudfront'),
    ('2600:9000:f540::/42', 'cloudfront'),
    ('2600:9000:f580::/41', 'cloudfront'),
    ('2600:9000:f600::/39', 'cloudfront'),
    ('2600:9000:f800::/37', 'cloudfront'),
    ('2600:9000:fff::/48', 'cloudfront'),
    ('2600:f0f0:5504::/46', 'cloudfront'),
    ('2600:f0f0:601::/48', 'cloudfront'),
    ('2600:f0f0:602::/47', 'cloudfront'),
    ('2600:f0f0:9::/48', 'cloudfront'),
    ('3.160.0.0/14', 'cloudfront'),
    ('3.164.0.0/18', 'cloudfront'),
    ('3.164.128.0/17', 'cloudfront'),
    ('3.164.64.0/18', 'cloudfront'),
    ('3.165.0.0/16', 'cloudfront'),
    ('3.166.0.0/15', 'cloudfront'),
    ('3.168.0.0/14', 'cloudfront'),
    ('3.172.0.0/18', 'cloudfront'),
    ('3.172.64.0/18', 'cloudfront'),
    ('3.173.0.0/17', 'cloudfront'),
    ('3.173.128.0/18', 'cloudfront'),
    ('3.173.192.0/18', 'cloudfront'),
    ('3.174.0.0/15', 'cloudfront'),
    ('36.103.232.0/25', 'cloudfront'),
    ('36.103.232.128/26', 'cloudfront'),
    ('51.74.192.0/18', 'cloudfront'),
    ('52.124.128.0/17', 'cloudfront'),
    ('52.222.128.0/17', 'cloudfront'),
    ('52.46.0.0/18', 'cloudfront'),
    ('52.82.128.0/19', 'cloudfront'),
    ('52.84.0.0/15', 'cloudfront'),
    ('54.182.0.0/16', 'cloudfront'),
    ('54.192.0.0/16', 'cloudfront'),
    ('54.230.0.0/17', 'cloudfront'),
    ('54.230.128.0/18', 'cloudfront'),
    ('54.230.200.0/21', 'cloudfront'),
    ('54.230.208.0/20', 'cloudfront'),
    ('54.230.224.0/19', 'cloudfront'),
    ('54.239.128.0/18', 'cloudfront'),
    ('54.239.192.0/19', 'cloudfront'),
    ('54.240.128.0/18', 'cloudfront'),
    ('58.254.138.0/25', 'cloudfront'),
    ('58.254.138.128/26', 'cloudfront'),
    ('64.252.128.0/18', 'cloudfront'),
    ('64.252.64.0/18', 'cloudfront'),
    ('65.8.0.0/16', 'cloudfront'),
    ('65.9.0.0/17', 'cloudfront'),
    ('65.9.128.0/18', 'cloudfront'),
    ('70.132.0.0/18', 'cloudfront'),
    ('71.152.0.0/17', 'cloudfront'),
    ('99.84.0.0/16', 'cloudfront'),
    ('99.86.0.0/16', 'cloudfront');

-- Keep the original, scope-bound positive evidence for providers outside the
-- prefix snapshot. This also covers records imported before this migration.
CREATE INDEX asset_imported_cdn_ip ON public.ai_info_gather_record
    (tianqiong_ai.scope_inet(payload->>'value'), target_id, run_id)
    WHERE kind='asset' AND status='imported' AND payload->'cdn'='true'::jsonb
      AND payload->>'type' IN ('ip','service');

CREATE FUNCTION public.tq_inventory_is_cdn(address inet, tenant uuid, org integer, target_filter integer)
RETURNS boolean LANGUAGE sql STABLE PARALLEL SAFE AS $$
 SELECT address IS NOT NULL AND (
    EXISTS (SELECT 1 FROM public.asset_cdn_prefix p WHERE p.network >>= address)
    OR EXISTS (SELECT 1 FROM public.ai_info_gather_record r
      JOIN public.ai_info_gather_run g ON g.id=r.run_id AND g.tenant_id=tenant
      JOIN public.target t ON t.id=r.target_id AND t.deleted_at IS NULL
      WHERE r.kind='asset' AND r.status='imported' AND r.payload->'cdn'='true'::jsonb
        AND r.payload->>'type' IN ('ip','service') AND tianqiong_ai.scope_inet(r.payload->>'value')=address
        AND (target_filter=0 OR t.id=target_filter)
        AND (org=0 OR EXISTS (SELECT 1 FROM public.organization_target ot WHERE ot.organization_id=org AND ot.target_id=t.id)))
 )
$$;

-- Keep named sites and their URLs visible even when their DNS points at a CDN.
CREATE FUNCTION public.tq_inventory_cdn_asset(kind text, label text, tenant uuid, org integer, target_filter integer)
RETURNS boolean LANGUAGE sql STABLE PARALLEL SAFE AS $$
 SELECT CASE WHEN kind='ip' THEN public.tq_inventory_is_cdn(tianqiong_ai.scope_inet(label),tenant,org,target_filter)
   WHEN kind IN ('service','endpoint') AND label ~* '^(https?://)?([0-9]+[.]|\[)' THEN
     public.tq_inventory_is_cdn(tianqiong_ai.scope_inet(tianqiong_ai.scope_host(label)),tenant,org,target_filter)
   ELSE false END
$$;

-- Retire installation-wide outbound destinations. Credentials and subscriptions
-- retain their ownership. Old pending jobs can now age out under retention.
UPDATE public.notification_destination SET enabled=FALSE,updated_at=clock_timestamp() WHERE enabled;
UPDATE public.notification_delivery SET status='failed_terminal',terminal_at=clock_timestamp(),
    lease_owner=NULL,lease_expires_at=NULL,updated_at=clock_timestamp()
 WHERE status IN ('pending','processing','retrying');

-- Task findings keep their own event IDs, filters and authorization. A new
-- inventory event references the real vulnerability instead of a fake task.
ALTER TABLE public.ai_notification_events
    ALTER COLUMN task_id DROP NOT NULL,
    ALTER COLUMN finding_id DROP NOT NULL,
    ADD COLUMN vulnerability_id integer REFERENCES public.vulnerability(id) ON DELETE CASCADE,
    ADD COLUMN conversation_id uuid REFERENCES public.ai_conversation(id) ON DELETE CASCADE,
    ADD COLUMN gather_run_id uuid REFERENCES public.ai_info_gather_run(id) ON DELETE CASCADE,
    ADD CONSTRAINT ai_notification_event_source CHECK (
        (task_id IS NOT NULL AND finding_id IS NOT NULL AND vulnerability_id IS NULL
         AND conversation_id IS NULL AND gather_run_id IS NULL)
        OR (task_id IS NULL AND finding_id IS NULL AND vulnerability_id IS NOT NULL
            AND kind='finding_created' AND num_nonnulls(conversation_id,gather_run_id)<=1)
    );
-- Inventory/chat deletion can cascade into its event. The per-channel receipt
-- must follow that event rather than blocking deletion of the original data.
ALTER TABLE public.ai_notification_deliveries
    DROP CONSTRAINT ai_notification_deliveries_event_id_fkey,
    ADD CONSTRAINT ai_notification_deliveries_event_id_fkey FOREIGN KEY(event_id)
      REFERENCES public.ai_notification_events(event_id) ON DELETE CASCADE;
CREATE INDEX ai_notification_event_inventory ON public.ai_notification_events(vulnerability_id) WHERE vulnerability_id IS NOT NULL;
ALTER FUNCTION tianqiong_ai.outbound_notification_visible(text,integer) RENAME TO outbound_task_notification_visible;
CREATE FUNCTION tianqiong_ai.outbound_notification_visible(p_event text,p_user integer)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM public.ai_notification_events e
   WHERE e.event_id=p_event AND e.owner_user_id=p_user AND CASE WHEN e.vulnerability_id IS NULL THEN
     tianqiong_ai.outbound_task_notification_visible(p_event,p_user)
   ELSE EXISTS(SELECT 1 FROM public.vulnerability v
     JOIN public.target t ON t.id=v.target_id AND t.deleted_at IS NULL
     JOIN public.tenant_membership m ON m.tenant_id=e.tenant_id AND m.user_id=p_user AND m.role IN ('admin','analyst')
     JOIN public.auth_user u ON u.id=m.user_id AND u.is_active
     JOIN public.tenant tenant ON tenant.id=m.tenant_id AND tenant.status='active'
     WHERE v.id=e.vulnerability_id
       AND (e.conversation_id IS NULL OR EXISTS (SELECT 1 FROM public.ai_conversation c
         WHERE c.id=e.conversation_id AND c.tenant_id=e.tenant_id AND c.owner_user_id=p_user
           AND tianqiong_ai.finding_case_review_conversation_visible(c.id)))
       AND (e.gather_run_id IS NULL OR EXISTS (SELECT 1 FROM public.ai_info_gather_run g
         WHERE g.id=e.gather_run_id AND g.tenant_id=e.tenant_id AND g.owner_user_id=p_user))) END)
$$;

CREATE FUNCTION tianqiong_ai.queue_inventory_notification(p_id integer, p_owner integer, p_chat uuid, p_gather uuid, p_name text, p_summary text)
RETURNS void LANGUAGE sql AS $$
 INSERT INTO public.ai_notification_events(event_id,tenant_id,owner_user_id,vulnerability_id,conversation_id,gather_run_id,kind,snapshot)
 SELECT concat('inventory/',CASE WHEN p_chat IS NOT NULL THEN 'chat/'||p_chat::text WHEN p_gather IS NOT NULL THEN 'gather/'||p_gather::text ELSE 'scan' END,'/',v.id,'/',m.user_id),
        m.tenant_id,m.user_id,v.id,p_chat,p_gather,'finding_created',
        jsonb_build_object('kind','finding_created','vulnerability_id',v.id,
          'name',left(COALESCE(NULLIF(p_name,''),NULLIF(v.display_name,''),v.vuln_type),200),
          'vulnclass',left(v.vuln_type,500),'severity',v.severity,
          'summary',left(COALESCE(NULLIF(p_summary,''),t.name),8000),
          'asset_ids',CASE WHEN v.asset_id IS NULL THEN '[]'::jsonb ELSE jsonb_build_array(v.asset_id) END)
 FROM public.vulnerability v JOIN public.target t ON t.id=v.target_id AND t.deleted_at IS NULL
 JOIN public.tenant_membership m ON m.tenant_id='00000000-0000-0000-0000-000000000001' AND m.role IN ('admin','analyst')
 JOIN public.auth_user u ON u.id=m.user_id AND u.is_active
 JOIN public.tenant tenant ON tenant.id=m.tenant_id AND tenant.status='active'
 WHERE v.id=p_id AND (p_owner IS NULL OR m.user_id=p_owner)
   AND EXISTS (SELECT 1 FROM public.ai_notification_channels ch
     WHERE ch.tenant_id=m.tenant_id AND ch.owner_user_id=m.user_id AND ch.enabled AND ch.deleted_at IS NULL)
 ON CONFLICT(event_id) DO NOTHING
$$;

CREATE FUNCTION tianqiong_ai.notify_inventory_insert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM tianqiong_ai.queue_inventory_notification(NEW.id,NULL,NULL,NULL,NEW.display_name,NULL);
 RETURN NEW;
END $$;
CREATE TRIGGER ai_notify_inventory_insert AFTER INSERT ON public.vulnerability FOR EACH ROW
 WHEN (NEW.source NOT IN ('ai','info-gath')) EXECUTE FUNCTION tianqiong_ai.notify_inventory_insert();

-- A source string alone cannot select a private recipient. Only the owning
-- run's actual import receipt can enqueue this kind of alert.
CREATE FUNCTION tianqiong_ai.notify_gather_risk_import() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM tianqiong_ai.queue_inventory_notification(v.id,g.owner_user_id,NULL,g.id,v.display_name,NULL)
 FROM public.ai_info_gather_run g JOIN public.vulnerability v
 ON v.target_id=NEW.target_id AND v.source='info-gath'
 AND v.raw_output->>'runId'=g.id::text AND v.raw_output->>'recordId'=NEW.record_id
 WHERE g.id=NEW.run_id AND g.tenant_id='00000000-0000-0000-0000-000000000001';
 RETURN NEW;
END $$;
CREATE TRIGGER ai_notify_gather_risk_import AFTER UPDATE OF status ON public.ai_info_gather_record FOR EACH ROW
 WHEN (NEW.status='imported' AND OLD.status<>'imported' AND NEW.kind IN ('vulnerability','observation'))
 EXECUTE FUNCTION tianqiong_ai.notify_gather_risk_import();

CREATE FUNCTION tianqiong_ai.notify_chat_report_insert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM tianqiong_ai.queue_inventory_notification(NEW.vulnerability_id,NEW.owner_user_id,NEW.conversation_id,NULL,NEW.report->>'name',NEW.report->>'summary');
 RETURN NEW;
END $$;
CREATE TRIGGER ai_notify_chat_report_insert AFTER INSERT ON public.ai_conversation_vulnerability_report FOR EACH ROW
 EXECUTE FUNCTION tianqiong_ai.notify_chat_report_insert();
COMMIT;

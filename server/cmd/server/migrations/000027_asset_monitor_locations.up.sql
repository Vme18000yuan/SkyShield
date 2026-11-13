BEGIN;
CREATE TABLE public.asset_monitor_ip_location (
 ip TEXT PRIMARY KEY,
 latitude DOUBLE PRECISION,
 longitude DOUBLE PRECISION,
 provider_key TEXT,
 resolved_at TIMESTAMPTZ,
 next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 CONSTRAINT asset_monitor_location_pair CHECK (
  (latitude IS NULL AND longitude IS NULL AND resolved_at IS NULL AND provider_key IS NULL) OR
  (latitude IS NOT NULL AND longitude IS NOT NULL AND provider_key IS NOT NULL AND latitude BETWEEN -90 AND 90 AND longitude BETWEEN -180 AND 180 AND resolved_at IS NOT NULL AND provider_key='freeipapi')
 )
);
CREATE INDEX asset_monitor_location_next_attempt ON public.asset_monitor_ip_location(next_attempt_at);
CREATE INDEX idx_asset_observation_monitor_organization ON public.asset_observation(tenant_id,organization_id,asset_id) WHERE organization_id IS NOT NULL;
COMMIT;

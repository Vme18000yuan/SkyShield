-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses a verified backup restore or an approved forward fix.
BEGIN;
DROP INDEX IF EXISTS public.idx_asset_observation_monitor_organization;
DROP TABLE IF EXISTS public.asset_monitor_ip_location;
COMMIT;

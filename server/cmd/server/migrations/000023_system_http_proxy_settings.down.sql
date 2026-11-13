-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses a verified backup restore or an approved forward fix.
BEGIN;
DROP TABLE IF EXISTS public.system_http_proxy_setting;
COMMIT;

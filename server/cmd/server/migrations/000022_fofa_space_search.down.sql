-- DESTRUCTIVE TEST TEARDOWN ONLY. Production recovery uses backup or forward fix.
BEGIN;
DROP TABLE public.fofa_import;
DROP TABLE public.fofa_search;
DROP TABLE public.fofa_setting;
COMMIT;

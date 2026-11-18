-- DESTRUCTIVE TEST TEARDOWN ONLY.
BEGIN;
DROP TRIGGER tq_register_default_mcp_client ON public.mcp_key;
DROP FUNCTION public.tq_register_default_mcp_client();
DROP TABLE public.mcp_client;
COMMIT;

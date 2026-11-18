BEGIN;

CREATE TABLE public.mcp_client (
    id serial PRIMARY KEY,
    owner_user_id integer NOT NULL REFERENCES public.auth_user(id) ON DELETE CASCADE,
    name varchar(100) NOT NULL CHECK (btrim(name) <> ''),
    default_key_id integer UNIQUE REFERENCES public.mcp_key(id) ON DELETE CASCADE,
    key_digest char(64) UNIQUE,
    enabled boolean NOT NULL DEFAULT true,
    legacy boolean NOT NULL DEFAULT false,
    all_organizations boolean NOT NULL DEFAULT false,
    organization_ids integer[] NOT NULL DEFAULT '{}',
    tools text[] NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (num_nonnulls(default_key_id, key_digest) = 1),
    CHECK (NOT legacy OR default_key_id IS NOT NULL),
    CHECK (NOT all_organizations OR cardinality(organization_ids) = 0)
);
CREATE INDEX mcp_client_owner ON public.mcp_client(owner_user_id, id);

-- Previously issued credentials retain their original protocol until the
-- owner saves a scoped policy. A fresh credential starts with no data grants.
INSERT INTO public.mcp_client(owner_user_id, name, default_key_id, legacy)
SELECT user_id, 'Default', id, true FROM public.mcp_key;
CREATE FUNCTION public.tq_register_default_mcp_client() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO public.mcp_client(owner_user_id, name, default_key_id)
    VALUES (NEW.user_id, 'Default', NEW.id);
    RETURN NEW;
END;
$$;
CREATE TRIGGER tq_register_default_mcp_client AFTER INSERT ON public.mcp_key
FOR EACH ROW EXECUTE FUNCTION public.tq_register_default_mcp_client();
COMMIT;

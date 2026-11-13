BEGIN;
CREATE TABLE public.system_http_proxy_setting (
 singleton_id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (singleton_id = 1),
 enabled BOOLEAN NOT NULL DEFAULT FALSE,
 encrypted_proxy_url BYTEA,
 updated_by INTEGER NOT NULL REFERENCES public.auth_user(id) ON DELETE RESTRICT,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 CONSTRAINT system_http_proxy_enabled_requires_url CHECK (NOT enabled OR encrypted_proxy_url IS NOT NULL)
);
COMMIT;

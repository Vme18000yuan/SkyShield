BEGIN;
CREATE TABLE public.oast_probe (
    id uuid PRIMARY KEY,
    sequence bigserial UNIQUE NOT NULL,
    tenant_id uuid NOT NULL REFERENCES public.tenant(id) ON DELETE CASCADE,
    owner_user_id integer NOT NULL REFERENCES public.auth_user(id) ON DELETE CASCADE,
    conversation_id uuid REFERENCES public.ai_conversation(id) ON DELETE CASCADE,
    task_id bigint REFERENCES public.ai_task(id) ON DELETE CASCADE,
    scope_key text NOT NULL,
    run_id text NOT NULL DEFAULT '',
    request_id text NOT NULL,
    input_hash text NOT NULL,
    token varchar(40) UNIQUE NOT NULL,
    label varchar(200) NOT NULL DEFAULT '',
    http_url text NOT NULL,
    dns_name text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    event_count integer NOT NULL DEFAULT 0 CHECK (event_count BETWEEN 0 AND 200),
    last_event_at timestamptz,
    CHECK (num_nonnulls(conversation_id,task_id)=1),
    UNIQUE(tenant_id,owner_user_id,scope_key,request_id)
);
CREATE INDEX oast_probe_scope ON public.oast_probe(tenant_id,owner_user_id,scope_key,sequence DESC);
CREATE INDEX oast_probe_retention ON public.oast_probe(expires_at);
CREATE TABLE public.oast_event (
    id bigserial PRIMARY KEY,
    probe_id uuid NOT NULL REFERENCES public.oast_probe(id) ON DELETE CASCADE,
    received_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    protocol text NOT NULL CHECK (protocol IN ('http','dns')),
    remote_addr text NOT NULL,
    method text NOT NULL DEFAULT '',
    host text NOT NULL DEFAULT '',
    path text NOT NULL DEFAULT '',
    headers text NOT NULL DEFAULT '',
    body_base64 text NOT NULL DEFAULT '',
    query_name text NOT NULL DEFAULT '',
    query_type text NOT NULL DEFAULT '',
    truncated boolean NOT NULL DEFAULT false
);
CREATE INDEX oast_event_probe_cursor ON public.oast_event(probe_id,id);
COMMIT;

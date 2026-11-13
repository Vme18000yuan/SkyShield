BEGIN;
CREATE TABLE public.ai_mcp_server (
 id UUID PRIMARY KEY,
 tenant_id UUID NOT NULL REFERENCES public.tenant(id) ON DELETE CASCADE,
 name VARCHAR(64) NOT NULL,
 endpoint TEXT NOT NULL,
 enabled BOOLEAN NOT NULL DEFAULT FALSE,
 insecure BOOLEAN NOT NULL DEFAULT FALSE,
 encrypted_headers BYTEA,
 created_by INTEGER NOT NULL REFERENCES public.auth_user(id) ON DELETE RESTRICT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,id),
 UNIQUE(tenant_id,name)
);
CREATE TABLE public.ai_mcp_server_agent (
 tenant_id UUID NOT NULL,
 server_id UUID NOT NULL,
 agent_key VARCHAR(20) NOT NULL CHECK(agent_key IN ('assistant','planner','worker')),
 PRIMARY KEY(server_id,agent_key),
 FOREIGN KEY(tenant_id,server_id) REFERENCES public.ai_mcp_server(tenant_id,id) ON DELETE CASCADE
);
CREATE INDEX idx_ai_mcp_server_agent ON public.ai_mcp_server_agent(tenant_id,agent_key,server_id);
COMMIT;

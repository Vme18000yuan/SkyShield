BEGIN;
CREATE TABLE public.ai_custom_agent (
 tenant_id UUID NOT NULL REFERENCES public.tenant(id) ON DELETE CASCADE,
 agent_key VARCHAR(64) NOT NULL CHECK (agent_key ~ '^[a-z][a-z0-9_]{0,63}$' AND agent_key NOT IN ('assistant','goals','planner','mainagent','worker','auto','pentest')),
 display_name VARCHAR(120) NOT NULL CHECK (btrim(display_name) <> ''),
 description TEXT NOT NULL DEFAULT '' CHECK (octet_length(description) <= 4096),
 system_prompt TEXT NOT NULL DEFAULT '' CHECK (octet_length(system_prompt) <= 65536),
 enabled BOOLEAN NOT NULL DEFAULT TRUE,
 max_turns INTEGER NOT NULL DEFAULT 0 CHECK (max_turns BETWEEN 0 AND 1000),
 run_seconds INTEGER NOT NULL DEFAULT 1200 CHECK (run_seconds BETWEEN 0 AND 86400),
 created_by INTEGER NOT NULL REFERENCES public.auth_user(id) ON DELETE RESTRICT,
 updated_by INTEGER NOT NULL REFERENCES public.auth_user(id) ON DELETE RESTRICT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY (tenant_id, agent_key)
);
CREATE INDEX idx_ai_custom_agent_tenant ON public.ai_custom_agent(tenant_id, created_at, agent_key);

ALTER TABLE public.ai_conversation
 ADD COLUMN agent_key VARCHAR(64) NOT NULL DEFAULT 'assistant'
 CHECK (agent_key ~ '^[a-z][a-z0-9_]{0,63}$');

ALTER TABLE public.ai_skill ADD COLUMN builtin_key VARCHAR(64);
CREATE UNIQUE INDEX idx_ai_skill_builtin_key ON public.ai_skill(tenant_id, builtin_key) WHERE builtin_key IS NOT NULL;

ALTER TABLE public.ai_skill_agent DROP CONSTRAINT IF EXISTS ai_skill_agent_agent_key_check;
ALTER TABLE public.ai_skill_agent ALTER COLUMN agent_key TYPE VARCHAR(64);
ALTER TABLE public.ai_skill_agent ADD CONSTRAINT ai_skill_agent_agent_key_check
 CHECK (agent_key ~ '^[a-z][a-z0-9_]{0,63}$');

ALTER TABLE public.ai_mcp_server_agent DROP CONSTRAINT IF EXISTS ai_mcp_server_agent_agent_key_check;
ALTER TABLE public.ai_mcp_server_agent ALTER COLUMN agent_key TYPE VARCHAR(64);
ALTER TABLE public.ai_mcp_server_agent ADD CONSTRAINT ai_mcp_server_agent_agent_key_check
 CHECK (agent_key ~ '^[a-z][a-z0-9_]{0,63}$');
COMMIT;

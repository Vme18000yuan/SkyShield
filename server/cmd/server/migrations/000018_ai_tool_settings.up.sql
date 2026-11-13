BEGIN;
CREATE TABLE public.ai_agent_tool_setting (
 tenant_id UUID NOT NULL REFERENCES public.tenant(id) ON DELETE CASCADE,
 agent_key VARCHAR(20) NOT NULL CHECK(agent_key IN ('assistant','planner','worker')),
 tool_key VARCHAR(40) NOT NULL CHECK(tool_key IN ('search_platform_assets','insert_assets')),
 enabled BOOLEAN NOT NULL DEFAULT TRUE,
 updated_by INTEGER NOT NULL REFERENCES public.auth_user(id) ON DELETE RESTRICT,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(tenant_id,agent_key,tool_key)
);
COMMIT;

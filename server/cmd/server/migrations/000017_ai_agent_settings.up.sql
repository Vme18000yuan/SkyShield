BEGIN;
CREATE TABLE public.ai_agent_setting (
 tenant_id UUID NOT NULL REFERENCES public.tenant(id) ON DELETE CASCADE,
 agent_key VARCHAR(20) NOT NULL CHECK(agent_key IN ('assistant','planner','worker')),
 enabled BOOLEAN NOT NULL DEFAULT TRUE,
 max_turns INTEGER NOT NULL DEFAULT 0 CHECK(max_turns BETWEEN 0 AND 1000),
 run_seconds INTEGER NOT NULL DEFAULT 0 CHECK(run_seconds BETWEEN 0 AND 86400),
 additional_prompt TEXT NOT NULL DEFAULT '' CHECK(octet_length(additional_prompt)<=32768),
 updated_by INTEGER NOT NULL REFERENCES public.auth_user(id) ON DELETE RESTRICT,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(tenant_id,agent_key)
);
COMMIT;

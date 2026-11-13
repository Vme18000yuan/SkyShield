BEGIN;
CREATE TABLE public.ai_skill (
 id UUID PRIMARY KEY,
 tenant_id UUID NOT NULL REFERENCES public.tenant(id) ON DELETE CASCADE,
 name VARCHAR(120) NOT NULL CHECK(btrim(name)<>''),
 description TEXT NOT NULL DEFAULT '' CHECK(octet_length(description)<=4096),
 instructions TEXT NOT NULL CHECK(octet_length(instructions)<=65536 AND btrim(instructions)<>''),
 enabled BOOLEAN NOT NULL DEFAULT TRUE,
 created_by INTEGER NOT NULL REFERENCES public.auth_user(id) ON DELETE RESTRICT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,id),
 UNIQUE(tenant_id,name)
);
CREATE TABLE public.ai_skill_agent (
 tenant_id UUID NOT NULL,
 skill_id UUID NOT NULL,
 agent_key VARCHAR(20) NOT NULL CHECK(agent_key IN ('assistant','planner','worker')),
 PRIMARY KEY(skill_id,agent_key),
 FOREIGN KEY(tenant_id,skill_id) REFERENCES public.ai_skill(tenant_id,id) ON DELETE CASCADE
);
CREATE INDEX idx_ai_skill_agent ON public.ai_skill_agent(tenant_id,agent_key,skill_id);
COMMIT;

BEGIN;
ALTER TABLE public.ai_conversation ADD COLUMN pinned BOOLEAN NOT NULL DEFAULT FALSE;
CREATE TABLE public.ai_builtin_agent_config (
 tenant_id UUID NOT NULL REFERENCES public.tenant(id) ON DELETE CASCADE,
 agent_key VARCHAR(64) NOT NULL CHECK (agent_key IN ('goals','planner','mainagent','worker','auto','pentest')),
 enabled BOOLEAN NOT NULL DEFAULT TRUE,
 max_turns INTEGER NOT NULL DEFAULT 0 CHECK (max_turns BETWEEN 0 AND 1000),
 run_seconds INTEGER NOT NULL DEFAULT 1200 CHECK (run_seconds BETWEEN 0 AND 86400),
 prompt_template TEXT NOT NULL DEFAULT '' CHECK (octet_length(prompt_template) <= 65536),
 updated_by INTEGER NOT NULL REFERENCES public.auth_user(id) ON DELETE RESTRICT,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY (tenant_id, agent_key)
);
CREATE TABLE public.ai_agent_llm_binding (
 tenant_id UUID NOT NULL REFERENCES public.tenant(id) ON DELETE CASCADE,
 agent_key VARCHAR(64) NOT NULL CHECK (agent_key ~ '^[a-z][a-z0-9_]{0,63}$'),
 profile_id UUID NOT NULL,
 updated_by INTEGER NOT NULL REFERENCES public.auth_user(id) ON DELETE RESTRICT,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY (tenant_id, agent_key),
 FOREIGN KEY (tenant_id, profile_id) REFERENCES public.ai_llm_profile(tenant_id, id) ON DELETE CASCADE
);
CREATE TABLE public.ai_agent_wrapup_config (
 tenant_id UUID NOT NULL REFERENCES public.tenant(id) ON DELETE CASCADE,
 agent_key VARCHAR(64) NOT NULL CHECK (agent_key ~ '^[a-z][a-z0-9_]{0,63}$'),
 wrapup_prompt TEXT NOT NULL DEFAULT '' CHECK (octet_length(wrapup_prompt) <= 32768),
 wrapup_max_turns INTEGER NOT NULL DEFAULT 0 CHECK (wrapup_max_turns BETWEEN 0 AND 1000),
 task_timeout_prompt TEXT NOT NULL DEFAULT '' CHECK (octet_length(task_timeout_prompt) <= 32768),
 task_timeout_max_turns INTEGER NOT NULL DEFAULT 0 CHECK (task_timeout_max_turns BETWEEN 0 AND 1000),
 updated_by INTEGER NOT NULL REFERENCES public.auth_user(id) ON DELETE RESTRICT,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY (tenant_id, agent_key)
);
CREATE TABLE public.ai_agent_prompt_version (
 id BIGSERIAL PRIMARY KEY,
 tenant_id UUID NOT NULL REFERENCES public.tenant(id) ON DELETE CASCADE,
 agent_key VARCHAR(64) NOT NULL CHECK (agent_key ~ '^[a-z][a-z0-9_]{0,63}$'),
 version INTEGER NOT NULL CHECK (version > 0),
 template_text TEXT NOT NULL CHECK (octet_length(template_text) <= 65536),
 updated_by INTEGER NOT NULL REFERENCES public.auth_user(id) ON DELETE RESTRICT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE (tenant_id, agent_key, version)
);
CREATE TABLE public.ai_conversation_llm_binding (
 tenant_id UUID NOT NULL,
 conversation_id UUID NOT NULL,
 owner_user_id INTEGER NOT NULL,
 profile_id UUID NOT NULL,
 PRIMARY KEY (tenant_id, conversation_id),
 FOREIGN KEY (tenant_id, conversation_id, owner_user_id)
   REFERENCES public.ai_conversation(tenant_id, id, owner_user_id) ON DELETE CASCADE,
 FOREIGN KEY (tenant_id, profile_id) REFERENCES public.ai_llm_profile(tenant_id, id) ON DELETE CASCADE
);
CREATE TABLE public.ai_auto_task_action (
 tenant_id UUID NOT NULL REFERENCES public.tenant(id) ON DELETE CASCADE,
 run_id UUID NOT NULL REFERENCES public.ai_run(id) ON DELETE CASCADE,
 tool_use_id VARCHAR(256) NOT NULL CHECK (tool_use_id <> ''),
 request_hash VARCHAR(64) NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
 operation VARCHAR(16) NOT NULL CHECK (operation IN ('spawn','pause')),
 task_id BIGINT REFERENCES public.ai_task(id) ON DELETE SET NULL,
 PRIMARY KEY (tenant_id, run_id, tool_use_id)
);
-- Preserve the prior assistant role grants under ARTEX's canonical name.
INSERT INTO public.ai_skill_agent(tenant_id,skill_id,agent_key)
 SELECT tenant_id,skill_id,'mainagent' FROM public.ai_skill_agent WHERE agent_key='assistant'
 ON CONFLICT DO NOTHING;
INSERT INTO public.ai_mcp_server_agent(tenant_id,server_id,agent_key)
 SELECT tenant_id,server_id,'mainagent' FROM public.ai_mcp_server_agent WHERE agent_key='assistant'
 ON CONFLICT DO NOTHING;
COMMIT;

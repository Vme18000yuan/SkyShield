CREATE TABLE ai_intercept_rule (
 id BIGSERIAL PRIMARY KEY,
 tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
 name TEXT NOT NULL,
 enabled BOOLEAN NOT NULL DEFAULT TRUE,
 priority INTEGER NOT NULL DEFAULT 0,
 match_target TEXT NOT NULL CHECK(match_target IN ('tool_name','tool_input')),
 match_type TEXT NOT NULL CHECK(match_type IN ('string','regex')),
 pattern TEXT NOT NULL,
 action TEXT NOT NULL CHECK(action IN ('allow','deny','ask')),
 message TEXT NOT NULL DEFAULT '',
 timeout_enabled BOOLEAN NOT NULL DEFAULT FALSE,
 timeout_seconds INTEGER NOT NULL DEFAULT 60 CHECK(timeout_seconds BETWEEN 1 AND 2147483647),
 timeout_action TEXT NOT NULL DEFAULT 'deny' CHECK(timeout_action IN ('allow','deny')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,name)
);
CREATE INDEX ai_intercept_rule_order ON ai_intercept_rule(tenant_id,enabled,priority DESC,id);
CREATE TABLE ai_intercept_settings (
 tenant_id UUID PRIMARY KEY REFERENCES tenant(id) ON DELETE CASCADE,
 enabled_tools JSONB NOT NULL DEFAULT '["Bash","WebFetch","web_search","shell_open","shell_send","Write","Edit","MultiEdit"]'::jsonb CHECK(jsonb_typeof(enabled_tools)='array'),
 judge JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(judge)='object'),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE ai_approval (
 id BIGSERIAL PRIMARY KEY,
 tenant_id UUID NOT NULL,
 owner_user_id INTEGER NOT NULL,
 run_id UUID NOT NULL,
 conversation_id UUID REFERENCES ai_conversation(id) ON DELETE SET NULL,
 task_id BIGINT REFERENCES ai_task(id) ON DELETE SET NULL,
 intent_id BIGINT,
 agent_name TEXT NOT NULL DEFAULT '',
 tool_name TEXT NOT NULL,
 tool_use_id TEXT NOT NULL DEFAULT '',
 tool_input JSONB NOT NULL,
 rule_id BIGINT REFERENCES ai_intercept_rule(id) ON DELETE SET NULL,
 rule_name TEXT NOT NULL DEFAULT '',
 decision_source TEXT NOT NULL DEFAULT 'rule',
 status TEXT NOT NULL CHECK(status IN ('pending','allowed','denied','timeout')),
 effective_action TEXT NOT NULL DEFAULT '' CHECK(effective_action IN ('','allow','deny')),
 reason TEXT NOT NULL DEFAULT '',
 timeout_at TIMESTAMPTZ,
 timeout_action TEXT NOT NULL DEFAULT 'deny' CHECK(timeout_action IN ('allow','deny')),
 execution_status TEXT NOT NULL DEFAULT 'pending' CHECK(execution_status IN ('pending','not_executed','succeeded','failed','unknown')),
 execution_output TEXT NOT NULL DEFAULT '',
 audit JSONB NOT NULL DEFAULT '{}'::jsonb,
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 decided_at TIMESTAMPTZ,
 finished_at TIMESTAMPTZ,
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES tenant_membership(tenant_id,user_id) ON DELETE CASCADE
);
CREATE INDEX ai_approval_owner_status ON ai_approval(tenant_id,owner_user_id,status,id DESC);
CREATE INDEX ai_approval_task ON ai_approval(tenant_id,owner_user_id,task_id,id DESC);
CREATE INDEX ai_approval_conversation ON ai_approval(tenant_id,owner_user_id,conversation_id,id DESC);

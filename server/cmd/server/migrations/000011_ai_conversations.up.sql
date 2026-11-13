CREATE TABLE ai_conversation (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL,
    owner_user_id INTEGER NOT NULL,
    title VARCHAR(200) NOT NULL DEFAULT '新对话',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ai_conversation_membership_fk FOREIGN KEY (tenant_id, owner_user_id)
        REFERENCES tenant_membership(tenant_id, user_id) ON DELETE CASCADE,
    CONSTRAINT ai_conversation_scope_unique UNIQUE (tenant_id, id, owner_user_id)
);
CREATE INDEX idx_ai_conversation_owner ON ai_conversation(tenant_id, owner_user_id, updated_at DESC, id);

CREATE TABLE ai_run (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL,
    conversation_id UUID NOT NULL,
    owner_user_id INTEGER NOT NULL,
    status VARCHAR(20) NOT NULL CHECK (status IN ('running', 'completed', 'failed', 'stopped', 'interrupted')),
    lease_owner UUID NOT NULL,
    lease_until TIMESTAMPTZ NOT NULL,
    error_message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMPTZ,
    CONSTRAINT ai_run_conversation_fk FOREIGN KEY (tenant_id, conversation_id, owner_user_id)
        REFERENCES ai_conversation(tenant_id, id, owner_user_id) ON DELETE CASCADE,
    CONSTRAINT ai_run_scope_unique UNIQUE (tenant_id, conversation_id, owner_user_id, id)
);
CREATE UNIQUE INDEX idx_ai_run_one_active ON ai_run(conversation_id) WHERE status='running';
CREATE INDEX idx_ai_run_owner ON ai_run(tenant_id, owner_user_id, conversation_id, created_at DESC);

CREATE TABLE ai_conversation_activity (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL,
    conversation_id UUID NOT NULL,
    owner_user_id INTEGER NOT NULL,
    run_id UUID NOT NULL,
    kind VARCHAR(30) NOT NULL,
    tool VARCHAR(200) NOT NULL DEFAULT '',
    tool_use_id TEXT NOT NULL DEFAULT '',
    is_error BOOLEAN NOT NULL DEFAULT FALSE,
    summary TEXT NOT NULL DEFAULT '',
    detail TEXT NOT NULL DEFAULT '',
    input_tokens INTEGER,
    output_tokens INTEGER,
    cache_read_tokens INTEGER,
    cache_write_tokens INTEGER,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ai_activity_run_fk FOREIGN KEY (tenant_id, conversation_id, owner_user_id, run_id)
        REFERENCES ai_run(tenant_id, conversation_id, owner_user_id, id) ON DELETE CASCADE
);
CREATE INDEX idx_ai_activity_conversation_cursor ON ai_conversation_activity(tenant_id, conversation_id, owner_user_id, id);

-- ARTEX's per-call usage ledger stores metering only, never prompts or replies.
-- Task/conversation/profile identifiers intentionally remain after deletion so
-- historical usage is not silently lost.
CREATE TABLE ai_llm_usage (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenant(id),
    owner_user_id INTEGER NOT NULL,
    task_id BIGINT,
    exploration_id BIGINT,
    conversation_id UUID,
    execution_id UUID,
    worker TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    profile_id UUID,
    profile_name TEXT NOT NULL DEFAULT '',
    duration_ms BIGINT NOT NULL DEFAULT 0 CHECK(duration_ms >= 0),
    input_tokens BIGINT NOT NULL DEFAULT 0 CHECK(input_tokens >= 0),
    output_tokens BIGINT NOT NULL DEFAULT 0 CHECK(output_tokens >= 0),
    cache_read_tokens BIGINT NOT NULL DEFAULT 0 CHECK(cache_read_tokens >= 0),
    cache_write_tokens BIGINT NOT NULL DEFAULT 0 CHECK(cache_write_tokens >= 0),
    status TEXT NOT NULL CHECK(status IN ('ok', 'error', 'interrupted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX ai_llm_usage_owner_time ON ai_llm_usage(tenant_id, owner_user_id, created_at DESC, id DESC);
CREATE INDEX ai_llm_usage_task ON ai_llm_usage(tenant_id, owner_user_id, task_id) WHERE task_id IS NOT NULL;
CREATE INDEX ai_llm_usage_profile_time ON ai_llm_usage(tenant_id, owner_user_id, profile_id, created_at DESC);

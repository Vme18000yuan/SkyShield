-- Detailed model request/response recordings are opt-in and encrypted by the
-- host. Usage metering remains independent of this table and its retention.
CREATE TABLE ai_llm_record (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenant(id),
    owner_user_id INTEGER NOT NULL,
    task_id BIGINT,
    exploration_id BIGINT,
    conversation_id UUID,
    execution_id UUID,
    session_id TEXT NOT NULL DEFAULT '',
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
    truncated BOOLEAN NOT NULL DEFAULT FALSE,
    sealed_payload BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX ai_llm_record_owner_time ON ai_llm_record(tenant_id, owner_user_id, created_at DESC, id DESC);
CREATE INDEX ai_llm_record_task ON ai_llm_record(tenant_id, owner_user_id, task_id, created_at DESC) WHERE task_id IS NOT NULL;
-- A delete advances the generation, preventing a request already in flight
-- from recreating the records that were just removed.
CREATE TABLE ai_llm_record_epoch (
    tenant_id UUID NOT NULL REFERENCES tenant(id),
    owner_user_id INTEGER NOT NULL,
    task_id BIGINT NOT NULL,
    generation BIGINT NOT NULL DEFAULT 0 CHECK(generation >= 0),
    PRIMARY KEY(tenant_id, owner_user_id, task_id)
);

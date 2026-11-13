CREATE TABLE ai_llm_profile (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    name VARCHAR(120) NOT NULL CHECK (btrim(name) <> ''),
    provider VARCHAR(30) NOT NULL CHECK (provider IN ('anthropic', 'openai', 'openai-responses')),
    model VARCHAR(200) NOT NULL CHECK (btrim(model) <> ''),
    base_url TEXT NOT NULL DEFAULT '',
    encrypted_api_key BYTEA NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ai_llm_profile_tenant_id_unique UNIQUE (tenant_id, id),
    CONSTRAINT ai_llm_profile_tenant_name_unique UNIQUE (tenant_id, name)
);
CREATE UNIQUE INDEX idx_ai_llm_profile_default ON ai_llm_profile(tenant_id) WHERE is_default;
CREATE INDEX idx_ai_llm_profile_tenant ON ai_llm_profile(tenant_id, created_at, id);

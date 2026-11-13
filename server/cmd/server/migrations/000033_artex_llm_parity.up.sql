ALTER TABLE ai_llm_profile ADD COLUMN runtime_id BIGSERIAL UNIQUE;
ALTER TABLE ai_llm_profile ADD COLUMN options JSONB NOT NULL DEFAULT '{"streaming":true}'::jsonb CHECK(jsonb_typeof(options)='object');
ALTER TABLE ai_llm_profile ADD COLUMN api_key_hint TEXT NOT NULL DEFAULT '';
ALTER TABLE ai_llm_profile ADD COLUMN encrypted_proxy_url BYTEA;

CREATE TABLE ai_runtime_setting (
 tenant_id UUID PRIMARY KEY REFERENCES tenant(id) ON DELETE CASCADE,
 options JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(options)='object'),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE ai_llm_health (
 tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
 runtime_id BIGINT PRIMARY KEY REFERENCES ai_llm_profile(runtime_id) ON DELETE CASCADE,
 state JSONB NOT NULL CHECK(jsonb_typeof(state)='object'),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE ai_judge_usage (
 tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
 day DATE NOT NULL,
 profile_key TEXT NOT NULL DEFAULT '',
 calls BIGINT NOT NULL DEFAULT 0,
 input_tokens BIGINT NOT NULL DEFAULT 0,
 output_tokens BIGINT NOT NULL DEFAULT 0,
 cache_read_tokens BIGINT NOT NULL DEFAULT 0,
 cache_write_tokens BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY(tenant_id,day,profile_key)
);

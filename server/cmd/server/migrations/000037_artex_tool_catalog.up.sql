CREATE TABLE ai_tool_catalog (
 tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
 key VARCHAR(100) NOT NULL,
 system BOOLEAN NOT NULL,
 description TEXT NOT NULL DEFAULT '',
 schema JSONB NOT NULL DEFAULT '{}',
 agents JSONB NOT NULL DEFAULT '[]',
 enabled BOOLEAN NOT NULL DEFAULT TRUE,
 kind VARCHAR(20) NOT NULL DEFAULT 'builtin' CHECK(kind IN ('builtin','shell','command','script','http')),
 exec JSONB NOT NULL DEFAULT '{}',
 deferred BOOLEAN NOT NULL DEFAULT FALSE,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(tenant_id,key),
 CHECK(jsonb_typeof(schema)='object'),
 CHECK(jsonb_typeof(agents)='array'),
 CHECK(jsonb_typeof(exec)='object')
);
CREATE TABLE ai_runtime_secret (
 tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
 key VARCHAR(100) NOT NULL,
 encrypted_value BYTEA NOT NULL,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(tenant_id,key)
);

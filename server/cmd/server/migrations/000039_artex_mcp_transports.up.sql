ALTER TABLE ai_mcp_server ADD COLUMN transport VARCHAR(16) NOT NULL DEFAULT 'http' CHECK(transport IN ('http','sse','stdio'));
ALTER TABLE ai_mcp_server ADD COLUMN command TEXT NOT NULL DEFAULT '';
ALTER TABLE ai_mcp_server ADD COLUMN args JSONB NOT NULL DEFAULT '[]';
ALTER TABLE ai_mcp_server ADD COLUMN encrypted_env BYTEA;
ALTER TABLE ai_mcp_server ADD COLUMN tools_cache JSONB NOT NULL DEFAULT '[]';

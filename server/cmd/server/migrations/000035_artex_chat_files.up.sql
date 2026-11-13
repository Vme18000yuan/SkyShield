CREATE TABLE ai_chat_attachment (
 id UUID PRIMARY KEY,
 tenant_id UUID NOT NULL,
 owner_user_id INTEGER NOT NULL,
 scope VARCHAR(16) NOT NULL CHECK(scope IN ('session','task','staging')),
 scope_id VARCHAR(128) NOT NULL,
 name TEXT NOT NULL,
 path TEXT NOT NULL,
 size BIGINT NOT NULL CHECK(size>=0 AND size<=134217728),
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES tenant_membership(tenant_id,user_id) ON DELETE CASCADE,
 UNIQUE(tenant_id,owner_user_id,scope,scope_id,path)
);
CREATE INDEX idx_ai_chat_attachment_scope ON ai_chat_attachment(tenant_id,owner_user_id,scope,scope_id);
CREATE INDEX idx_ai_conversation_history_reverse ON ai_conversation_activity(tenant_id,conversation_id,owner_user_id,id DESC);

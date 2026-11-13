CREATE TABLE ai_side_session (
 id BIGSERIAL PRIMARY KEY,
 tenant_id UUID NOT NULL,
 owner_user_id INTEGER NOT NULL,
 parent_key TEXT NOT NULL,
 conversation_id UUID,
 task_id BIGINT,
 generation BIGINT NOT NULL DEFAULT 1,
 snapshot JSONB,
 memory JSONB NOT NULL DEFAULT '{}',
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES tenant_membership(tenant_id,user_id) ON DELETE CASCADE,
 FOREIGN KEY(tenant_id,conversation_id,owner_user_id) REFERENCES ai_conversation(tenant_id,id,owner_user_id) ON DELETE CASCADE,
 FOREIGN KEY(task_id) REFERENCES ai_task(id) ON DELETE CASCADE,
 CHECK((conversation_id IS NULL) <> (task_id IS NULL)),
 UNIQUE(tenant_id,owner_user_id,parent_key)
);
CREATE TABLE ai_side_exchange (
 id UUID PRIMARY KEY,
 session_id BIGINT NOT NULL REFERENCES ai_side_session(id) ON DELETE CASCADE,
 generation BIGINT NOT NULL,
 client_request_id VARCHAR(128) NOT NULL,
 ordinal BIGSERIAL UNIQUE NOT NULL,
 status VARCHAR(20) NOT NULL CHECK(status IN ('running','completed','failed','cancelled','interrupted')),
 state JSONB NOT NULL,
 lease_owner UUID NOT NULL,
 lease_until TIMESTAMPTZ NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(session_id,generation,client_request_id)
);
CREATE UNIQUE INDEX idx_ai_side_one_running ON ai_side_exchange(session_id) WHERE status='running';
CREATE INDEX idx_ai_side_history ON ai_side_exchange(session_id,generation,ordinal DESC);

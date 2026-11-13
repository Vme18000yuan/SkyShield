-- Information-gathering results remain available after a conversation ends.
-- Unmatched observations are held for explicit target selection.
CREATE TABLE ai_info_gather_run (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL,
    owner_user_id INTEGER NOT NULL,
    execution_id UUID NOT NULL,
    tool_use_id TEXT NOT NULL CHECK (length(tool_use_id) BETWEEN 1 AND 200),
    target TEXT NOT NULL CHECK (length(target) BETWEEN 1 AND 300),
    target_kind TEXT NOT NULL CHECK (target_kind IN ('HOST','IP','CIDR')),
    mode TEXT NOT NULL CHECK (mode IN ('fast','normal','deep')),
    status TEXT NOT NULL CHECK (status IN ('running','completed','partial','failed','interrupted')),
    error_message TEXT NOT NULL DEFAULT '',
    coverage JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(coverage)='object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT ai_info_gather_owner_fk FOREIGN KEY (tenant_id, owner_user_id)
        REFERENCES tenant_membership(tenant_id, user_id) ON DELETE CASCADE,
    UNIQUE (tenant_id, owner_user_id, execution_id, tool_use_id)
);
CREATE INDEX ai_info_gather_run_owner ON ai_info_gather_run(tenant_id,owner_user_id,created_at DESC,id);

CREATE TABLE ai_info_gather_record (
    run_id UUID NOT NULL REFERENCES ai_info_gather_run(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('asset','vulnerability','observation')),
    record_id TEXT NOT NULL CHECK (record_id ~ '^[a-f0-9]{64}$'),
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload)='object'),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','imported','ignored')),
    target_id INTEGER REFERENCES target(id) ON DELETE SET NULL,
    reason TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(run_id,kind,record_id)
);
CREATE INDEX ai_info_gather_record_pending ON ai_info_gather_record(run_id,kind,record_id) WHERE status='pending';

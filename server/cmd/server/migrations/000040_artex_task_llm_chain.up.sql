ALTER TABLE ai_task ADD COLUMN active_llm_profile_id UUID REFERENCES ai_llm_profile(id) ON DELETE SET NULL;
ALTER TABLE ai_task ADD COLUMN llm_chain_revision BIGINT NOT NULL DEFAULT 0;
CREATE TABLE ai_task_llm_profile (
 tenant_id UUID NOT NULL,
 task_id BIGINT NOT NULL,
 profile_id UUID NOT NULL,
 position INTEGER NOT NULL CHECK(position>=0 AND position<50),
 status VARCHAR(20) NOT NULL DEFAULT 'ready' CHECK(status IN ('ready','quota_exhausted')),
 last_error TEXT NOT NULL DEFAULT '',
 exhausted_at TIMESTAMPTZ,
 PRIMARY KEY(task_id,profile_id),
 UNIQUE(task_id,position),
 FOREIGN KEY(tenant_id,task_id) REFERENCES ai_task(tenant_id,id) ON DELETE CASCADE,
 FOREIGN KEY(tenant_id,profile_id) REFERENCES ai_llm_profile(tenant_id,id) ON DELETE CASCADE
);
CREATE INDEX idx_ai_task_llm_profile ON ai_task_llm_profile(tenant_id,profile_id,task_id);
CREATE TABLE ai_task_llm_history (
 id BIGSERIAL PRIMARY KEY,
 tenant_id UUID NOT NULL,
 task_id BIGINT NOT NULL,
 owner_user_id INTEGER NOT NULL,
 mode VARCHAR(20) NOT NULL CHECK(mode IN ('manual','automatic','exhausted')),
 previous_profile_id UUID,
 next_profile_id UUID,
 reason TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 FOREIGN KEY(tenant_id,task_id) REFERENCES ai_task(tenant_id,id) ON DELETE CASCADE,
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES tenant_membership(tenant_id,user_id) ON DELETE CASCADE
);
CREATE INDEX idx_ai_task_llm_history ON ai_task_llm_history(tenant_id,task_id,id DESC);

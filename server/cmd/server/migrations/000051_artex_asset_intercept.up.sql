CREATE TABLE ai_asset_intercept_rule (
 id BIGSERIAL PRIMARY KEY,
 tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
 task_id BIGINT REFERENCES ai_task(id) ON DELETE CASCADE,
 owner_user_id INTEGER,
 enabled BOOLEAN NOT NULL DEFAULT TRUE,
 action TEXT NOT NULL CHECK(action IN ('block','allow')),
 kind TEXT NOT NULL CHECK(kind IN ('exact_domain','exact_ip','exact_url','fuzzy_domain','fuzzy_ip','fuzzy_url','cidr')),
 pattern TEXT NOT NULL CHECK(length(pattern) BETWEEN 1 AND 8192),
 note TEXT NOT NULL DEFAULT '',
 builtin BOOLEAN NOT NULL DEFAULT FALSE,
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES tenant_membership(tenant_id,user_id) ON DELETE CASCADE,
 FOREIGN KEY(tenant_id,task_id) REFERENCES ai_task(tenant_id,id) ON DELETE CASCADE,
 CHECK((task_id IS NULL AND owner_user_id IS NULL AND action='block') OR (task_id IS NOT NULL AND owner_user_id IS NOT NULL AND NOT builtin))
);
CREATE INDEX ai_asset_intercept_scope ON ai_asset_intercept_rule(tenant_id,task_id,id);
CREATE FUNCTION tianqiong_ai.guard_asset_intercept_owner() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.task_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM public.ai_task t WHERE t.id=NEW.task_id AND t.tenant_id=NEW.tenant_id AND t.owner_user_id=NEW.owner_user_id AND t.deleted_at IS NULL) THEN
  RAISE EXCEPTION 'AI asset rule task scope rejected';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER tq_guard_asset_intercept_owner BEFORE INSERT OR UPDATE ON ai_asset_intercept_rule FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_asset_intercept_owner();

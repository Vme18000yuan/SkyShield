CREATE TABLE ai_task_category (
 id BIGSERIAL PRIMARY KEY,
 tenant_id UUID NOT NULL,
 owner_user_id INTEGER NOT NULL,
 name TEXT NOT NULL,
 nkey TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES tenant_membership(tenant_id,user_id) ON DELETE CASCADE,
 UNIQUE(tenant_id,owner_user_id,nkey)
);
ALTER TABLE ai_task ADD COLUMN category_id BIGINT REFERENCES ai_task_category(id) ON DELETE SET NULL;
ALTER TABLE ai_task ADD COLUMN remark TEXT NOT NULL DEFAULT '';
ALTER TABLE ai_task ADD COLUMN pinned_at TIMESTAMPTZ;
CREATE INDEX ai_task_category_owner ON ai_task(tenant_id,owner_user_id,category_id,id) WHERE deleted_at IS NULL;
CREATE INDEX ai_task_pinned_owner ON ai_task(tenant_id,owner_user_id,pinned_at DESC,id DESC) WHERE deleted_at IS NULL;
CREATE TABLE ai_task_template (
 id BIGSERIAL PRIMARY KEY,
 tenant_id UUID NOT NULL,
 owner_user_id INTEGER NOT NULL,
 name TEXT NOT NULL,
 nkey TEXT NOT NULL,
 description TEXT NOT NULL,
 goal TEXT NOT NULL,
 remark TEXT NOT NULL DEFAULT '',
 category_id BIGINT REFERENCES ai_task_category(id) ON DELETE SET NULL,
 intercept_rules JSONB NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(intercept_rules)='array'),
 worker_count INTEGER NOT NULL DEFAULT 0 CHECK(worker_count BETWEEN 0 AND 50),
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES tenant_membership(tenant_id,user_id) ON DELETE CASCADE,
 UNIQUE(tenant_id,owner_user_id,nkey)
);
CREATE FUNCTION tianqiong_ai.guard_task_category_owner() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.category_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM public.ai_task_category c WHERE c.id=NEW.category_id AND c.tenant_id=NEW.tenant_id AND c.owner_user_id=NEW.owner_user_id) THEN
  RAISE EXCEPTION 'AI task category scope rejected';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER tq_task_category_scope BEFORE INSERT OR UPDATE OF category_id,tenant_id,owner_user_id ON ai_task FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_task_category_owner();
CREATE TRIGGER tq_template_category_scope BEFORE INSERT OR UPDATE OF category_id,tenant_id,owner_user_id ON ai_task_template FOR EACH ROW EXECUTE FUNCTION tianqiong_ai.guard_task_category_owner();

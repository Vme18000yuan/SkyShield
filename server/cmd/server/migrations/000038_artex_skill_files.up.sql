ALTER TABLE ai_skill ADD COLUMN license TEXT NOT NULL DEFAULT '';
ALTER TABLE ai_skill DROP CONSTRAINT ai_skill_instructions_check;
ALTER TABLE ai_skill ADD CONSTRAINT ai_skill_instructions_check CHECK(octet_length(instructions)<=20971520);
ALTER TABLE ai_skill ADD COLUMN compatibility TEXT NOT NULL DEFAULT '';
ALTER TABLE ai_skill ADD COLUMN mcps JSONB NOT NULL DEFAULT '[]';
CREATE TABLE ai_skill_file (
 tenant_id UUID NOT NULL,
 skill_id UUID NOT NULL,
 path TEXT NOT NULL,
 content BYTEA NOT NULL DEFAULT '',
 is_dir BOOLEAN NOT NULL DEFAULT FALSE,
 mode INTEGER NOT NULL DEFAULT 420,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(skill_id,path),
 FOREIGN KEY(tenant_id,skill_id) REFERENCES ai_skill(tenant_id,id) ON DELETE CASCADE,
 CHECK(octet_length(content)<=20971520)
);
CREATE TABLE ai_skill_usage (
 id BIGSERIAL PRIMARY KEY,
 tenant_id UUID NOT NULL,
 owner_user_id INTEGER NOT NULL,
 skill TEXT NOT NULL,
 agent_key TEXT NOT NULL,
 task_id BIGINT NOT NULL DEFAULT 0,
 session_id TEXT NOT NULL DEFAULT '',
 args_len INTEGER NOT NULL DEFAULT 0,
 status VARCHAR(20) NOT NULL CHECK(status IN ('loaded','missing')),
 source_kind VARCHAR(40) NOT NULL,
 source_id BIGINT NOT NULL,
 ts TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 FOREIGN KEY(tenant_id,owner_user_id) REFERENCES tenant_membership(tenant_id,user_id) ON DELETE CASCADE,
 UNIQUE(source_kind,source_id)
);
CREATE INDEX idx_ai_skill_usage_name ON ai_skill_usage(tenant_id,skill,ts DESC);
CREATE FUNCTION tq_capture_ai_skill_usage() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_input jsonb;v_tenant uuid;v_owner integer;v_agent text;v_task bigint:=0;v_session text;v_exists boolean;
BEGIN
 IF NEW.kind<>'tool_use' OR NEW.tool<>'Skill' THEN RETURN NEW; END IF;
 BEGIN v_input:=NEW.detail::jsonb;EXCEPTION WHEN invalid_text_representation THEN RETURN NEW;END;
 IF COALESCE(v_input->>'name','')='' THEN RETURN NEW; END IF;
 IF TG_TABLE_NAME='ai_conversation_activity' THEN
  v_tenant:=NEW.tenant_id;v_owner:=NEW.owner_user_id;v_session:=NEW.conversation_id::text;
  SELECT CASE agent_key WHEN 'assistant' THEN 'mainagent' ELSE agent_key END INTO v_agent FROM ai_conversation WHERE id=NEW.conversation_id;
 ELSE
  SELECT tenant_id,owner_user_id,id INTO v_tenant,v_owner,v_task FROM ai_task WHERE exploration_id=NEW.exploration_id;
  v_agent:=CASE WHEN NEW.worker LIKE 'work%' THEN 'worker' ELSE NEW.worker END;v_session:=COALESCE(NEW.worker,'');
 END IF;
 IF v_tenant IS NULL THEN RETURN NEW;END IF;
 SELECT EXISTS(SELECT 1 FROM ai_skill s JOIN ai_skill_agent a ON a.skill_id=s.id AND a.tenant_id=s.tenant_id WHERE s.tenant_id=v_tenant AND s.name=v_input->>'name' AND s.enabled AND a.agent_key=v_agent) INTO v_exists;
 INSERT INTO ai_skill_usage(tenant_id,owner_user_id,skill,agent_key,task_id,session_id,args_len,status,source_kind,source_id,ts)
 VALUES(v_tenant,v_owner,v_input->>'name',COALESCE(v_agent,''),v_task,COALESCE(v_session,''),length(COALESCE(v_input->>'args','')),CASE WHEN v_exists THEN 'loaded' ELSE 'missing' END,TG_TABLE_NAME,NEW.id,NEW.created_at)
 ON CONFLICT(source_kind,source_id) DO NOTHING;
 RETURN NEW;
END $$;
CREATE TRIGGER tq_ai_chat_skill_usage AFTER INSERT ON ai_conversation_activity FOR EACH ROW EXECUTE FUNCTION tq_capture_ai_skill_usage();
CREATE TRIGGER tq_ai_task_skill_usage AFTER INSERT ON tianqiong_ai.activity FOR EACH ROW EXECUTE FUNCTION tq_capture_ai_skill_usage();

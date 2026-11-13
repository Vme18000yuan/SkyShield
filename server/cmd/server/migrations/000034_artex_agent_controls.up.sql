ALTER TABLE ai_custom_agent ADD COLUMN preset_key VARCHAR(64);
CREATE TABLE ai_agent_option (
 tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
 agent_key VARCHAR(64) NOT NULL,
 options JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(options)='object'),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(tenant_id,agent_key)
);
CREATE TABLE ai_runtime_seed (
 tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
 seed_key TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(tenant_id,seed_key)
);
CREATE TABLE ai_agent_trigger (
 id BIGSERIAL PRIMARY KEY,
 tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
 agent_key VARCHAR(64) NOT NULL,
 owner_user_id INTEGER NOT NULL REFERENCES auth_user(id) ON DELETE RESTRICT,
 enabled BOOLEAN NOT NULL DEFAULT TRUE,
 config JSONB NOT NULL CHECK(jsonb_typeof(config)='object'),
 last_fire TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_ai_agent_trigger_agent ON ai_agent_trigger(tenant_id,agent_key,id);
CREATE TABLE ai_agent_trigger_job (
 id BIGSERIAL PRIMARY KEY,
 tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
 trigger_id BIGINT REFERENCES ai_agent_trigger(id) ON DELETE SET NULL,
 agent_key VARCHAR(64) NOT NULL,
 owner_user_id INTEGER NOT NULL REFERENCES auth_user(id) ON DELETE RESTRICT,
 source_task_id BIGINT REFERENCES ai_task(id) ON DELETE SET NULL,
 event_key TEXT NOT NULL,
 title TEXT NOT NULL,
 message TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','starting','running','completed','failed','cancelled')),
 conversation_id UUID REFERENCES ai_conversation(id) ON DELETE SET NULL,
 run_id UUID REFERENCES ai_run(id) ON DELETE SET NULL,
 error_message TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,event_key)
);
CREATE INDEX idx_ai_agent_trigger_job_dispatch ON ai_agent_trigger_job(tenant_id,status,agent_key,owner_user_id,id);
CREATE TABLE ai_agent_event_cursor (
 tenant_id UUID PRIMARY KEY REFERENCES tenant(id) ON DELETE CASCADE,
 event_id BIGINT NOT NULL DEFAULT 0
);
CREATE TABLE ai_agent_event (
 id BIGSERIAL PRIMARY KEY,
 tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
 owner_user_id INTEGER NOT NULL REFERENCES auth_user(id) ON DELETE RESTRICT,
 task_id BIGINT NOT NULL REFERENCES ai_task(id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK(kind IN ('finding','goal_met','task_timeout','tool_call','task_create')),
 event_key TEXT NOT NULL UNIQUE,
 payload JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_ai_agent_event_cursor ON ai_agent_event(tenant_id,id);

CREATE FUNCTION tq_capture_ai_agent_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE task public.ai_task%ROWTYPE; event_kind TEXT; event_identity TEXT; body JSONB; call_tool TEXT; call_input TEXT;
BEGIN
 IF TG_TABLE_SCHEMA='public' AND TG_TABLE_NAME='ai_task' THEN
  task:=NEW;
  IF TG_OP='INSERT' THEN event_kind:='task_create';event_identity:='created:'||NEW.id;
  ELSIF NEW.status='timeout' AND OLD.status IS DISTINCT FROM NEW.status THEN event_kind:='task_timeout';event_identity:='timeout:'||NEW.id;
  ELSE RETURN NULL; END IF;
  body:='{}'::jsonb;
 ELSIF TG_TABLE_NAME='exploration_nodes' THEN
  SELECT * INTO task FROM public.ai_task WHERE exploration_id=NEW.exploration_id AND deleted_at IS NULL;
  IF NOT FOUND THEN RETURN NULL; END IF;
  IF TG_OP='INSERT' AND NEW.kind='finding' THEN event_kind:='finding';event_identity:='finding:'||NEW.id;
  ELSIF NEW.kind='goal' AND NEW.state='met' AND (TG_OP='INSERT' OR OLD.state IS DISTINCT FROM NEW.state) THEN event_kind:='goal_met';event_identity:='goal:'||NEW.id;
  ELSE RETURN NULL; END IF;
  body:=jsonb_build_object('node_id',NEW.id,'summary',left(COALESCE(NEW.payload->>'summary',NEW.payload->>'text',''),4096),'severity',NEW.payload->>'severity','vulnclass',NEW.payload->>'vulnclass');
 ELSE
  IF NEW.kind<>'tool_result' THEN RETURN NULL; END IF;
  SELECT * INTO task FROM public.ai_task WHERE exploration_id=NEW.exploration_id AND deleted_at IS NULL;
  IF NOT FOUND THEN RETURN NULL; END IF;
  SELECT tool,detail INTO call_tool,call_input FROM tianqiong_ai.activity a WHERE a.exploration_id=NEW.exploration_id AND a.execution_id IS NOT DISTINCT FROM NEW.execution_id
   AND a.worker IS NOT DISTINCT FROM NEW.worker AND a.node_id IS NOT DISTINCT FROM NEW.node_id AND a.tool_use_id=NEW.tool_use_id AND a.kind='tool_use' AND a.id<NEW.id ORDER BY a.id DESC LIMIT 1;
  call_tool:=COALESCE(NULLIF(NEW.tool,''),call_tool,'');
  IF call_tool='' THEN RETURN NULL; END IF;
  event_kind:='tool_call';event_identity:='tool:'||NEW.id;
  body:=jsonb_build_object('activity_id',NEW.id,'tool',call_tool,'input',left(COALESCE(call_input,''),16384),'output',left(COALESCE(NEW.detail,''),16384),'is_error',NEW.is_error);
 END IF;
 IF task.deleted_at IS NOT NULL THEN RETURN NULL; END IF;
 body:=body||jsonb_build_object('task_id',task.id,'task_description',left(task.description,2048),'task_goal',left(task.goal,2048));
 INSERT INTO public.ai_agent_event(tenant_id,owner_user_id,task_id,kind,event_key,payload) VALUES(task.tenant_id,task.owner_user_id,task.id,event_kind,event_identity,body) ON CONFLICT(event_key) DO NOTHING;
 RETURN NULL;
END;
$$;
CREATE TRIGGER tq_ai_task_agent_event AFTER INSERT OR UPDATE OF status ON ai_task FOR EACH ROW EXECUTE FUNCTION tq_capture_ai_agent_event();
CREATE TRIGGER tq_ai_node_agent_event AFTER INSERT OR UPDATE OF state ON tianqiong_ai.exploration_nodes FOR EACH ROW EXECUTE FUNCTION tq_capture_ai_agent_event();
CREATE TRIGGER tq_ai_tool_agent_event AFTER INSERT ON tianqiong_ai.activity FOR EACH ROW EXECUTE FUNCTION tq_capture_ai_agent_event();

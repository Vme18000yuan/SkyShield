-- ARTEX exploration graph schema, adapted from commit e740334b9042d6da64156b0adb37a293117e140e.
-- This namespace preserves mature graph SQL; all asset anchors reference the
-- one public.asset_entity registry. No ARTEX assets table or db.Open is used.
BEGIN;
CREATE SCHEMA tianqiong_ai;
SET LOCAL search_path TO tianqiong_ai,public;
CREATE FUNCTION tianqiong_ai.set_updated_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN NEW.updated_at=clock_timestamp(); RETURN NEW; END;
$$;


CREATE TABLE explorations (
    id          BIGSERIAL PRIMARY KEY,
    description TEXT,
    goal        TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'open'
                  CHECK (status IN ('open','achieved','failed')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE explorations ADD COLUMN round_no BIGINT NOT NULL DEFAULT 0;

CREATE TABLE public.ai_task (
 id BIGSERIAL PRIMARY KEY,
 tenant_id UUID NOT NULL,
 owner_user_id INTEGER NOT NULL,
 name VARCHAR(200) NOT NULL,
 description TEXT NOT NULL DEFAULT '',
 goal TEXT NOT NULL CHECK (btrim(goal)<>''),
 exploration_id BIGINT NOT NULL UNIQUE REFERENCES tianqiong_ai.explorations(id) ON DELETE RESTRICT,
 target_id INTEGER REFERENCES public.target(id) ON DELETE SET NULL,
 original_target_id INTEGER,
 status VARCHAR(20) NOT NULL DEFAULT 'created' CHECK (status IN ('created','running','paused','done','failed','timeout','stopped')),
 queued BOOLEAN NOT NULL DEFAULT TRUE,
 worker_count INTEGER NOT NULL DEFAULT 1 CHECK (worker_count BETWEEN 1 AND 16),
 max_turns INTEGER NOT NULL DEFAULT 30 CHECK (max_turns BETWEEN 1 AND 1000),
 timeout_seconds INTEGER NOT NULL DEFAULT 3600 CHECK (timeout_seconds BETWEEN 30 AND 604800),
 active_run UUID NOT NULL,
 lease_owner UUID,
 lease_epoch BIGINT NOT NULL DEFAULT 0,
 lease_until TIMESTAMPTZ,
 first_run_at TIMESTAMPTZ,
 deadline_at TIMESTAMPTZ,
 paused_by_schedule BOOLEAN NOT NULL DEFAULT FALSE,
 schedule_manual_until TIMESTAMPTZ,
 error_message TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 completed_at TIMESTAMPTZ,
 deleted_at TIMESTAMPTZ,
 CONSTRAINT ai_task_membership_fk FOREIGN KEY(tenant_id,owner_user_id) REFERENCES public.tenant_membership(tenant_id,user_id) ON DELETE RESTRICT,
 CONSTRAINT ai_task_tenant_id_unique UNIQUE(tenant_id,id)
);
CREATE INDEX idx_ai_task_owner ON public.ai_task(tenant_id,owner_user_id,id DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_ai_task_dispatch ON public.ai_task(status,queued,lease_until,id) WHERE deleted_at IS NULL;
CREATE TABLE public.ai_task_asset (
 tenant_id UUID NOT NULL,
 task_id BIGINT NOT NULL,
 asset_id BIGINT NOT NULL,
 source VARCHAR(20) NOT NULL DEFAULT 'selected',
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(task_id,asset_id),
 FOREIGN KEY(tenant_id,task_id) REFERENCES public.ai_task(tenant_id,id) ON DELETE CASCADE,
 FOREIGN KEY(tenant_id,asset_id) REFERENCES public.asset_entity(tenant_id,id) ON DELETE RESTRICT
);
CREATE TABLE public.ai_task_relation (
 tenant_id UUID NOT NULL,
 task_id BIGINT NOT NULL,
 source_task_id BIGINT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(task_id,source_task_id),
 FOREIGN KEY(tenant_id,task_id) REFERENCES public.ai_task(tenant_id,id) ON DELETE CASCADE,
 FOREIGN KEY(tenant_id,source_task_id) REFERENCES public.ai_task(tenant_id,id) ON DELETE CASCADE,
 CHECK(task_id<>source_task_id)
);
CREATE TABLE public.ai_task_archive (
    id                         BIGSERIAL PRIMARY KEY,
    task_id                    BIGINT NOT NULL UNIQUE REFERENCES public.ai_task(id) ON DELETE CASCADE,
    state                      TEXT NOT NULL DEFAULT 'archive_queued' CHECK (state IN (
                                   'archive_queued','archiving','archive_failed','ready',
                                   'restore_queued','restoring','restore_failed',
                                   'delete_queued','deleting','delete_failed'
                               )),
    phase                      TEXT NOT NULL DEFAULT 'queued',
    progress                   INTEGER NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    error                      TEXT NOT NULL DEFAULT '',
    warnings                   JSONB NOT NULL DEFAULT '[]',
    format_version             INTEGER NOT NULL DEFAULT 2,
    archive_path               TEXT NOT NULL DEFAULT '',
    sha256                     TEXT NOT NULL DEFAULT '',
    original_size              BIGINT NOT NULL DEFAULT 0,
    compressed_size            BIGINT NOT NULL DEFAULT 0,
    task_name                  TEXT NOT NULL DEFAULT '',
    task_description           TEXT NOT NULL DEFAULT '',
    task_goal                  TEXT NOT NULL DEFAULT '',
    original_status            TEXT NOT NULL DEFAULT '',
    category_id_snapshot       BIGINT,
    category_name_snapshot     TEXT NOT NULL DEFAULT '',
    source_task_ids            BIGINT[] NOT NULL DEFAULT '{}',
    remaining_timeout_seconds  BIGINT NOT NULL DEFAULT 0,
    data_counts                JSONB NOT NULL DEFAULT '{}',
    aggregate_stats            JSONB NOT NULL DEFAULT '{}',
    archived_at                TIMESTAMPTZ,
    requested_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE VIEW task_archives AS SELECT * FROM public.ai_task_archive;
CREATE VIEW tasks AS SELECT * FROM public.ai_task;
CREATE VIEW task_relations AS SELECT * FROM public.ai_task_relation;


CREATE TABLE exploration_nodes (
    id             BIGSERIAL PRIMARY KEY,
    exploration_id BIGINT NOT NULL REFERENCES explorations(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL,
    payload        JSONB NOT NULL DEFAULT '{}',
    priority       INT  NOT NULL DEFAULT 0,
    state          TEXT NOT NULL DEFAULT 'open',
    origin         TEXT,
    owner          TEXT,
    blocked_reason TEXT,
    delete_reason  TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at   TIMESTAMPTZ,
    CONSTRAINT ck_node_kind CHECK (kind IN ('begin','goal','intent','fact','finding','hint','digest')),
    CONSTRAINT ck_node_state CHECK (
        (kind='begin'   AND state IN ('open')) OR
        (kind='intent'  AND state IN ('open','running','paused','done','blocked','exhausted','stopped','deleted')) OR
        (kind='goal'    AND state IN ('open','met','abandoned')) OR
        (kind='fact'    AND state IN ('confirmed','dismissed','origin')) OR
        (kind='finding' AND state IN ('confirmed','dismissed')) OR
        (kind='hint'    AND state IN ('active','consumed')) OR
        (kind='digest'  AND state IN ('active','superseded'))
    )
);

CREATE TABLE exploration_edges (
    exploration_id BIGINT NOT NULL REFERENCES explorations(id) ON DELETE CASCADE,
    src_id         BIGINT NOT NULL REFERENCES exploration_nodes(id) ON DELETE CASCADE,
    dst_id         BIGINT NOT NULL REFERENCES exploration_nodes(id) ON DELETE CASCADE,
    rel            TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (exploration_id, src_id, rel, dst_id),
    CONSTRAINT ck_edge_noself CHECK (src_id <> dst_id),
    CONSTRAINT ck_edge_rel CHECK (rel IN ('spawns','derived_from','yields','proves','covers'))
);

CREATE TABLE exploration_anchors (
    node_id   BIGINT NOT NULL REFERENCES exploration_nodes(id) ON DELETE CASCADE,
    asset_id  BIGINT NOT NULL REFERENCES public.asset_entity(id) ON DELETE CASCADE,
    PRIMARY KEY (node_id, asset_id)
);

CREATE TABLE task_constraints (
    id             BIGSERIAL PRIMARY KEY,
    exploration_id BIGINT NOT NULL REFERENCES explorations(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL CHECK (kind IN ('allow','deny')),
    text           TEXT NOT NULL,
    origin         TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE activity (
    id                 BIGSERIAL PRIMARY KEY,
    exploration_id     BIGINT NOT NULL REFERENCES explorations(id) ON DELETE CASCADE,
    node_id            BIGINT REFERENCES exploration_nodes(id) ON DELETE SET NULL,
    worker             TEXT,
    kind               TEXT,
    tool               TEXT,
    tool_use_id        TEXT,
    is_error           BOOLEAN NOT NULL DEFAULT false,
    summary            TEXT,
    detail             TEXT,
    metadata           JSONB NOT NULL DEFAULT '{}',
    input_tokens       INTEGER,
    output_tokens      INTEGER,
    cache_read_tokens  INTEGER,
    cache_write_tokens INTEGER,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE main_sessions (
    exploration_id BIGINT NOT NULL REFERENCES explorations(id) ON DELETE CASCADE,
    seq            INTEGER NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (exploration_id, seq)
);

CREATE TABLE findings (
    id          BIGSERIAL PRIMARY KEY,
    task_id     BIGINT REFERENCES public.ai_task(id) ON DELETE SET NULL,
    node_id     BIGINT REFERENCES exploration_nodes(id) ON DELETE SET NULL,
    vulnclass   TEXT NOT NULL DEFAULT '',
    -- 漏洞名称(可读标题)；为空时前端回退展示 vulnclass。severity 取值：
    -- critical 严重 / high 高 / medium 中 / low 低（不加 CHECK，与 status 一致由 server 白名单校验）。
    name        TEXT NOT NULL DEFAULT '',
    severity    TEXT NOT NULL DEFAULT '',
    summary     TEXT NOT NULL DEFAULT '',
    evidence    TEXT NOT NULL DEFAULT '',
    worker      TEXT NOT NULL DEFAULT '',
    asset_ids   JSONB NOT NULL DEFAULT '[]',
    -- 处置状态：pending 待处理 / in_progress 处理中 / confirmed 已确认 / resolved 已处理 / fixed 已修复 /
    -- false_positive 误报 / ignored 忽略 / duplicate 重复 / risk_accepted 风险接受。
    -- 取值不加 CHECK：旧库靠下面的 ALTER 补列,CHECK 无法回填,统一由 server 侧白名单校验。
    status      TEXT NOT NULL DEFAULT 'pending',
    -- 漏洞详细报告(Markdown)；默认空,仅详情页读取/展示,不进列表接口以免 payload 膨胀。
    report      TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE traffic_evidence_snapshots (
    id TEXT PRIMARY KEY,
    source_traffic_id TEXT NOT NULL,
    captured_at BIGINT NOT NULL,
    url TEXT NOT NULL,
    method TEXT NOT NULL,
    status INTEGER NOT NULL,
    content_type TEXT NOT NULL DEFAULT '',
    req_head TEXT NOT NULL,
    resp_head TEXT NOT NULL,
    req_hash TEXT NOT NULL,
    resp_hash TEXT NOT NULL,
    req_len BIGINT NOT NULL,
    resp_len BIGINT NOT NULL,
    unreferenced_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE finding_traffic_bindings (
    id BIGSERIAL PRIMARY KEY,
    finding_id BIGINT NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
    snapshot_id TEXT NOT NULL REFERENCES traffic_evidence_snapshots(id),
    role TEXT NOT NULL DEFAULT 'supporting',
    note TEXT NOT NULL DEFAULT '',
    position INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(finding_id, snapshot_id)
);

CREATE TABLE task_intelligence (
    id BIGSERIAL PRIMARY KEY,
    task_id BIGINT NOT NULL REFERENCES public.ai_task(id) ON DELETE CASCADE,
    node_id BIGINT REFERENCES exploration_nodes(id) ON DELETE SET NULL,
    kind TEXT NOT NULL CHECK (kind IN ('credential','lead','sensitive_data')),
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    evidence TEXT NOT NULL DEFAULT '',
    confidence TEXT NOT NULL CHECK (confidence IN ('observed','inferred')),
    status TEXT NOT NULL,
    asset_ids JSONB NOT NULL DEFAULT '[]',
    worker TEXT NOT NULL DEFAULT '',
    fingerprint TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(task_id,kind,fingerprint)
);

ALTER TABLE exploration_nodes ADD COLUMN content_version INTEGER NOT NULL DEFAULT 0,
 ADD COLUMN cold_since_round BIGINT;
ALTER TABLE activity ADD COLUMN main_seg INTEGER;
ALTER TABLE findings ADD COLUMN evidence_version BIGINT NOT NULL DEFAULT 0,
 ADD COLUMN report_evidence_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE task_intelligence ADD COLUMN credential_analysis JSONB;
CREATE INDEX idx_tq_nodes_kind ON exploration_nodes(exploration_id,kind,id);
CREATE INDEX idx_tq_intent_frontier ON exploration_nodes(exploration_id,priority DESC,id) WHERE kind='intent' AND state='open';
CREATE INDEX idx_tq_edges_src ON exploration_edges(exploration_id,src_id,rel);
CREATE INDEX idx_tq_edges_dst ON exploration_edges(exploration_id,dst_id,rel);
CREATE INDEX idx_tq_anchor_asset ON exploration_anchors(asset_id,node_id);
CREATE INDEX idx_tq_activity_cursor ON activity(exploration_id,id);
CREATE INDEX idx_tq_activity_tool ON activity(exploration_id,tool_use_id,id);
CREATE INDEX idx_tq_finding_task ON findings(task_id,id);
CREATE TRIGGER tq_graph_exp_updated BEFORE UPDATE ON explorations FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER tq_graph_node_updated BEFORE UPDATE ON exploration_nodes FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER tq_graph_constraint_updated BEFORE UPDATE ON task_constraints FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE FUNCTION tianqiong_ai.assert_task_write(p_exploration BIGINT,p_asset BIGINT DEFAULT NULL)
RETURNS public.ai_task LANGUAGE plpgsql AS $$
DECLARE
 v_task public.ai_task%ROWTYPE;
 v_id BIGINT;
 v_owner INTEGER;
 v_run UUID;
 v_mode TEXT;
BEGIN
 v_id:=NULLIF(current_setting('tianqiong.task_id',TRUE),'')::BIGINT;
 v_owner:=NULLIF(current_setting('tianqiong.owner_user_id',TRUE),'')::INTEGER;
 v_run:=NULLIF(current_setting('tianqiong.execution_id',TRUE),'')::UUID;
 v_mode:=current_setting('tianqiong.execution_mode',TRUE);
 SELECT task.* INTO v_task FROM public.ai_task task
 JOIN public.tenant_membership m ON m.tenant_id=task.tenant_id AND m.user_id=task.owner_user_id
 JOIN public.auth_user u ON u.id=m.user_id JOIN public.tenant tenant ON tenant.id=m.tenant_id
 WHERE task.id=v_id AND task.owner_user_id=v_owner AND task.exploration_id=p_exploration
 AND task.active_run=v_run AND task.deleted_at IS NULL AND u.is_active=TRUE
 AND tenant.status='active' AND m.role IN ('admin','analyst')
 FOR SHARE OF task,m,u;
 IF NOT FOUND THEN RAISE EXCEPTION 'AI task execution scope rejected'; END IF;
 IF v_mode='setup' THEN
   IF v_task.status<>'created' OR v_task.lease_epoch<>0 THEN RAISE EXCEPTION 'AI task setup scope rejected'; END IF;
 ELSIF v_task.status<>'running' OR v_task.lease_until IS NULL OR v_task.lease_until<=clock_timestamp() THEN
   RAISE EXCEPTION 'AI task execution lease rejected';
 END IF;
 IF v_task.original_target_id IS NOT NULL AND NOT EXISTS(
   SELECT 1 FROM public.target WHERE id=v_task.target_id AND deleted_at IS NULL
 ) THEN RAISE EXCEPTION 'AI task target is inactive'; END IF;
 IF p_asset IS NOT NULL THEN
   IF NOT EXISTS(SELECT 1 FROM public.asset_entity a WHERE a.id=p_asset AND a.tenant_id=v_task.tenant_id) THEN
     RAISE EXCEPTION 'AI task asset scope rejected';
   END IF;
   IF v_task.target_id IS NOT NULL AND NOT (
     EXISTS(SELECT 1 FROM public.asset_website_binding b JOIN public.website a ON a.id=b.website_id WHERE b.asset_id=p_asset AND a.target_id=v_task.target_id) OR
     EXISTS(SELECT 1 FROM public.asset_endpoint_binding b JOIN public.endpoint a ON a.id=b.endpoint_id WHERE b.asset_id=p_asset AND a.target_id=v_task.target_id) OR
     EXISTS(SELECT 1 FROM public.asset_subdomain_binding b JOIN public.subdomain a ON a.id=b.subdomain_id WHERE b.asset_id=p_asset AND a.target_id=v_task.target_id) OR
     EXISTS(SELECT 1 FROM public.asset_host_port_binding b JOIN public.host_port_mapping a ON a.id=b.host_port_id WHERE b.asset_id=p_asset AND a.target_id=v_task.target_id) OR
     EXISTS(SELECT 1 FROM public.asset_observation o WHERE o.asset_id=p_asset AND o.tenant_id=v_task.tenant_id AND o.target_id=v_task.target_id)
   ) THEN RAISE EXCEPTION 'AI task asset is outside the selected target'; END IF;
 END IF;
 RETURN v_task;
END;
$$;

CREATE FUNCTION tianqiong_ai.guard_graph_write() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_exp BIGINT; v_node BIGINT; v_asset BIGINT; v_task public.ai_task%ROWTYPE;
BEGIN
 IF TG_TABLE_NAME='explorations' THEN
   IF TG_OP='DELETE' THEN v_exp:=OLD.id; ELSE v_exp:=NEW.id; END IF;
 ELSIF TG_TABLE_NAME='exploration_anchors' THEN
   IF TG_OP='DELETE' THEN v_node:=OLD.node_id;v_asset:=OLD.asset_id; ELSE v_node:=NEW.node_id;v_asset:=NEW.asset_id; END IF;
   SELECT exploration_id INTO v_exp FROM tianqiong_ai.exploration_nodes WHERE id=v_node;
 ELSIF TG_TABLE_NAME='findings' THEN
   IF TG_OP='DELETE' THEN v_node:=OLD.node_id; ELSE v_node:=NEW.node_id; END IF;
   SELECT exploration_id INTO v_exp FROM tianqiong_ai.exploration_nodes WHERE id=v_node;
 ELSIF TG_TABLE_NAME='task_intelligence' THEN
   IF TG_OP='DELETE' THEN SELECT exploration_id INTO v_exp FROM public.ai_task WHERE id=OLD.task_id;
   ELSE SELECT exploration_id INTO v_exp FROM public.ai_task WHERE id=NEW.task_id; END IF;
 ELSE
   IF TG_OP='DELETE' THEN v_exp:=OLD.exploration_id; ELSE v_exp:=NEW.exploration_id; END IF;
 END IF;
 v_task:=tianqiong_ai.assert_task_write(v_exp,v_asset);
 IF TG_OP<>'DELETE' AND TG_TABLE_NAME='exploration_edges' THEN
   IF NOT EXISTS(SELECT 1 FROM tianqiong_ai.exploration_nodes WHERE id=NEW.src_id AND exploration_id=v_exp)
      OR NOT EXISTS(SELECT 1 FROM tianqiong_ai.exploration_nodes WHERE id=NEW.dst_id AND exploration_id=v_exp) THEN
      RAISE EXCEPTION 'AI task graph edge crosses exploration boundary';
   END IF;
 END IF;
 IF TG_OP<>'DELETE' AND TG_TABLE_NAME='findings' THEN
   IF NEW.task_id<>v_task.id THEN RAISE EXCEPTION 'AI finding crosses task boundary'; END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;

CREATE TRIGGER tq_graph_exploration_fence BEFORE UPDATE OR DELETE ON explorations FOR EACH ROW EXECUTE FUNCTION guard_graph_write();
CREATE TRIGGER tq_graph_node_fence BEFORE INSERT OR UPDATE OR DELETE ON exploration_nodes FOR EACH ROW EXECUTE FUNCTION guard_graph_write();
CREATE TRIGGER tq_graph_edge_fence BEFORE INSERT OR UPDATE OR DELETE ON exploration_edges FOR EACH ROW EXECUTE FUNCTION guard_graph_write();
CREATE TRIGGER tq_graph_anchor_fence BEFORE INSERT OR UPDATE OR DELETE ON exploration_anchors FOR EACH ROW EXECUTE FUNCTION guard_graph_write();
CREATE TRIGGER tq_graph_constraint_fence BEFORE INSERT OR UPDATE OR DELETE ON task_constraints FOR EACH ROW EXECUTE FUNCTION guard_graph_write();
CREATE TRIGGER tq_graph_activity_fence BEFORE INSERT OR UPDATE OR DELETE ON activity FOR EACH ROW EXECUTE FUNCTION guard_graph_write();
CREATE TRIGGER tq_graph_finding_fence BEFORE INSERT OR UPDATE OR DELETE ON findings FOR EACH ROW EXECUTE FUNCTION guard_graph_write();
CREATE TRIGGER tq_graph_intelligence_fence BEFORE INSERT OR UPDATE OR DELETE ON task_intelligence FOR EACH ROW EXECUTE FUNCTION guard_graph_write();

CREATE TABLE public.ai_finding_risk_link (
 finding_id BIGINT PRIMARY KEY REFERENCES tianqiong_ai.findings(id) ON DELETE CASCADE,
 task_id BIGINT NOT NULL REFERENCES public.ai_task(id) ON DELETE RESTRICT,
 vulnerability_id INTEGER REFERENCES public.vulnerability(id) ON DELETE SET NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE FUNCTION tianqiong_ai.project_finding_risk() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_target INTEGER;v_url TEXT;v_risk INTEGER;v_severity TEXT;
BEGIN
 SELECT target_id INTO v_target FROM public.ai_task WHERE id=NEW.task_id;
 IF v_target IS NULL OR NOT EXISTS(SELECT 1 FROM public.target WHERE id=v_target AND deleted_at IS NULL) THEN RETURN NEW; END IF;
 SELECT a.label INTO v_url FROM exploration_anchors b JOIN public.asset_entity a ON a.id=b.asset_id
 WHERE b.node_id=NEW.node_id AND a.asset_type IN ('service','endpoint') AND a.label ~ '^https?://' ORDER BY a.id LIMIT 1;
 IF v_url IS NULL THEN RETURN NEW; END IF;
 v_severity:=CASE WHEN NEW.severity IN ('critical','high','medium','low','info') THEN NEW.severity ELSE 'unknown' END;
 INSERT INTO public.vulnerability(target_id,url,vuln_type,severity,source,description,raw_output)
 VALUES(v_target,v_url,NEW.vulnclass,v_severity,'ai',NEW.summary,jsonb_build_object('aiTaskId',NEW.task_id::text,'aiFindingId',NEW.id::text,'evidence',NEW.evidence))
 ON CONFLICT(target_id,url,vuln_type,severity) DO NOTHING RETURNING id INTO v_risk;
 IF v_risk IS NULL THEN SELECT id INTO v_risk FROM public.vulnerability WHERE target_id=v_target AND url=v_url AND vuln_type=NEW.vulnclass AND severity=v_severity; END IF;
 INSERT INTO public.ai_finding_risk_link(finding_id,task_id,vulnerability_id) VALUES(NEW.id,NEW.task_id,v_risk) ON CONFLICT DO NOTHING;
 RETURN NEW;
END;
$$;
CREATE TRIGGER tq_graph_finding_risk AFTER INSERT ON findings FOR EACH ROW EXECUTE FUNCTION project_finding_risk();

CREATE FUNCTION public.tq_pause_tasks_for_deleted_target() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL THEN
   UPDATE public.ai_task SET status='paused',queued=FALSE,lease_until=NULL,updated_at=CURRENT_TIMESTAMP,error_message='关联目标已删除'
   WHERE original_target_id=NEW.id AND deleted_at IS NULL AND status IN ('created','running');
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER tq_target_ai_task_cleanup AFTER UPDATE OF deleted_at ON public.target FOR EACH ROW EXECUTE FUNCTION public.tq_pause_tasks_for_deleted_target();
COMMIT;


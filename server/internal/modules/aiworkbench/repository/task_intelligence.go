package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

type TaskIntelligenceStore struct{ db *gorm.DB }

func NewTaskIntelligenceStore(db *gorm.DB) *TaskIntelligenceStore {
	return &TaskIntelligenceStore{db: db}
}

func (s *TaskIntelligenceStore) Enabled(ctx context.Context, user int) (bool, error) {
	var enabled bool
	err := s.db.WithContext(ctx).Raw(`SELECT COALESCE((SELECT options->>'task_intelligence_enabled' FROM ai_runtime_setting WHERE tenant_id=m.tenant_id),'false')='true' FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE m.tenant_id=? AND m.user_id=? AND t.status='active' AND u.is_active`, assets.DefaultTenantID, user).Row().Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, aiapp.ErrTaskNotFound
	}
	return enabled, err
}

func (s *TaskIntelligenceStore) CredentialEnabled(ctx context.Context, user int) (bool, error) {
	var enabled bool
	err := s.db.WithContext(ctx).Raw(`SELECT COALESCE((SELECT options->>'task_intelligence_enabled' FROM ai_runtime_setting WHERE tenant_id=m.tenant_id),'false')='true' AND COALESCE((SELECT options->>'credential_agent_enabled' FROM ai_runtime_setting WHERE tenant_id=m.tenant_id),'false')='true' FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE m.tenant_id=? AND m.user_id=? AND t.status='active' AND u.is_active`, assets.DefaultTenantID, user).Row().Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, aiapp.ErrTaskNotFound
	}
	return enabled, err
}

func (s *TaskIntelligenceStore) Record(ctx context.Context, task airuntime.TaskExecution, nodeID int64, worker string, in artexdb.IntelligenceInput, fingerprint string, sealed []byte) (int64, error) {
	var id int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT set_config('tianqiong.task_id',?,TRUE),set_config('tianqiong.owner_user_id',?,TRUE),set_config('tianqiong.execution_id',?,TRUE),set_config('tianqiong.execution_mode',tianqiong_ai.task_run_mode(?::bigint,?::uuid),TRUE)`, strconv.FormatInt(task.ID, 10), strconv.Itoa(task.UserID), task.RunID, task.ID, task.RunID).Error; err != nil {
			return err
		}
		var checked int64
		if err := tx.Raw(`SELECT id FROM tianqiong_ai.assert_task_write(?,NULL)`, task.ExplorationID).Row().Scan(&checked); err != nil || checked != task.ID {
			return aiapp.ErrRunLeaseLost
		}
		var enabled bool
		err := tx.Raw(`SELECT COALESCE(options->>'task_intelligence_enabled','false')='true' FROM ai_runtime_setting WHERE tenant_id=? FOR SHARE`, task.TenantID).Row().Scan(&enabled)
		if errors.Is(err, sql.ErrNoRows) || err == nil && !enabled {
			return aiapp.ErrTaskControlConflict
		}
		if err != nil {
			return err
		}
		var node any
		if nodeID > 0 {
			var found bool
			if err = tx.Raw(`SELECT EXISTS(SELECT 1 FROM tianqiong_ai.exploration_nodes WHERE exploration_id=? AND id=? AND kind IN ('intent','fact') AND state<>'deleted')`, task.ExplorationID, nodeID).Scan(&found).Error; err != nil {
				return err
			}
			if !found {
				return aiapp.ErrInvalidInput
			}
			node = nodeID
		}
		for _, assetID := range in.AssetIDs {
			if err = tx.Raw(`SELECT id FROM tianqiong_ai.assert_task_write(?,?)`, task.ExplorationID, assetID).Row().Scan(&checked); err != nil || checked != task.ID {
				return aiapp.ErrRunLeaseLost
			}
		}
		ids, err := json.Marshal(in.AssetIDs)
		if err != nil {
			return err
		}
		return tx.Raw(`INSERT INTO tianqiong_ai.task_intelligence(task_id,node_id,kind,title,content,evidence,confidence,status,asset_ids,worker,fingerprint,sealed_payload)
		 VALUES(?,?,?,?,'','',?,?,?::jsonb,?,?,?) ON CONFLICT(task_id,kind,fingerprint) DO UPDATE SET updated_at=clock_timestamp(),
		 sealed_payload=CASE WHEN task_intelligence.confidence='inferred' AND EXCLUDED.confidence='observed' OR task_intelligence.status IN ('unverified','suspected','pending') AND EXCLUDED.status NOT IN ('unverified','suspected','pending') THEN EXCLUDED.sealed_payload ELSE task_intelligence.sealed_payload END,
		 confidence=CASE WHEN task_intelligence.confidence='observed' THEN 'observed' ELSE EXCLUDED.confidence END,
		 status=CASE WHEN task_intelligence.status IN ('unverified','suspected','pending') THEN EXCLUDED.status ELSE task_intelligence.status END RETURNING id`, task.ID, node, in.Kind, in.Title, in.Confidence, in.Status, string(ids), worker, fingerprint, sealed).Row().Scan(&id)
	})
	return id, err
}

func (s *TaskIntelligenceStore) Stream(ctx context.Context, user int, taskID, id int64, visit func(aiapp.TaskIntelligence) error) error {
	task, err := NewTaskStore(s.db).GetTask(ctx, user, taskID)
	if err != nil {
		return err
	}
	rows, err := s.db.WithContext(ctx).Raw(`WITH current_task AS (SELECT id,tenant_id,owner_user_id,target_id,exploration_id FROM ai_task WHERE id=? AND tenant_id=? AND owner_user_id=? AND deleted_at IS NULL AND (original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=ai_task.target_id AND deleted_at IS NULL))), context_tasks AS (
	 SELECT id,tenant_id,owner_user_id,target_id,exploration_id FROM current_task
	 UNION ALL SELECT source.id,source.tenant_id,source.owner_user_id,source.target_id,source.exploration_id FROM current_task c JOIN ai_task_relation r ON r.task_id=c.id AND r.tenant_id=c.tenant_id JOIN ai_task source ON source.id=r.source_task_id AND source.tenant_id=c.tenant_id AND source.owner_user_id=c.owner_user_id AND source.deleted_at IS NULL AND (source.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=source.target_id AND deleted_at IS NULL))
	) SELECT i.id,i.task_id,i.node_id,i.kind,i.title,i.content,i.evidence,i.confidence,i.status,i.asset_ids,i.worker,i.created_at,i.updated_at,i.fingerprint,i.sealed_payload,(i.task_id<>c.id) AS inherited,job.sealed_analysis,COALESCE(job.status,''),COALESCE(job.metadata,'{}'::jsonb),i.credential_analysis,
	 COALESCE((SELECT 'intent:'||n.id::text FROM tianqiong_ai.exploration_nodes n WHERE n.id=i.node_id AND n.exploration_id=source.exploration_id AND n.kind='intent'),(SELECT 'intent:'||min(n.id)::text FROM tianqiong_ai.exploration_edges e JOIN tianqiong_ai.exploration_nodes n ON n.id=e.src_id AND n.kind='intent' WHERE e.dst_id=i.node_id AND e.rel='yields' AND e.exploration_id=source.exploration_id AND n.exploration_id=source.exploration_id HAVING count(*)=1),CASE WHEN i.worker='planner' THEN 'plan' ELSE '' END) AS source_session
	 FROM current_task c JOIN context_tasks source ON TRUE JOIN tianqiong_ai.task_intelligence i ON i.task_id=source.id LEFT JOIN ai_credential_job job ON job.intelligence_id=i.id
	 WHERE (c.target_id IS NULL OR source.id=c.id OR source.target_id=c.target_id OR (jsonb_array_length(CASE WHEN jsonb_typeof(i.asset_ids)='array' THEN i.asset_ids ELSE '[]'::jsonb END)>0 AND NOT EXISTS(
	  SELECT 1 FROM jsonb_array_elements_text(CASE WHEN jsonb_typeof(i.asset_ids)='array' THEN i.asset_ids ELSE '[]'::jsonb END) ref(value)
	  WHERE NOT EXISTS(SELECT 1 FROM asset_entity a WHERE a.id::text=ref.value AND a.tenant_id=c.tenant_id AND (
	   EXISTS(SELECT 1 FROM asset_website_binding b JOIN website x ON x.id=b.website_id WHERE b.asset_id=a.id AND x.target_id=c.target_id) OR
	   EXISTS(SELECT 1 FROM asset_endpoint_binding b JOIN endpoint x ON x.id=b.endpoint_id WHERE b.asset_id=a.id AND x.target_id=c.target_id) OR
	   EXISTS(SELECT 1 FROM asset_subdomain_binding b JOIN subdomain x ON x.id=b.subdomain_id WHERE b.asset_id=a.id AND x.target_id=c.target_id) OR
	   EXISTS(SELECT 1 FROM asset_host_port_binding b JOIN host_port_mapping x ON x.id=b.host_port_id WHERE b.asset_id=a.id AND x.target_id=c.target_id) OR
	   EXISTS(SELECT 1 FROM asset_observation o WHERE o.asset_id=a.id AND o.tenant_id=c.tenant_id AND o.target_id=c.target_id) OR
	   EXISTS(SELECT 1 FROM asset_application_visible_website w JOIN website x ON x.id=w.website_id WHERE w.tenant_id=c.tenant_id AND w.application_id=a.id AND x.target_id=c.target_id) OR
	   EXISTS(SELECT 1 FROM asset_root_domain_target r JOIN target t ON t.id=r.target_id AND t.deleted_at IS NULL WHERE r.tenant_id=c.tenant_id AND r.root_asset_id=a.id AND r.target_id=c.target_id AND t.type='domain' AND t.name=a.label AND a.asset_type='root_domain')
	  ))))) AND (?=0 OR i.id=?) ORDER BY i.id DESC`, task.ID, task.TenantID, user, id, id).Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item aiapp.TaskIntelligence
		var assetJSON []byte
		if err = rows.Scan(&item.ID, &item.TaskID, &item.NodeID, &item.Kind, &item.Title, &item.Content, &item.Evidence, &item.Confidence, &item.Status, &assetJSON, &item.Worker, &item.CreatedAt, &item.UpdatedAt, &item.Fingerprint, &item.Sealed, &item.Inherited, &item.AnalysisSealed, &item.CredentialStatus, &item.CredentialMetadata, &item.LegacyAnalysis, &item.SourceSession); err != nil {
			return err
		}
		if err = json.Unmarshal(assetJSON, &item.AssetIDs); err != nil {
			return err
		}
		if err = visit(item); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *TaskIntelligenceStore) UpdateStatus(ctx context.Context, user int, taskID, id int64, status string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockHumanGoalScope(tx, user, taskID); err != nil {
			return err
		}
		var kind string
		err := tx.Raw(`SELECT kind FROM tianqiong_ai.task_intelligence WHERE task_id=? AND id=? FOR UPDATE`, taskID, id).Row().Scan(&kind)
		if errors.Is(err, sql.ErrNoRows) {
			return aiapp.ErrTaskNotFound
		}
		if err != nil {
			return err
		}
		if !artexdb.ValidIntelligenceStatus(kind, status) {
			return aiapp.ErrInvalidInput
		}
		if err = tx.Exec(`SELECT set_config('tianqiong.execution_mode','human_intelligence',TRUE),set_config('tianqiong.intelligence_id',?,TRUE)`, strconv.FormatInt(id, 10)).Error; err != nil {
			return err
		}
		return tx.Exec(`UPDATE tianqiong_ai.task_intelligence SET status=?,updated_at=clock_timestamp() WHERE task_id=? AND id=?`, status, taskID, id).Error
	})
}

var _ aiapp.IntelligenceStore = (*TaskIntelligenceStore)(nil)

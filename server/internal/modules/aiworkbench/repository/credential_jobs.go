package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/google/uuid"
	"github.com/lib/pq"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

type CredentialJobStore struct{ db *gorm.DB }

func NewCredentialJobStore(db *gorm.DB) *CredentialJobStore { return &CredentialJobStore{db: db} }

func (s *CredentialJobStore) Claim(ctx context.Context) (*aiapp.CredentialJob, error) {
	// A disabled processor has nothing to claim or recover. This preliminary
	// check avoids scanning the task graph on every idle poll; the transaction
	// below still rechecks the settings when claiming a job. If a setting is
	// enabled concurrently, the next poll will recover any waiting jobs.
	var enabled bool
	if err := s.db.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM ai_runtime_setting cfg JOIN ai_custom_agent agent ON agent.tenant_id=cfg.tenant_id AND agent.agent_key='credential_processor' AND agent.enabled WHERE COALESCE(cfg.options->>'task_intelligence_enabled','false')='true' AND COALESCE(cfg.options->>'credential_agent_enabled','false')='true')`).Scan(&enabled).Error; err != nil {
		return nil, err
	}
	if !enabled {
		return nil, nil
	}
	var job aiapp.CredentialJob
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT set_config('tianqiong.execution_mode','credential_queue',TRUE)`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`WITH candidates AS (
		 SELECT j.intelligence_id FROM ai_credential_job j JOIN ai_task t ON t.id=j.task_id JOIN tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id JOIN tenant tenant ON tenant.id=t.tenant_id JOIN auth_user u ON u.id=t.owner_user_id JOIN ai_runtime_setting cfg ON cfg.tenant_id=j.tenant_id JOIN ai_custom_agent a ON a.tenant_id=j.tenant_id AND a.agent_key='credential_processor' AND a.enabled
		 WHERE j.status='ready' AND COALESCE(j.metadata->>'traffic_state','off')='off' AND t.deleted_at IS NULL AND u.is_active AND tenant.status='active' AND m.role IN ('admin','analyst') AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target x WHERE x.id=t.target_id AND x.deleted_at IS NULL)) AND NOT EXISTS(SELECT 1 FROM ai_task_archive x WHERE x.task_id=t.id AND x.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting')) AND COALESCE(cfg.options->>'task_intelligence_enabled','false')='true' AND COALESCE(cfg.options->>'credential_agent_enabled','false')='true' AND COALESCE(cfg.options->>'credential_traffic_binding','false')='true' ORDER BY j.intelligence_id FOR UPDATE OF j SKIP LOCKED LIMIT 100)
		 UPDATE ai_credential_job j SET status='queued',updated_at=clock_timestamp() FROM candidates c WHERE j.intelligence_id=c.intelligence_id`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO ai_credential_job(intelligence_id,tenant_id,owner_user_id,task_id,fingerprint)
		 SELECT i.id,t.tenant_id,t.owner_user_id,t.id,i.fingerprint FROM tianqiong_ai.task_intelligence i JOIN ai_task t ON t.id=i.task_id
		 JOIN tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id JOIN tenant tenant ON tenant.id=t.tenant_id JOIN auth_user u ON u.id=t.owner_user_id
		 JOIN ai_runtime_setting cfg ON cfg.tenant_id=t.tenant_id JOIN ai_custom_agent agent ON agent.tenant_id=t.tenant_id AND agent.agent_key='credential_processor' AND agent.enabled
		 WHERE i.kind='credential' AND (i.credential_analysis IS NULL OR i.credential_analysis='null'::jsonb OR i.credential_analysis->>'state' IN ('queued','processing')) AND t.deleted_at IS NULL AND tenant.status='active' AND u.is_active AND m.role IN ('admin','analyst') AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=t.target_id AND deleted_at IS NULL)) AND NOT EXISTS(SELECT 1 FROM ai_task_archive a WHERE a.task_id=t.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting'))
		 AND COALESCE(cfg.options->>'task_intelligence_enabled','false')='true' AND COALESCE(cfg.options->>'credential_agent_enabled','false')='true'
		 AND NOT EXISTS(SELECT 1 FROM ai_credential_job j WHERE j.intelligence_id=i.id) ORDER BY i.id LIMIT 100 ON CONFLICT DO NOTHING`).Error; err != nil {
			return err
		}
		var assetJSON []byte
		err := tx.Raw(`SELECT j.intelligence_id,j.task_id,j.owner_user_id AS user_id,j.tenant_id::text,j.fingerprint,j.force_extract AS force,i.content,i.evidence,i.sealed_payload AS sealed,j.sealed_analysis,COALESCE(i.credential_analysis,'null'::jsonb),i.asset_ids
		 FROM ai_credential_job j JOIN tianqiong_ai.task_intelligence i ON i.id=j.intelligence_id JOIN ai_task t ON t.id=j.task_id
		 JOIN tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id JOIN tenant tenant ON tenant.id=t.tenant_id JOIN auth_user u ON u.id=t.owner_user_id
		 JOIN ai_runtime_setting cfg ON cfg.tenant_id=j.tenant_id JOIN ai_custom_agent agent ON agent.tenant_id=j.tenant_id AND agent.agent_key='credential_processor' AND agent.enabled
		 WHERE (j.status='queued' OR (j.status='processing' AND j.lease_until<=clock_timestamp())) AND t.deleted_at IS NULL AND tenant.status='active' AND u.is_active AND m.role IN ('admin','analyst') AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=t.target_id AND deleted_at IS NULL)) AND NOT EXISTS(SELECT 1 FROM ai_task_archive a WHERE a.task_id=t.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting'))
		 AND COALESCE(cfg.options->>'task_intelligence_enabled','false')='true' AND COALESCE(cfg.options->>'credential_agent_enabled','false')='true' ORDER BY j.intelligence_id FOR UPDATE OF j SKIP LOCKED LIMIT 1`).Row().Scan(&job.IntelligenceID, &job.TaskID, &job.UserID, &job.TenantID, &job.Fingerprint, &job.Force, &job.Content, &job.Evidence, &job.Sealed, &job.PreviousSealed, &job.LegacyAnalysis, &assetJSON)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = json.Unmarshal(assetJSON, &job.AssetIDs); err != nil {
			return err
		}
		job.Lease = uuid.NewString()
		return tx.Exec(`UPDATE ai_credential_job SET status='processing',lease_id=?,lease_until=clock_timestamp()+interval '3 minutes',updated_at=clock_timestamp() WHERE intelligence_id=?`, job.Lease, job.IntelligenceID).Error
	})
	if err != nil {
		return nil, err
	}
	if job.IntelligenceID == 0 {
		return nil, nil
	}
	return &job, nil
}

func (s *CredentialJobStore) Enabled(ctx context.Context, job *aiapp.CredentialJob) (bool, error) {
	if job == nil {
		return false, aiapp.ErrInvalidInput
	}
	var enabled bool
	err := s.db.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM ai_credential_job j JOIN ai_task t ON t.id=j.task_id JOIN tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id JOIN tenant tenant ON tenant.id=t.tenant_id JOIN auth_user u ON u.id=t.owner_user_id JOIN ai_runtime_setting cfg ON cfg.tenant_id=j.tenant_id JOIN ai_custom_agent agent ON agent.tenant_id=j.tenant_id AND agent.agent_key='credential_processor' AND agent.enabled WHERE j.intelligence_id=? AND j.task_id=? AND j.owner_user_id=? AND j.status='processing' AND j.lease_id=? AND j.lease_until>clock_timestamp() AND t.deleted_at IS NULL AND tenant.status='active' AND u.is_active AND m.role IN ('admin','analyst') AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=t.target_id AND deleted_at IS NULL)) AND NOT EXISTS(SELECT 1 FROM ai_task_archive a WHERE a.task_id=t.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting')) AND COALESCE(cfg.options->>'task_intelligence_enabled','false')='true' AND COALESCE(cfg.options->>'credential_agent_enabled','false')='true')`, job.IntelligenceID, job.TaskID, job.UserID, job.Lease).Scan(&enabled).Error
	return enabled, err
}

func (s *CredentialJobStore) Finish(ctx context.Context, job *aiapp.CredentialJob, status string, sealed []byte, metadata json.RawMessage) error {
	if job == nil || (status != "queued" && status != "ready" && status != "error") {
		return aiapp.ErrInvalidInput
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current struct {
			TenantID string
			Status   string
			Lease    string
		}
		result := tx.Raw(`SELECT tenant_id::text,status,COALESCE(lease_id::text,'') AS lease FROM ai_credential_job WHERE intelligence_id=? AND task_id=? AND owner_user_id=? FOR UPDATE`, job.IntelligenceID, job.TaskID, job.UserID).Scan(&current)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 || current.Status != "processing" || current.Lease != job.Lease {
			return aiapp.ErrRunLeaseLost
		}
		var enabled, binding bool
		var epoch int64
		err := tx.Raw(`SELECT COALESCE(options->>'task_intelligence_enabled','false')='true' AND COALESCE(options->>'credential_agent_enabled','false')='true',COALESCE(options->>'credential_traffic_binding','false')='true',credential_binding_epoch FROM ai_runtime_setting WHERE tenant_id=? FOR SHARE`, current.TenantID).Row().Scan(&enabled, &binding, &epoch)
		if errors.Is(err, sql.ErrNoRows) {
			enabled = false
		} else if err != nil {
			return err
		}
		var agentEnabled bool
		err = tx.Raw(`SELECT enabled FROM ai_custom_agent WHERE tenant_id=? AND agent_key='credential_processor' FOR SHARE`, current.TenantID).Row().Scan(&agentEnabled)
		if errors.Is(err, sql.ErrNoRows) {
			agentEnabled = false
		} else if err != nil {
			return err
		}
		if !enabled || !agentEnabled {
			status = "queued"
		}
		if status == "ready" && len(metadata) > 0 {
			var info struct {
				TrafficEpoch *int64 `json:"traffic_epoch"`
			}
			if json.Unmarshal(metadata, &info) != nil {
				return aiapp.ErrInvalidInput
			}
			if info.TrafficEpoch != nil && (!binding || *info.TrafficEpoch != epoch) {
				return aiapp.ErrCredentialBindingStateChanged
			}
		}
		if err = tx.Exec(`SELECT set_config('tianqiong.execution_mode','credential_worker',TRUE),set_config('tianqiong.credential_lease',?,TRUE)`, job.Lease).Error; err != nil {
			return err
		}
		var update *gorm.DB
		if status == "queued" {
			update = tx.Exec(`UPDATE ai_credential_job SET status='queued',lease_id=NULL,lease_until=NULL,updated_at=clock_timestamp() WHERE intelligence_id=? AND lease_id=? AND lease_until>clock_timestamp()`, job.IntelligenceID, job.Lease)
		} else {
			if len(metadata) == 0 || !json.Valid(metadata) {
				return aiapp.ErrInvalidInput
			}
			update = tx.Exec(`UPDATE ai_credential_job SET status=?,lease_id=NULL,lease_until=NULL,force_extract=FALSE,sealed_analysis=?,metadata=?::jsonb,updated_at=clock_timestamp() WHERE intelligence_id=? AND lease_id=? AND lease_until>clock_timestamp()`, status, sealed, string(metadata), job.IntelligenceID, job.Lease)
		}
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return aiapp.ErrRunLeaseLost
		}
		return nil
	})
}

func (s *CredentialJobStore) Retry(ctx context.Context, user int, taskID, id int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockHumanGoalScope(tx, user, taskID); err != nil {
			return err
		}
		var enabled bool
		err := tx.Raw(`SELECT COALESCE(options->>'task_intelligence_enabled','false')='true' AND COALESCE(options->>'credential_agent_enabled','false')='true' FROM ai_runtime_setting WHERE tenant_id=? FOR SHARE`, assets.DefaultTenantID).Row().Scan(&enabled)
		if errors.Is(err, sql.ErrNoRows) || err == nil && !enabled {
			return aiapp.ErrTaskControlConflict
		}
		if err != nil {
			return err
		}
		if err = tx.Exec(`SELECT set_config('tianqiong.execution_mode','credential_queue',TRUE)`).Error; err != nil {
			return err
		}
		if err = tx.Exec(`INSERT INTO ai_credential_job(intelligence_id,tenant_id,owner_user_id,task_id,fingerprint) SELECT i.id,t.tenant_id,t.owner_user_id,t.id,i.fingerprint FROM tianqiong_ai.task_intelligence i JOIN ai_task t ON t.id=i.task_id WHERE i.id=? AND t.id=? AND t.owner_user_id=? AND i.kind='credential' ON CONFLICT DO NOTHING`, id, taskID, user).Error; err != nil {
			return err
		}
		var state string
		var live bool
		err = tx.Raw(`SELECT status,COALESCE(lease_until>clock_timestamp(),FALSE) FROM ai_credential_job WHERE intelligence_id=? AND task_id=? AND owner_user_id=? FOR UPDATE`, id, taskID, user).Row().Scan(&state, &live)
		if errors.Is(err, sql.ErrNoRows) {
			return aiapp.ErrTaskNotFound
		}
		if err != nil {
			return err
		}
		if state == "processing" && live {
			return aiapp.ErrTaskControlConflict
		}
		if err = tx.Exec(`SELECT set_config('tianqiong.execution_mode','human_credential',TRUE),set_config('tianqiong.intelligence_id',?,TRUE)`, strconv.FormatInt(id, 10)).Error; err != nil {
			return err
		}
		return tx.Exec(`UPDATE ai_credential_job SET status='queued',lease_id=NULL,lease_until=NULL,force_extract=TRUE,metadata=(metadata-'error'-'state')||'{"state":"queued"}'::jsonb,updated_at=clock_timestamp() WHERE intelligence_id=? AND task_id=?`, id, taskID).Error
	})
}

var _ aiapp.CredentialJobStore = (*CredentialJobStore)(nil)

func (s *CredentialJobStore) TrafficState(ctx context.Context, job *aiapp.CredentialJob) (bool, int64, error) {
	if job == nil {
		return false, 0, aiapp.ErrInvalidInput
	}
	var binding bool
	var epoch int64
	err := s.db.WithContext(ctx).Raw(`SELECT COALESCE(cfg.options->>'credential_traffic_binding','false')='true',cfg.credential_binding_epoch FROM ai_credential_job j JOIN ai_task t ON t.id=j.task_id JOIN tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id JOIN tenant tenant ON tenant.id=m.tenant_id JOIN auth_user u ON u.id=t.owner_user_id JOIN ai_runtime_setting cfg ON cfg.tenant_id=j.tenant_id JOIN ai_custom_agent agent ON agent.tenant_id=j.tenant_id AND agent.agent_key='credential_processor' AND agent.enabled WHERE j.intelligence_id=? AND j.task_id=? AND j.owner_user_id=? AND j.status='processing' AND j.lease_id=? AND j.lease_until>clock_timestamp() AND t.deleted_at IS NULL AND tenant.status='active' AND u.is_active AND m.role IN ('admin','analyst') AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target x WHERE x.id=t.target_id AND x.deleted_at IS NULL)) AND NOT EXISTS(SELECT 1 FROM ai_task_archive a WHERE a.task_id=t.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting')) AND COALESCE(cfg.options->>'task_intelligence_enabled','false')='true' AND COALESCE(cfg.options->>'credential_agent_enabled','false')='true'`, job.IntelligenceID, job.TaskID, job.UserID, job.Lease).Row().Scan(&binding, &epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return false, 0, aiapp.ErrRunLeaseLost
	}
	return binding, epoch, err
}
func (s *CredentialJobStore) TrafficAssets(ctx context.Context, job *aiapp.CredentialJob) ([]aiapp.CredentialTrafficAsset, error) {
	if job == nil {
		return nil, aiapp.ErrInvalidInput
	}
	if _, _, err := s.TrafficState(ctx, job); err != nil {
		return nil, err
	}
	items := []aiapp.CredentialTrafficAsset{}
	if len(job.AssetIDs) == 0 {
		return items, nil
	}
	err := s.db.WithContext(ctx).Raw(`SELECT e.asset_type AS type,e.label FROM asset_entity e JOIN tianqiong_ai.task_scope_assets(?,?) scope ON scope.asset_id=e.id WHERE e.tenant_id=?::uuid AND e.id=ANY(?::bigint[]) ORDER BY e.id`, job.TaskID, job.UserID, job.TenantID, pq.Int64Array(job.AssetIDs)).Scan(&items).Error
	return items, err
}

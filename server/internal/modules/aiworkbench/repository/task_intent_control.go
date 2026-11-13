package repository

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

func (s *TaskStore) QueueIntentPause(ctx context.Context, user int, taskID, intentID int64) (string, error) {
	id := uuid.NewString()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		scope, err := lockHumanGoalScope(tx, user, taskID)
		if err != nil {
			return err
		}
		if scope.Status != "running" || scope.LeaseUntil == nil || !scope.LeaseUntil.After(time.Now()) {
			return aiapp.ErrTaskControlConflict
		}
		var run, state string
		if err = tx.Raw(`SELECT active_run::text FROM ai_task WHERE id=?`, taskID).Row().Scan(&run); err != nil {
			return err
		}
		err = tx.Raw(`SELECT state FROM tianqiong_ai.exploration_nodes WHERE exploration_id=? AND id=? AND kind='intent'`, scope.ExplorationID, intentID).Row().Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			return aiapp.ErrTaskNotFound
		}
		if err != nil {
			return err
		}
		if state != "running" {
			return aiapp.ErrTaskControlConflict
		}
		if err = tx.Exec(`SELECT set_config('tianqiong.execution_mode','human_intent',TRUE)`).Error; err != nil {
			return err
		}
		result := tx.Exec(`INSERT INTO ai_task_intent_control(id,tenant_id,owner_user_id,task_id,run_id,intent_id) VALUES(?,?,?,?,?,?) ON CONFLICT (task_id,run_id,intent_id) WHERE status='pending' DO NOTHING`, id, assets.DefaultTenantID, user, taskID, run, intentID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return aiapp.ErrTaskControlConflict
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *TaskStore) IntentPauseStatus(ctx context.Context, user int, taskID int64, id string) (string, error) {
	var status string
	err := s.db.WithContext(ctx).Raw(`SELECT CASE WHEN c.action='pause' AND c.status='pending' AND (t.status<>'running' OR t.active_run IS DISTINCT FROM c.run_id OR t.lease_until IS NULL OR t.lease_until<=clock_timestamp()) THEN 'conflict' ELSE c.status END
		FROM ai_task_intent_control c JOIN ai_task t ON t.id=c.task_id JOIN tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id JOIN tenant tenant ON tenant.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
		WHERE c.id=? AND c.task_id=? AND c.tenant_id=? AND c.owner_user_id=? AND t.deleted_at IS NULL AND tenant.status='active' AND u.is_active=TRUE`, id, taskID, assets.DefaultTenantID, user).Row().Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", aiapp.ErrTaskNotFound
	}
	return status, err
}

func (s *TaskStore) ResumeTaskIntent(ctx context.Context, user int, taskID, intentID int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		scope, err := lockHumanGoalScope(tx, user, taskID)
		if err != nil {
			return err
		}
		var deleting bool
		if err = tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_task_intent_control WHERE task_id=? AND intent_id=? AND action='cancel' AND status IN ('pending','paused'))`, taskID, intentID).Scan(&deleting).Error; err != nil {
			return err
		}
		if deleting {
			return aiapp.ErrTaskControlConflict
		}
		if err = tx.Exec(`SELECT set_config('tianqiong.execution_mode','human_intent',TRUE),set_config('tianqiong.intent_id',?,TRUE)`, strconv.FormatInt(intentID, 10)).Error; err != nil {
			return err
		}
		result := tx.Exec(`UPDATE tianqiong_ai.exploration_nodes SET state='open',owner=NULL,blocked_reason=NULL,completed_at=NULL,content_version=content_version+1 WHERE exploration_id=? AND id=? AND kind='intent' AND state='paused'`, scope.ExplorationID, intentID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return aiapp.ErrTaskControlConflict
		}
		// A paused task remains paused. Resuming the task later starts its open
		// intent; a running or terminal task needs a durable wake immediately.
		if scope.Status == "paused" || scope.Status == "created" && !scope.Queued {
			return nil
		}
		if err = insertHumanGoalEvent(tx, user, taskID, "intent_reopened", map[string]any{"intent_id": intentID}); err != nil {
			return err
		}
		return reviveForHumanGoal(tx, user, taskID, scope)
	})
}

var _ aiapp.TaskIntentControlStore = (*TaskStore)(nil)

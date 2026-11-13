package repository

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/Autumn-27/artex/sidequestion"
	"github.com/google/uuid"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

var errIntentDeleteBusy = errors.New("intent deletion is waiting for a graph lock")

// CancelTaskIntent soft-deletes idle work immediately. Running work first gets
// a durable pause request tied to its current lease; a recovery pass completes
// deletion after the Worker has settled or that lease has expired.
func (s *TaskStore) CancelTaskIntent(ctx context.Context, user int, taskID, intentID int64, reason, mode string) (string, string, error) {
	var request, state string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		scope, err := lockHumanGoalScope(tx, user, taskID)
		if err != nil {
			return err
		}
		var current string
		err = tx.Raw(`SELECT state FROM tianqiong_ai.exploration_nodes WHERE exploration_id=? AND id=? AND kind='intent' FOR UPDATE SKIP LOCKED`, scope.ExplorationID, intentID).Row().Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			var exists bool
			if err = tx.Raw(`SELECT EXISTS(SELECT 1 FROM tianqiong_ai.exploration_nodes WHERE exploration_id=? AND id=? AND kind='intent')`, scope.ExplorationID, intentID).Scan(&exists).Error; err != nil {
				return err
			}
			if exists {
				return aiapp.ErrTaskControlConflict
			}
			return aiapp.ErrTaskNotFound
		}
		if err != nil {
			return err
		}
		if current != "open" && current != "paused" && current != "running" {
			return aiapp.ErrTaskControlConflict
		}
		var existing bool
		if err = tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_task_intent_control WHERE task_id=? AND intent_id=? AND action='cancel' AND status IN ('pending','paused'))`, taskID, intentID).Scan(&existing).Error; err != nil {
			return err
		}
		if existing {
			return aiapp.ErrTaskControlConflict
		}
		if err = tx.Exec(`SELECT set_config('tianqiong.execution_mode','human_intent',TRUE),set_config('tianqiong.intent_id',?,TRUE)`, strconv.FormatInt(intentID, 10)).Error; err != nil {
			return err
		}
		if current == "running" && scope.Status == "running" && scope.LeaseUntil != nil && scope.LeaseUntil.After(time.Now()) {
			var run string
			if err = tx.Raw(`SELECT active_run::text FROM ai_task WHERE id=?`, taskID).Row().Scan(&run); err != nil {
				return err
			}
			request = uuid.NewString()
			result := tx.Exec(`INSERT INTO ai_task_intent_control(id,tenant_id,owner_user_id,task_id,run_id,intent_id,action,reason,mode) VALUES(?,?,?,?,?,?,'cancel',?,?) ON CONFLICT (task_id,run_id,intent_id) WHERE status='pending' DO NOTHING`, request, assets.DefaultTenantID, user, taskID, run, intentID, reason, mode)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return aiapp.ErrTaskControlConflict
			}
			state = "pending"
			return nil
		}
		if err = deleteTaskIntent(tx, user, taskID, intentID, scope, reason, mode); err != nil {
			return err
		}
		state = "deleted"
		return nil
	})
	if errors.Is(err, errIntentDeleteBusy) {
		return "", "", aiapp.ErrTaskControlConflict
	}
	return request, state, err
}

func deleteTaskIntent(tx *gorm.DB, user int, taskID, intentID int64, scope humanGoalScope, reason, mode string) error {
	if mode == "hard" {
		return hardDeleteTaskIntent(tx, user, taskID, intentID, scope, reason)
	}
	return softDeleteTaskIntent(tx, user, taskID, intentID, scope, reason)
}

func softDeleteTaskIntent(tx *gorm.DB, user int, taskID, intentID int64, scope humanGoalScope, reason string) error {
	var summary string
	if err := tx.Raw(`SELECT COALESCE(payload->>'summary','') FROM tianqiong_ai.exploration_nodes WHERE exploration_id=? AND id=? AND kind='intent' FOR UPDATE SKIP LOCKED`, scope.ExplorationID, intentID).Row().Scan(&summary); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			var exists bool
			if queryErr := tx.Raw(`SELECT EXISTS(SELECT 1 FROM tianqiong_ai.exploration_nodes WHERE exploration_id=? AND id=? AND kind='intent')`, scope.ExplorationID, intentID).Scan(&exists).Error; queryErr != nil {
				return queryErr
			}
			if exists {
				return errIntentDeleteBusy
			}
			return aiapp.ErrTaskControlConflict
		}
		return err
	}
	result := tx.Exec(`UPDATE tianqiong_ai.exploration_nodes SET state='deleted',delete_reason=?,blocked_reason=NULL,completed_at=clock_timestamp(),content_version=content_version+1 WHERE exploration_id=? AND id=? AND kind='intent' AND state IN ('open','paused','running')`, reason, scope.ExplorationID, intentID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return aiapp.ErrTaskControlConflict
	}
	parent := sidequestion.Parent{TaskID: taskID, ExplorationID: scope.ExplorationID, IntentID: intentID}
	if err := tx.Exec(`DELETE FROM ai_side_session WHERE tenant_id=? AND owner_user_id=? AND task_id=? AND parent_key=?`, assets.DefaultTenantID, user, taskID, parent.Key()).Error; err != nil {
		return err
	}
	if err := tx.Exec(`DELETE FROM ai_task_intent_message WHERE task_id=? AND intent_id=? AND status='pending'`, taskID, intentID).Error; err != nil {
		return err
	}
	if err := insertHumanGoalEvent(tx, user, taskID, "cancelled", map[string]any{"intent_id": intentID, "summary": summary, "detail": reason}); err != nil {
		return err
	}
	if scope.Status != "paused" && !(scope.Status == "created" && !scope.Queued) {
		if err := reviveForHumanGoal(tx, user, taskID, scope); err != nil {
			return err
		}
	}
	return nil
}

// The controller retries pending deletions after process/lease interruption.
// It never touches a live Worker before its pause was acknowledged.
func (s *TaskStore) ProcessIntentDeletions(ctx context.Context) error {
	type entry struct {
		ID     string
		User   int
		TaskID int64
	}
	items := []entry{}
	if err := s.db.WithContext(ctx).Raw(`SELECT c.id::text,c.owner_user_id AS "user",c.task_id FROM ai_task_intent_control c JOIN ai_task t ON t.id=c.task_id JOIN tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id JOIN tenant tenant ON tenant.id=t.tenant_id JOIN auth_user u ON u.id=t.owner_user_id WHERE c.action='cancel' AND t.deleted_at IS NULL AND tenant.status='active' AND u.is_active AND m.role IN ('admin','analyst') AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=t.target_id AND deleted_at IS NULL)) AND (c.status='paused' OR (c.status='pending' AND (t.status<>'running' OR t.lease_until IS NULL OR t.lease_until<=clock_timestamp()))) ORDER BY c.created_at LIMIT 100`).Scan(&items).Error; err != nil {
		return err
	}
	for _, item := range items {
		err := s.processIntentDeletion(ctx, item.User, item.TaskID, item.ID)
		if errors.Is(err, aiapp.ErrTaskControlConflict) {
			if err = s.failIntentDeletion(ctx, item.User, item.TaskID, item.ID); err != nil && !errors.Is(err, aiapp.ErrTaskNotFound) {
				return err
			}
		} else if err != nil && !errors.Is(err, aiapp.ErrTaskNotFound) && !errors.Is(err, errIntentDeleteBusy) {
			return err
		}
	}
	return nil
}

func (s *TaskStore) failIntentDeletion(ctx context.Context, user int, taskID int64, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockHumanGoalScope(tx, user, taskID); err != nil {
			return err
		}
		if err := tx.Exec(`SELECT set_config('tianqiong.execution_mode','human_intent',TRUE)`).Error; err != nil {
			return err
		}
		return tx.Exec(`UPDATE ai_task_intent_control SET status='conflict',finished_at=clock_timestamp() WHERE id=? AND task_id=? AND action='cancel' AND status IN ('pending','paused')`, id, taskID).Error
	})
}

func (s *TaskStore) processIntentDeletion(ctx context.Context, user int, taskID int64, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		scope, err := lockHumanGoalScope(tx, user, taskID)
		if err != nil {
			return err
		}
		var row struct {
			IntentID             int64
			Status, Reason, Mode string
		}
		result := tx.Raw(`SELECT intent_id,status,reason,mode FROM ai_task_intent_control WHERE id=? AND task_id=? AND action='cancel' FOR UPDATE`, id, taskID).Scan(&row)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 || (row.Status != "pending" && row.Status != "paused") {
			return nil
		}
		if row.Status == "pending" && scope.Status == "running" && scope.LeaseUntil != nil && scope.LeaseUntil.After(time.Now()) {
			return nil
		}
		if err = tx.Exec(`SELECT set_config('tianqiong.execution_mode','human_intent',TRUE),set_config('tianqiong.intent_id',?,TRUE)`, strconv.FormatInt(row.IntentID, 10)).Error; err != nil {
			return err
		}
		if err = deleteTaskIntent(tx, user, taskID, row.IntentID, scope, row.Reason, row.Mode); err != nil {
			return err
		}
		return tx.Exec(`UPDATE ai_task_intent_control SET status='deleted',finished_at=clock_timestamp() WHERE id=? AND status IN ('pending','paused')`, id).Error
	})
}

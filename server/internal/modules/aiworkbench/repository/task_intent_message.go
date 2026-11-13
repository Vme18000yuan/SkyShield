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

func existingIntentMessage(tx *gorm.DB, taskID, intentID int64, requestID, message string) (bool, error) {
	var previous struct {
		IntentID    int64
		DisplayText string
	}
	result := tx.Raw(`SELECT intent_id,display_text FROM ai_task_intent_message WHERE task_id=? AND request_id=?`, taskID, requestID).Scan(&previous)
	if result.Error != nil || result.RowsAffected == 0 {
		return false, result.Error
	}
	if previous.IntentID != intentID || previous.DisplayText != message {
		return false, aiapp.ErrTaskControlConflict
	}
	return true, nil
}

func (s *TaskStore) ExistingIntentMessage(ctx context.Context, user int, taskID, intentID int64, requestID, message string) (bool, error) {
	var exists bool
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockHumanGoalScope(tx, user, taskID); err != nil {
			return err
		}
		var err error
		exists, err = existingIntentMessage(tx, taskID, intentID, requestID, message)
		return err
	})
	return exists, err
}

func (s *TaskStore) SendIntentMessage(ctx context.Context, user int, taskID, intentID int64, requestID, message, modelText string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		scope, err := lockHumanGoalScope(tx, user, taskID)
		if err != nil {
			return err
		}
		exists, err := existingIntentMessage(tx, taskID, intentID, requestID, message)
		if err != nil || exists {
			return err
		}
		if scope.Status != "running" || scope.Queued || scope.LeaseUntil == nil || !scope.LeaseUntil.After(time.Now()) || scope.DeadlineAt != nil && !scope.DeadlineAt.After(time.Now()) {
			return aiapp.ErrTaskControlConflict
		}
		var state, worker string
		err = tx.Raw(`SELECT state,COALESCE(owner,'') FROM tianqiong_ai.exploration_nodes WHERE exploration_id=? AND id=? AND kind='intent'`, scope.ExplorationID, intentID).Row().Scan(&state, &worker)
		if errors.Is(err, sql.ErrNoRows) {
			return aiapp.ErrTaskNotFound
		}
		if err != nil {
			return err
		}
		if state != "paused" {
			return aiapp.ErrTaskControlConflict
		}
		var deleting bool
		if err = tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_task_intent_control WHERE task_id=? AND intent_id=? AND action='cancel' AND status IN ('pending','paused'))`, taskID, intentID).Scan(&deleting).Error; err != nil {
			return err
		}
		if deleting {
			return aiapp.ErrTaskControlConflict
		}
		var pending bool
		if err = tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_task_intent_message WHERE task_id=? AND intent_id=? AND status='pending')`, taskID, intentID).Scan(&pending).Error; err != nil {
			return err
		}
		if pending {
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
		if err = tx.Exec(`INSERT INTO ai_task_intent_message(id,tenant_id,owner_user_id,task_id,intent_id,request_id,display_text,model_text,worker) VALUES(?,?,?,?,?,?,?,?,?)`, uuid.NewString(), assets.DefaultTenantID, user, taskID, intentID, requestID, message, modelText, worker).Error; err != nil {
			return err
		}
		return insertHumanGoalEvent(tx, user, taskID, "intent_reopened", map[string]any{"intent_id": intentID})
	})
}

var _ aiapp.TaskIntentMessageStore = (*TaskStore)(nil)

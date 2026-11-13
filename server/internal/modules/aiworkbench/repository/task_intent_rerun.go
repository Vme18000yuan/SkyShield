package repository

import (
	"context"
	"strconv"

	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"gorm.io/gorm"
)

func (s *TaskStore) RerunTaskIntent(ctx context.Context, user int, taskID, intentID int64) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		scope, err := lockHumanGoalScope(tx, user, taskID)
		if err != nil {
			return err
		}
		intent := ""
		if intentID > 0 {
			intent = strconv.FormatInt(intentID, 10)
		}
		if err = tx.Exec(`SELECT set_config('tianqiong.execution_mode','human_intent',TRUE),set_config('tianqiong.intent_id',?,TRUE)`, intent).Error; err != nil {
			return err
		}
		query := `UPDATE tianqiong_ai.exploration_nodes SET state='open',owner=NULL,blocked_reason=NULL,completed_at=NULL,content_version=content_version+1 WHERE exploration_id=? AND kind='intent' `
		var changed *gorm.DB
		if intentID > 0 {
			changed = tx.Exec(query+`AND id=? AND state IN ('blocked','exhausted','stopped')`, scope.ExplorationID, intentID)
		} else {
			changed = tx.Exec(query+`AND state='blocked'`, scope.ExplorationID)
		}
		if changed.Error != nil {
			return changed.Error
		}
		count = changed.RowsAffected
		if count == 0 {
			if intentID > 0 {
				return aiapp.ErrTaskControlConflict
			}
			return nil
		}
		if err = insertHumanGoalEvent(tx, user, taskID, "intent_reopened", map[string]any{"intent_id": intentID}); err != nil {
			return err
		}
		return reviveForHumanGoal(tx, user, taskID, scope)
	})
	return count, err
}

var _ aiapp.TaskIntentRerunStore = (*TaskStore)(nil)

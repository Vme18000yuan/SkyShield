package repository

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

func (s *TaskStore) ListTaskConstraints(ctx context.Context, user int, taskID int64) ([]aiapp.TaskConstraint, error) {
	if _, err := s.GetTask(ctx, user, taskID); err != nil {
		return nil, err
	}
	items := []aiapp.TaskConstraint{}
	err := s.db.WithContext(ctx).Raw(`SELECT c.id,c.kind,c.text,COALESCE(c.origin,'') AS origin,c.created_at FROM tianqiong_ai.task_constraints c JOIN ai_task t ON t.exploration_id=c.exploration_id JOIN tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id JOIN tenant tenant ON tenant.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE t.tenant_id=? AND t.owner_user_id=? AND t.id=? AND t.deleted_at IS NULL AND tenant.status='active' AND u.is_active=TRUE ORDER BY CASE c.kind WHEN 'allow' THEN 0 ELSE 1 END,c.id LIMIT 10000`, assets.DefaultTenantID, user, taskID).Scan(&items).Error
	return items, err
}
func humanConstraintScope(tx *gorm.DB, user int, taskID int64) (int64, error) {
	if err := scheduleWriteRole(tx, user); err != nil {
		return 0, err
	}
	var exploration int64
	err := tx.Raw(`SELECT exploration_id FROM ai_task WHERE tenant_id=? AND owner_user_id=? AND id=? AND deleted_at IS NULL AND (original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=ai_task.target_id AND deleted_at IS NULL)) FOR SHARE`, assets.DefaultTenantID, user, taskID).Row().Scan(&exploration)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, aiapp.ErrTaskNotFound
	}
	if err != nil {
		return 0, err
	}
	if err = tx.Exec(`SELECT set_config('tianqiong.task_id',?,TRUE),set_config('tianqiong.owner_user_id',?,TRUE),set_config('tianqiong.execution_mode','human_constraint',TRUE)`, strconv.FormatInt(taskID, 10), strconv.Itoa(user)).Error; err != nil {
		return 0, err
	}
	return exploration, nil
}
func (s *TaskStore) SaveTaskConstraint(ctx context.Context, user int, taskID, id int64, kind, text string) (*aiapp.TaskConstraint, error) {
	var item aiapp.TaskConstraint
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		exp, err := humanConstraintScope(tx, user, taskID)
		if err != nil {
			return err
		}
		var row *sql.Row
		if id == 0 {
			row = tx.Raw(`INSERT INTO tianqiong_ai.task_constraints(exploration_id,kind,text,origin) VALUES(?,?,?,'human') RETURNING id,kind,text,COALESCE(origin,''),created_at`, exp, kind, text).Row()
		} else {
			row = tx.Raw(`UPDATE tianqiong_ai.task_constraints SET kind=?,text=? WHERE id=? AND exploration_id=? RETURNING id,kind,text,COALESCE(origin,''),created_at`, kind, text, id, exp).Row()
		}
		err = row.Scan(&item.ID, &item.Kind, &item.Text, &item.Origin, &item.CreatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return aiapp.ErrTaskNotFound
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return &item, nil
}
func (s *TaskStore) DeleteTaskConstraint(ctx context.Context, user int, taskID, id int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		exp, err := humanConstraintScope(tx, user, taskID)
		if err != nil {
			return err
		}
		result := tx.Exec(`DELETE FROM tianqiong_ai.task_constraints WHERE id=? AND exploration_id=?`, id, exp)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return aiapp.ErrTaskNotFound
		}
		return nil
	})
}

var _ aiapp.TaskConstraintStore = (*TaskStore)(nil)

package repository

import (
	"context"
	"database/sql"
	"errors"

	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

// ClaimMainChat serializes MainAgent turns across server instances. It locks the
// task before the lease, the same order used by task control and graph writes.
func (s *TaskStore) ClaimMainChat(ctx context.Context, user int, taskID int64, run string) (*aiapp.Task, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := scheduleWriteRole(tx, user); err != nil {
			return err
		}
		var id int64
		err := tx.Raw(`SELECT t.id FROM ai_task t WHERE t.tenant_id=? AND t.owner_user_id=? AND t.id=? AND t.deleted_at IS NULL
          AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target x WHERE x.id=t.target_id AND x.deleted_at IS NULL))
          AND NOT EXISTS(SELECT 1 FROM ai_task_archive a WHERE a.task_id=t.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting')) FOR UPDATE`, assets.DefaultTenantID, user, taskID).Row().Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return aiapp.ErrTaskNotFound
		}
		if err != nil {
			return err
		}
		q := tx.Exec(`INSERT INTO ai_task_main_chat_run(task_id,tenant_id,owner_user_id,run_id,status,lease_until)
          VALUES(?,?,?,?::uuid,'running',clock_timestamp()+interval '30 seconds')
          ON CONFLICT(task_id) DO UPDATE SET run_id=EXCLUDED.run_id,status='running',lease_until=EXCLUDED.lease_until,created_at=clock_timestamp()
          WHERE ai_task_main_chat_run.lease_until<=clock_timestamp()`, taskID, assets.DefaultTenantID, user, run)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return aiapp.ErrTaskChatBusy
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	task, err := s.GetTask(ctx, user, taskID)
	if err != nil {
		_ = s.ReleaseMainChat(context.Background(), user, taskID, run)
	}
	return task, err
}

func (s *TaskStore) HeartbeatMainChat(ctx context.Context, user int, taskID int64, run string) (bool, error) {
	q := s.db.WithContext(ctx).Exec(`UPDATE ai_task_main_chat_run c SET lease_until=clock_timestamp()+interval '30 seconds'
      WHERE c.task_id=? AND c.owner_user_id=? AND c.run_id=?::uuid AND c.status='running' AND c.lease_until>clock_timestamp()
      AND tianqiong_ai.task_run_live(c.task_id,c.owner_user_id,c.run_id)`, taskID, user, run)
	return q.RowsAffected == 1, q.Error
}
func (s *TaskStore) ReleaseMainChat(ctx context.Context, user int, taskID int64, run string) error {
	return s.db.WithContext(ctx).Exec(`DELETE FROM ai_task_main_chat_run WHERE task_id=? AND owner_user_id=? AND run_id=?::uuid`, taskID, user, run).Error
}
func (s *TaskStore) StopMainChat(ctx context.Context, user int, taskID int64) (string, error) {
	var run string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockHumanGoalScope(tx, user, taskID); err != nil {
			return err
		}
		err := tx.Raw(`UPDATE ai_task_main_chat_run SET status='stopping' WHERE task_id=? AND owner_user_id=? AND status='running' AND lease_until>clock_timestamp() RETURNING run_id::text`, taskID, user).Row().Scan(&run)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	return run, err
}

func (s *TaskStore) MainChatRunning(ctx context.Context, user int, taskID int64) (bool, error) {
	if _, err := s.GetTask(ctx, user, taskID); err != nil {
		return false, err
	}
	var running bool
	err := s.db.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM ai_task_main_chat_run WHERE task_id=? AND owner_user_id=? AND lease_until>clock_timestamp())`, taskID, user).Scan(&running).Error
	return running, err
}

// A graph insert and its wake marker commit together. A separate pass takes
// calendar/task locks in normal order so a crash cannot lose a requested wake.
func (s *TaskStore) ProcessMainChatWakes(ctx context.Context) error {
	type candidate struct {
		TaskID      int64
		OwnerUserID int
	}
	var items []candidate
	if err := s.db.WithContext(ctx).Raw(`SELECT DISTINCT w.task_id,t.owner_user_id FROM ai_task_main_chat_wake w JOIN ai_task t ON t.id=w.task_id WHERE t.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM ai_task_main_chat_run c WHERE c.task_id=t.id AND c.lease_until>clock_timestamp()) ORDER BY w.task_id LIMIT 100`).Scan(&items).Error; err != nil {
		return err
	}
	for _, item := range items {
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			scope, err := lockHumanGoalScope(tx, item.OwnerUserID, item.TaskID)
			if err != nil {
				return err
			}
			q := tx.Exec(`DELETE FROM ai_task_main_chat_wake WHERE task_id=?`, item.TaskID)
			if q.Error != nil {
				return q.Error
			}
			if q.RowsAffected == 0 {
				return nil
			}
			return reviveForHumanGoal(tx, item.OwnerUserID, item.TaskID, scope)
		})
		if err != nil && !errors.Is(err, aiapp.ErrTaskNotFound) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}

package repository

import (
	"context"

	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"gorm.io/gorm"
)

func (s *TaskStore) ListMainSessions(ctx context.Context, user int, taskID int64) ([]aiapp.TaskMainSession, error) {
	task, err := s.GetTask(ctx, user, taskID)
	if err != nil {
		return nil, err
	}
	items := []aiapp.TaskMainSession{}
	if err = s.db.WithContext(ctx).Raw(`SELECT seq,created_at FROM tianqiong_ai.main_sessions WHERE exploration_id=? ORDER BY seq DESC`, task.ExplorationID).Scan(&items).Error; err != nil {
		return nil, err
	}
	return append(items, aiapp.TaskMainSession{Seq: 0}), nil
}

func (s *TaskStore) NewMainSession(ctx context.Context, user int, taskID int64) (aiapp.TaskMainSession, error) {
	var item aiapp.TaskMainSession
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		scope, err := lockHumanGoalScope(tx, user, taskID)
		if err != nil {
			return err
		}
		if err = tx.Exec(`SELECT set_config('tianqiong.execution_mode','human_main_session',TRUE)`).Error; err != nil {
			return err
		}
		return tx.Raw(`INSERT INTO tianqiong_ai.main_sessions(exploration_id,seq) VALUES(?,(SELECT COALESCE(MAX(seq),0)+1 FROM tianqiong_ai.main_sessions WHERE exploration_id=?)) RETURNING seq,created_at`, scope.ExplorationID, scope.ExplorationID).Row().Scan(&item.Seq, &item.CreatedAt)
	})
	return item, err
}

func (s *TaskStore) ListMainMessages(ctx context.Context, user int, taskID int64, segment int, before int64) ([]aiapp.TaskActivity, error) {
	task, err := s.GetTask(ctx, user, taskID)
	if err != nil {
		return nil, err
	}
	if segment > 0 {
		var exists bool
		if err = s.db.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM tianqiong_ai.main_sessions WHERE exploration_id=? AND seq=?)`, task.ExplorationID, segment).Scan(&exists).Error; err != nil {
			return nil, err
		}
		if !exists {
			return nil, aiapp.ErrTaskNotFound
		}
	}
	items := []aiapp.TaskActivity{}
	err = s.db.WithContext(ctx).Raw(`SELECT id,node_id,COALESCE(worker,'') AS worker,COALESCE(kind,'') AS kind,COALESCE(tool,'') AS tool,COALESCE(tool_use_id,'') AS tool_use_id,is_error,COALESCE(summary,'') AS summary,COALESCE(detail,'') AS detail,main_seg,created_at FROM tianqiong_ai.activity WHERE exploration_id=? AND worker='mainagent' AND kind IN ('human_message','assistant_message') AND COALESCE(main_seg,0)=? AND (?=0 OR id<?) ORDER BY id DESC LIMIT 101`, task.ExplorationID, segment, before, before).Scan(&items).Error
	return items, err
}

var _ aiapp.TaskMainSessionStore = (*TaskStore)(nil)

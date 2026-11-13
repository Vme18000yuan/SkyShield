package repository

import (
	"context"

	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
)

// ListMainTranscript keeps detailed tool output on the existing activity-detail
// endpoint so a large tool response does not inflate every poll.
func (s *TaskStore) ListMainTranscript(ctx context.Context, user int, taskID int64, segment int, before int64) ([]aiapp.TaskActivity, error) {
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
	err = s.db.WithContext(ctx).Raw(`SELECT a.id,a.node_id,COALESCE(a.worker,'') AS worker,COALESCE(a.kind,'') AS kind,COALESCE(a.tool,'') AS tool,COALESCE(a.tool_use_id,'') AS tool_use_id,a.is_error,COALESCE(a.summary,'') AS summary,COALESCE(a.main_seg,0) AS main_seg,a.created_at
      FROM tianqiong_ai.activity a WHERE a.exploration_id=? AND a.worker='mainagent' AND COALESCE(a.main_seg,0)=?
      AND a.kind IN ('human_message','assistant_message','thinking','text','tool_use','tool_result','intercept_request','result')
      AND (a.kind<>'assistant_message' OR a.execution_id IS NULL OR NOT EXISTS(SELECT 1 FROM tianqiong_ai.activity r WHERE r.exploration_id=a.exploration_id AND r.execution_id=a.execution_id AND r.worker='mainagent' AND r.kind='result'))
      AND (?=0 OR a.id<?) ORDER BY a.id DESC LIMIT 101`, task.ExplorationID, segment, before, before).Scan(&items).Error
	return items, err
}

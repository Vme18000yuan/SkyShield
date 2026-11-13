package repository

import (
	"context"

	"github.com/lib/pq"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
)

func (s *TaskStore) hydrateTaskSources(ctx context.Context, items []aiapp.Task) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(items))
	index := make(map[int64]int, len(items))
	for i := range items {
		items[i].SourceTaskIDs = []int64{}
		ids = append(ids, items[i].ID)
		index[items[i].ID] = i
	}
	rows, err := s.db.WithContext(ctx).Raw(`SELECT r.task_id,r.source_task_id FROM ai_task_relation r JOIN ai_task child ON child.id=r.task_id AND child.tenant_id=r.tenant_id JOIN ai_task source ON source.id=r.source_task_id AND source.tenant_id=r.tenant_id AND source.owner_user_id=child.owner_user_id WHERE r.task_id=ANY(?::bigint[]) AND child.deleted_at IS NULL AND source.deleted_at IS NULL AND (source.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=source.target_id AND deleted_at IS NULL)) ORDER BY r.task_id,r.created_at,r.source_task_id`, pq.Int64Array(ids)).Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var taskID, sourceID int64
		if err = rows.Scan(&taskID, &sourceID); err != nil {
			return err
		}
		if i, ok := index[taskID]; ok {
			items[i].SourceTaskIDs = append(items[i].SourceTaskIDs, sourceID)
		}
	}
	return rows.Err()
}

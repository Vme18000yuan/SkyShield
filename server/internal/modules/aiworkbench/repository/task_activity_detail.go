package repository

import (
	"context"
	"database/sql"
	"errors"

	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

func (s *TaskStore) ReadTaskActivityDetail(ctx context.Context, user int, taskID, activityID int64) (string, error) {
	if _, err := s.TrafficTaskTenant(ctx, user, taskID); err != nil {
		return "", err
	}
	var detail string
	var sourceID int64
	err := s.db.WithContext(ctx).Raw(`SELECT COALESCE(a.detail,''),source.id FROM ai_task task JOIN ai_task source ON source.tenant_id=task.tenant_id AND source.owner_user_id=task.owner_user_id AND source.deleted_at IS NULL AND (source.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=source.target_id AND deleted_at IS NULL)) AND NOT EXISTS(SELECT 1 FROM ai_task_archive x WHERE x.task_id=source.id AND x.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting')) JOIN tianqiong_ai.activity a ON a.exploration_id=source.exploration_id LEFT JOIN tianqiong_ai.exploration_nodes n ON n.id=a.node_id JOIN tenant_membership m ON m.tenant_id=task.tenant_id AND m.user_id=task.owner_user_id JOIN tenant tenant ON tenant.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE task.tenant_id=? AND task.owner_user_id=? AND task.id=? AND task.deleted_at IS NULL AND tenant.status='active' AND u.is_active=TRUE AND a.id=? AND (source.id=task.id OR (n.kind='intent' AND COALESCE(a.kind,'') NOT IN ('thinking','usage') AND n.state IN ('done','blocked','exhausted','stopped') AND EXISTS(SELECT 1 FROM ai_task_relation r WHERE r.task_id=task.id AND r.source_task_id=source.id AND r.tenant_id=task.tenant_id)))`, assets.DefaultTenantID, user, taskID, activityID).Row().Scan(&detail, &sourceID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", aiapp.ErrTaskNotFound
	}
	if err != nil {
		return "", err
	}
	if _, err = s.TrafficTaskTenant(ctx, user, taskID); err != nil {
		return "", err
	}
	if sourceID != taskID {
		if _, err = s.TrafficTaskTenant(ctx, user, sourceID); err != nil {
			return "", err
		}
		var linked bool
		if err = s.db.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM ai_task_relation WHERE tenant_id=?::uuid AND task_id=? AND source_task_id=?)`, assets.DefaultTenantID, taskID, sourceID).Scan(&linked).Error; err != nil {
			return "", err
		}
		if !linked {
			return "", aiapp.ErrTaskNotFound
		}
	}
	return detail, nil
}

var _ aiapp.TaskActivityDetailStore = (*TaskStore)(nil)

package repository

import (
	"context"
	"database/sql"
	"errors"

	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

func (s *TaskStore) TrafficTaskTenant(ctx context.Context, user int, taskID int64) (string, error) {
	var tenant string
	err := s.db.WithContext(ctx).Raw(`SELECT t.tenant_id::text FROM ai_task t JOIN tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id JOIN auth_user u ON u.id=m.user_id JOIN tenant tenant ON tenant.id=m.tenant_id
      WHERE t.tenant_id=? AND t.owner_user_id=? AND t.id=? AND t.deleted_at IS NULL AND u.is_active AND tenant.status='active' AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target x WHERE x.id=t.target_id AND x.deleted_at IS NULL)) AND NOT EXISTS(SELECT 1 FROM ai_task_archive a WHERE a.task_id=t.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting'))`, assets.DefaultTenantID, user, taskID).Row().Scan(&tenant)
	if errors.Is(err, sql.ErrNoRows) {
		return "", aiapp.ErrTaskNotFound
	}
	return tenant, err
}

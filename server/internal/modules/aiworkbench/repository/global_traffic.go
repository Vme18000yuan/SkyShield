package repository

import (
	"context"
	"strconv"

	"github.com/lib/pq"
	"github.com/yyhuni/lunafox/server/internal/aitraffic"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

func (s *TaskStore) VisibleTrafficScopes(ctx context.Context, user int, scopes []aitraffic.Scope) ([]aiapp.TrafficScope, error) {
	var allowed bool
	if err := s.db.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE m.tenant_id=?::uuid AND m.user_id=? AND t.status='active' AND u.is_active)`, assets.DefaultTenantID, user).Scan(&allowed).Error; err != nil {
		return nil, err
	}
	if !allowed {
		return nil, aiapp.ErrTaskNotFound
	}
	tasks, conversations := pq.Int64Array{}, pq.StringArray{}
	for _, scope := range scopes {
		if scope.TenantID != assets.DefaultTenantID || scope.UserID != user {
			continue
		}
		if scope.TaskID > 0 && scope.ConversationID == "" {
			tasks = append(tasks, scope.TaskID)
		} else if scope.TaskID == 0 && scope.ConversationID != "" {
			conversations = append(conversations, scope.ConversationID)
		}
	}
	out := []aiapp.TrafficScope{}
	rows, err := s.db.WithContext(ctx).Raw(`SELECT 'task',t.id::text,t.name FROM ai_task t WHERE t.tenant_id=?::uuid AND t.owner_user_id=? AND t.id=ANY(?::bigint[]) AND t.deleted_at IS NULL AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target x WHERE x.id=t.target_id AND x.deleted_at IS NULL)) AND NOT EXISTS(SELECT 1 FROM ai_task_archive a WHERE a.task_id=t.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting'))
 UNION ALL SELECT 'conversation',c.id::text,c.title FROM ai_conversation c WHERE c.tenant_id=?::uuid AND c.owner_user_id=? AND c.id=ANY(?::uuid[]) AND tianqiong_ai.finding_case_review_conversation_visible(c.id)`, assets.DefaultTenantID, user, tasks, assets.DefaultTenantID, user, conversations).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, id, name string
		if err = rows.Scan(&kind, &id, &name); err != nil {
			return nil, err
		}
		scope := aitraffic.Scope{TenantID: assets.DefaultTenantID, UserID: user}
		if kind == "task" {
			task, parseErr := strconv.ParseInt(id, 10, 64)
			if parseErr != nil {
				return nil, parseErr
			}
			scope.TaskID = task
		} else {
			scope.ConversationID = id
		}
		out = append(out, aiapp.TrafficScope{Scope: scope, Name: name})
	}
	return out, rows.Err()
}

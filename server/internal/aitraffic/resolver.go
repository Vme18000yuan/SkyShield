package aitraffic

import (
	"context"
	"database/sql"
	"errors"

	"github.com/yyhuni/lunafox/server/internal/airuntime"
)

func TaskResolver(db *sql.DB, manager *Manager) airuntime.TaskTrafficResolver {
	return func(ctx context.Context, task airuntime.TaskExecution) (*airuntime.TrafficAccess, error) {
		enabled := func(ctx context.Context) (bool, error) {
			var live, on bool
			err := db.QueryRowContext(ctx, `SELECT tianqiong_ai.task_run_live($1,$2,$3::uuid),COALESCE((SELECT options->>'traffic_capture' FROM ai_runtime_setting WHERE tenant_id=$4::uuid),'false')='true'`, task.ID, task.UserID, task.RunID, task.TenantID).Scan(&live, &on)
			if err != nil {
				return false, err
			}
			if !live {
				return false, errors.New("AI task traffic lease inactive")
			}
			return on, nil
		}
		on, err := enabled(ctx)
		if err != nil || !on {
			return nil, err
		}
		lease, err := manager.Acquire(ctx, Scope{TenantID: task.TenantID, UserID: task.UserID, TaskID: task.ID})
		if err != nil {
			return nil, err
		}
		return &airuntime.TrafficAccess{Lease: lease, Enabled: enabled}, nil
	}
}
func ConversationResolver(db *sql.DB, manager *Manager) airuntime.ConversationTrafficResolver {
	return func(ctx context.Context, run airuntime.ChatRun) (*airuntime.TrafficAccess, error) {
		var sourceTask int64
		if err := db.QueryRowContext(ctx, `SELECT COALESCE((SELECT j.source_task_id FROM ai_agent_trigger_job j JOIN ai_task t ON t.id=j.source_task_id AND t.tenant_id=j.tenant_id AND t.owner_user_id=j.owner_user_id WHERE j.run_id=$1::uuid AND j.conversation_id=$2::uuid AND j.tenant_id=$3::uuid AND j.owner_user_id=$4 AND j.status='running' AND t.deleted_at IS NULL AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target x WHERE x.id=t.target_id AND x.deleted_at IS NULL)) LIMIT 1),0)`, run.ExecutionID, run.SessionID, run.TenantID, run.UserID).Scan(&sourceTask); err != nil {
			return nil, err
		}
		enabled := func(ctx context.Context) (bool, error) {
			var live, on bool
			err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_run r JOIN ai_conversation c ON c.id=r.conversation_id AND c.tenant_id=r.tenant_id AND c.owner_user_id=r.owner_user_id JOIN tenant_membership m ON m.tenant_id=c.tenant_id AND m.user_id=c.owner_user_id JOIN auth_user u ON u.id=m.user_id JOIN tenant t ON t.id=m.tenant_id WHERE r.id=$1::uuid AND c.id=$2::uuid AND c.tenant_id=$3::uuid AND c.owner_user_id=$4 AND r.status='running' AND r.lease_until>clock_timestamp() AND u.is_active AND t.status='active' AND ($5::bigint=0 OR EXISTS(SELECT 1 FROM ai_agent_trigger_job j JOIN ai_task source ON source.id=j.source_task_id AND source.tenant_id=j.tenant_id AND source.owner_user_id=j.owner_user_id WHERE j.run_id=r.id AND j.conversation_id=c.id AND j.status='running' AND source.id=$5 AND source.deleted_at IS NULL AND (source.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target x WHERE x.id=source.target_id AND x.deleted_at IS NULL))))),COALESCE((SELECT options->>'traffic_capture' FROM ai_runtime_setting WHERE tenant_id=$3::uuid),'false')='true'`, run.ExecutionID, run.SessionID, run.TenantID, run.UserID, sourceTask).Scan(&live, &on)
			if err != nil {
				return false, err
			}
			if !live {
				return false, errors.New("AI conversation traffic lease inactive")
			}
			return on, nil
		}
		on, err := enabled(ctx)
		if err != nil || !on {
			return nil, err
		}
		scope := Scope{TenantID: run.TenantID, UserID: run.UserID, ConversationID: run.SessionID}
		if sourceTask > 0 {
			scope.ConversationID = ""
			scope.TaskID = sourceTask
		}
		lease, err := manager.Acquire(ctx, scope)
		if err != nil {
			return nil, err
		}
		return &airuntime.TrafficAccess{Lease: lease, Enabled: enabled}, nil
	}
}

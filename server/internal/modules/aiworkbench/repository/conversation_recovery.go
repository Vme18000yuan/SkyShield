package repository

import (
	"context"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

// Recovery records uncertainty; it does not replay a model/tool request.
func (s *ConversationStore) RecoverExpiredRuns(ctx context.Context) (int64, error) {
	result := s.db.WithContext(ctx).Exec(`UPDATE ai_run SET status='interrupted',terminal_reason='lease_expired',
	 error_message='执行租约已过期，结果可能不完整；请检查已有证据后恢复。',finished_at=clock_timestamp(),live_text='',live_kind=''
	 WHERE tenant_id=? AND status IN ('running','stopping') AND lease_until<=clock_timestamp()`, assets.DefaultTenantID)
	return result.RowsAffected, result.Error
}

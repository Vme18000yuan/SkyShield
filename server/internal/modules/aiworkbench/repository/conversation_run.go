package repository

import (
	"context"
	"database/sql"
	"errors"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

func (s *ConversationStore) readConversationRun(ctx context.Context, user int, id string, page *aiapp.ActivityPage) error {
	// State and final cursor share one statement snapshot. A terminal status is
	// published only after all acknowledged event inserts have committed.
	err := s.db.WithContext(ctx).Raw(`SELECT
		CASE WHEN r.status IN ('running','stopping') AND r.lease_until<=clock_timestamp() THEN 'interrupted' ELSE r.status END,
		r.terminal_reason,r.error_message,r.last_progress_at,
		CASE WHEN r.status='running' AND r.lease_until>clock_timestamp() THEN r.live_text ELSE '' END,
		CASE WHEN r.status='running' AND r.lease_until>clock_timestamp() THEN r.live_kind ELSE '' END,
		COALESCE((SELECT MAX(a.id) FROM ai_conversation_activity a WHERE a.tenant_id=r.tenant_id AND a.owner_user_id=r.owner_user_id AND a.conversation_id=r.conversation_id),0)
		FROM ai_run r WHERE r.tenant_id=? AND r.owner_user_id=? AND r.conversation_id=?
		ORDER BY r.created_at DESC,r.id DESC LIMIT 1`, assets.DefaultTenantID, user, id).
		Row().Scan(&page.RunStatus, &page.TerminalReason, &page.ErrorMessage, &page.LastProgressAt, &page.LiveText, &page.LiveKind, &page.FinalCursor)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	page.Running = page.RunStatus == "running" || page.RunStatus == "stopping"
	if page.RunStatus == "interrupted" && page.ErrorMessage == "" {
		page.ErrorMessage = "执行已中断，结果可能不完整。"
	}
	return err
}

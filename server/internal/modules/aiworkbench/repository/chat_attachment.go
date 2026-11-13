package repository

import (
	"context"
	"github.com/lib/pq"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

func (s *ConversationStore) AuthorizeAttachmentScope(ctx context.Context, user int, scope, id string) error {
	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE m.tenant_id=? AND m.user_id=? AND t.status='active' AND u.is_active=TRUE`
	args := []any{assets.DefaultTenantID, user}
	switch scope {
	case "session":
		query += ` AND EXISTS(SELECT 1 FROM ai_conversation c WHERE c.tenant_id=m.tenant_id AND c.owner_user_id=m.user_id AND c.id=? AND tianqiong_ai.finding_case_review_conversation_visible(c.id))`
		args = append(args, id)
	case "task":
		query += ` AND EXISTS(SELECT 1 FROM ai_task task LEFT JOIN target target ON target.id=task.target_id WHERE task.tenant_id=m.tenant_id AND task.owner_user_id=m.user_id AND task.id=? AND task.deleted_at IS NULL AND (task.original_target_id IS NULL OR target.id IS NOT NULL AND target.deleted_at IS NULL))`
		args = append(args, id)
	case "staging":
	default:
		return aiapp.ErrInvalidInput
	}
	query += `)`
	if err := s.db.WithContext(ctx).Raw(query, args...).Scan(&exists).Error; err != nil {
		return err
	}
	if !exists {
		return aiapp.ErrConversationNotFound
	}
	return nil
}
func (s *ConversationStore) SaveChatAttachment(ctx context.Context, user int, scope, id string, item aiapp.ChatAttachment) error {
	if err := s.AuthorizeAttachmentScope(ctx, user, scope, id); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Exec(`INSERT INTO ai_chat_attachment(id,tenant_id,owner_user_id,scope,scope_id,name,path,size) VALUES(?,?,?,?,?,?,?,?)`, item.ID, assets.DefaultTenantID, user, scope, id, item.Name, item.Path, item.Size).Error
}
func (s *ConversationStore) FindChatAttachments(ctx context.Context, user int, scope, id string, paths []string) ([]aiapp.ChatAttachment, error) {
	if err := s.AuthorizeAttachmentScope(ctx, user, scope, id); err != nil {
		return nil, err
	}
	items := []aiapp.ChatAttachment{}
	err := s.db.WithContext(ctx).Raw(`SELECT id::text,name,path,size FROM ai_chat_attachment WHERE tenant_id=? AND owner_user_id=? AND scope=? AND scope_id=? AND path=ANY(?::text[])`, assets.DefaultTenantID, user, scope, id, pq.StringArray(paths)).Scan(&items).Error
	return items, err
}

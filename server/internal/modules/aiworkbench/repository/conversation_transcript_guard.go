package repository

import (
	"context"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

func (s *ConversationStore) RequiresTranscript(ctx context.Context, user int, conversation, run string) (bool, error) {
	var required bool
	err := s.db.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM ai_conversation_activity WHERE tenant_id=? AND owner_user_id=? AND conversation_id=? AND run_id<>?::uuid AND kind='user')`, assets.DefaultTenantID, user, conversation, run).Scan(&required).Error
	return required, err
}

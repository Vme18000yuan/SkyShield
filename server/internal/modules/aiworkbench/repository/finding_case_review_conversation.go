package repository

import (
	"context"

	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

func (s *ConversationStore) IsFindingCaseReviewConversation(ctx context.Context, user int, conversation string) (bool, error) {
	var exists bool
	err := s.db.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM ai_finding_case_review WHERE tenant_id=?::uuid AND owner_user_id=? AND conversation_id=?::uuid)`, assets.DefaultTenantID, user, conversation).Scan(&exists).Error
	return exists, err
}

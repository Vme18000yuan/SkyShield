package repository

import (
	"context"
	"database/sql"
	"errors"

	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

func (s *ConversationStore) TrafficConversationTenant(ctx context.Context, user int, conversation string) (string, error) {
	var tenant string
	err := s.db.WithContext(ctx).Raw(`SELECT c.tenant_id::text`+ownedConversation, assets.DefaultTenantID, user, conversation).Row().Scan(&tenant)
	if errors.Is(err, sql.ErrNoRows) {
		return "", aiapp.ErrTaskNotFound
	}
	return tenant, err
}

package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/lib/pq"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

const ownedConversation = ` FROM ai_conversation c JOIN tenant_membership m ON m.tenant_id=c.tenant_id AND m.user_id=c.owner_user_id JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE c.tenant_id=? AND c.owner_user_id=? AND c.id=? AND t.status='active' AND u.is_active=TRUE AND tianqiong_ai.finding_case_review_conversation_visible(c.id) `

func (s *ConversationStore) ReadConversationHistory(ctx context.Context, user int, id string, before int64, limit int) (*aiapp.ActivityPage, error) {
	var exists bool
	if err := s.db.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1`+ownedConversation+`)`, assets.DefaultTenantID, user, id).Scan(&exists).Error; err != nil {
		return nil, err
	}
	if !exists {
		return nil, aiapp.ErrConversationNotFound
	}
	page := &aiapp.ActivityPage{Items: []aiapp.ConversationActivity{}}
	if err := s.db.WithContext(ctx).Raw(`SELECT a.id,a.run_id::text,c.agent_key AS worker,a.kind,a.tool,a.tool_use_id,a.is_error,left(a.summary,500) summary,a.created_at,a.input_tokens,a.output_tokens,a.cache_read_tokens,a.cache_write_tokens FROM ai_conversation_activity a JOIN ai_conversation c ON c.id=a.conversation_id WHERE a.tenant_id=? AND a.owner_user_id=? AND a.conversation_id=? AND (?=0 OR a.id<?) AND tianqiong_ai.finding_case_review_conversation_visible(c.id) ORDER BY a.id DESC LIMIT ?`, assets.DefaultTenantID, user, id, before, before, limit+1).Scan(&page.Items).Error; err != nil {
		return nil, err
	}
	page.HasMore = len(page.Items) > limit
	if page.HasMore {
		page.Items = page.Items[:limit]
	}
	for left, right := 0, len(page.Items)-1; left < right; left, right = left+1, right-1 {
		page.Items[left], page.Items[right] = page.Items[right], page.Items[left]
	}
	if len(page.Items) > 0 {
		page.Cursor = page.Items[len(page.Items)-1].ID
	}
	if err := s.readConversationRun(ctx, user, id, page); err != nil {
		return nil, err
	}

	if _, err := s.TrafficConversationTenant(ctx, user, id); err != nil {
		if errors.Is(err, aiapp.ErrTaskNotFound) {
			return nil, aiapp.ErrConversationNotFound
		}
		return nil, err
	}
	return page, nil
}
func (s *ConversationStore) ReadConversationActivityDetail(ctx context.Context, user int, id string, activity int64) (string, error) {
	var detail string
	err := s.db.WithContext(ctx).Raw(`SELECT a.detail FROM ai_conversation_activity a JOIN ai_conversation c ON c.id=a.conversation_id JOIN tenant_membership m ON m.tenant_id=c.tenant_id AND m.user_id=c.owner_user_id JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE a.tenant_id=? AND a.owner_user_id=? AND a.conversation_id=? AND a.id=? AND t.status='active' AND u.is_active=TRUE AND tianqiong_ai.finding_case_review_conversation_visible(c.id)`, assets.DefaultTenantID, user, id, activity).Row().Scan(&detail)
	if errors.Is(err, sql.ErrNoRows) {
		return "", aiapp.ErrConversationNotFound
	}
	return detail, err
}
func (s *ConversationStore) DeleteOwnedConversations(ctx context.Context, user int, ids []string) ([]string, error) {
	deleted := []string{}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The dispatcher locks this key before claiming jobs. Lock it first so a
		// queued job cannot recreate its conversation while deletion commits.
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(?,?)`, 0x4147454e, 0x4a4f4253).Error; err != nil {
			return err
		}
		var owned []string
		if err := tx.Raw(`SELECT c.id::text FROM ai_conversation c WHERE c.tenant_id=? AND c.owner_user_id=? AND c.id=ANY(?::uuid[]) AND EXISTS(SELECT 1 FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE m.tenant_id=c.tenant_id AND m.user_id=c.owner_user_id AND t.status='active' AND u.is_active=TRUE) ORDER BY c.id FOR UPDATE OF c`, assets.DefaultTenantID, user, pq.StringArray(ids)).Scan(&owned).Error; err != nil {
			return err
		}
		if len(owned) == 0 {
			return nil
		}
		if err := tx.Exec(`UPDATE ai_agent_trigger_job j SET status='cancelled',error_message='会话已删除',updated_at=CURRENT_TIMESTAMP WHERE j.tenant_id=? AND j.owner_user_id=? AND j.conversation_id=ANY(?::uuid[]) AND j.status IN ('queued','starting','running') AND EXISTS(SELECT 1 FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE m.tenant_id=j.tenant_id AND m.user_id=j.owner_user_id AND t.status='active' AND u.is_active=TRUE)`, assets.DefaultTenantID, user, pq.StringArray(owned)).Error; err != nil {
			return err
		}
		if err := tx.Raw(`DELETE FROM ai_conversation c WHERE c.tenant_id=? AND c.owner_user_id=? AND c.id=ANY(?::uuid[]) AND EXISTS(SELECT 1 FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE m.tenant_id=c.tenant_id AND m.user_id=c.owner_user_id AND t.status='active' AND u.is_active=TRUE) RETURNING c.id::text`, assets.DefaultTenantID, user, pq.StringArray(owned)).Scan(&deleted).Error; err != nil {
			return err
		}
		for _, id := range deleted {
			if err := tx.Exec(`DELETE FROM ai_chat_attachment WHERE tenant_id=? AND owner_user_id=? AND scope='session' AND scope_id=?`, assets.DefaultTenantID, user, id).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return deleted, err
}

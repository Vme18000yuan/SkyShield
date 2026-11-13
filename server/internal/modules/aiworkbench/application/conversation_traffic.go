package application

import (
	"context"

	"github.com/Autumn-27/artex/traffic"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/aitraffic"
)

type ConversationTrafficAuthorizer interface {
	TrafficConversationTenant(context.Context, int, string) (string, error)
}
type ConversationTrafficFacade struct {
	access  ConversationTrafficAuthorizer
	manager TaskTrafficManager
}

func NewConversationTrafficFacade(access ConversationTrafficAuthorizer, manager TaskTrafficManager) *ConversationTrafficFacade {
	return &ConversationTrafficFacade{access: access, manager: manager}
}
func (f *ConversationTrafficFacade) read(ctx context.Context, user int, conversation string, visit func(*traffic.Traffic) error) (bool, error) {
	id, err := uuid.Parse(conversation)
	if f == nil || f.access == nil || f.manager == nil || user <= 0 || err != nil || id.String() != conversation {
		return false, ErrInvalidInput
	}
	tenant, err := f.access.TrafficConversationTenant(ctx, user, conversation)
	if err != nil {
		return false, err
	}
	found, err := f.manager.Read(ctx, aitraffic.Scope{TenantID: tenant, UserID: user, ConversationID: conversation}, visit)
	if err != nil {
		return false, err
	}
	current, err := f.access.TrafficConversationTenant(ctx, user, conversation)
	if err != nil || current != tenant {
		if err == nil {
			err = ErrTaskNotFound
		}
		return false, err
	}
	return found, nil
}
func (f *ConversationTrafficFacade) List(ctx context.Context, user int, conversation string, filter TaskTrafficFilter) (TaskTrafficPage, error) {
	return listTraffic(filter, func(visit func(*traffic.Traffic) error) (bool, error) {
		return f.read(ctx, user, conversation, visit)
	})
}
func (f *ConversationTrafficFacade) Exchange(ctx context.Context, user int, conversation, id string) (string, string, error) {
	return trafficExchange(id, func(visit func(*traffic.Traffic) error) (bool, error) {
		return f.read(ctx, user, conversation, visit)
	})
}
func (f *ConversationTrafficFacade) Blob(ctx context.Context, user int, conversation, hash string, offset, length int64) ([]byte, int64, error) {
	return trafficBlob(hash, offset, length, func(visit func(*traffic.Traffic) error) (bool, error) {
		return f.read(ctx, user, conversation, visit)
	})
}

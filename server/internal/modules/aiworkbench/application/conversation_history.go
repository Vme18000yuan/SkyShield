package application

import "context"

func (f *ConversationFacade) History(ctx context.Context, user int, id string, before int64, limit int) (*ActivityPage, error) {
	if user <= 0 || !validConversationID(id) || before < 0 || limit < 1 || limit > 500 {
		return nil, ErrInvalidInput
	}
	store, ok := f.store.(ConversationHistoryStore)
	if !ok {
		return nil, ErrConversationNotFound
	}
	return store.ReadConversationHistory(ctx, user, id, before, limit)
}
func (f *ConversationFacade) ActivityDetail(ctx context.Context, user int, id string, activity int64) (string, error) {
	if user <= 0 || !validConversationID(id) || activity <= 0 {
		return "", ErrInvalidInput
	}
	store, ok := f.store.(ConversationHistoryStore)
	if !ok {
		return "", ErrConversationNotFound
	}
	return store.ReadConversationActivityDetail(ctx, user, id, activity)
}

type ConversationDeleteItem struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (f *ConversationFacade) DeleteConversations(ctx context.Context, user int, ids []string) ([]ConversationDeleteItem, error) {
	if user <= 0 || len(ids) == 0 || len(ids) > 100 {
		return nil, ErrInvalidInput
	}
	seen := map[string]bool{}
	unique := []string{}
	for _, id := range ids {
		if !validConversationID(id) {
			return nil, ErrInvalidInput
		}
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	store, ok := f.store.(ConversationHistoryStore)
	if !ok {
		return nil, ErrConversationNotFound
	}
	for _, id := range unique {
		_ = f.Stop(ctx, user, id)
	}
	deleted, err := store.DeleteOwnedConversations(ctx, user, unique)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, id := range deleted {
		set[id] = true
	}
	items := make([]ConversationDeleteItem, 0, len(unique))
	for _, id := range unique {
		item := ConversationDeleteItem{ID: id, OK: set[id]}
		if !item.OK {
			item.Error = "conversation not found"
		} else if f.attachments != nil {
			if err := f.attachments.files.RemoveSession(user, id); err != nil {
				item.Error = "会话已删除，附件目录清理失败"
			}
		}
		items = append(items, item)
	}
	return items, nil
}

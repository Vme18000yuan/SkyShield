package application

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

type TaskConstraint struct {
	ID        int64     `json:"id,string"`
	Kind      string    `json:"kind"`
	Text      string    `json:"text"`
	Origin    string    `json:"origin"`
	CreatedAt time.Time `json:"ts"`
}
type TaskConstraintStore interface {
	ListTaskConstraints(context.Context, int, int64) ([]TaskConstraint, error)
	SaveTaskConstraint(context.Context, int, int64, int64, string, string) (*TaskConstraint, error)
	DeleteTaskConstraint(context.Context, int, int64, int64) error
}

func (f *TaskFacade) Constraints(ctx context.Context, user int, taskID int64) ([]TaskConstraint, error) {
	if user <= 0 || taskID <= 0 {
		return nil, ErrInvalidInput
	}
	store, ok := f.store.(TaskConstraintStore)
	if !ok {
		return nil, ErrTaskNotFound
	}
	return store.ListTaskConstraints(ctx, user, taskID)
}
func (f *TaskFacade) SaveConstraint(ctx context.Context, user int, taskID, id int64, kind, text string) (*TaskConstraint, error) {
	kind, text = strings.ToLower(strings.TrimSpace(kind)), strings.TrimSpace(text)
	if user <= 0 || taskID <= 0 || id < 0 || (kind != "allow" && kind != "deny") || text == "" || len(text) > 16000 || !utf8.ValidString(text) || strings.ContainsRune(text, '\x00') {
		return nil, ErrInvalidInput
	}
	store, ok := f.store.(TaskConstraintStore)
	if !ok {
		return nil, ErrTaskNotFound
	}
	return store.SaveTaskConstraint(ctx, user, taskID, id, kind, text)
}
func (f *TaskFacade) DeleteConstraint(ctx context.Context, user int, taskID, id int64) error {
	if user <= 0 || taskID <= 0 || id <= 0 {
		return ErrInvalidInput
	}
	store, ok := f.store.(TaskConstraintStore)
	if !ok {
		return ErrTaskNotFound
	}
	return store.DeleteTaskConstraint(ctx, user, taskID, id)
}

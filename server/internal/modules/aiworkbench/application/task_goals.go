package application

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

type TaskGoal struct {
	ID        int64     `json:"id,string"`
	Text      string    `json:"text"`
	VulnClass string    `json:"vulnclass,omitempty"`
	State     string    `json:"state"`
	Origin    string    `json:"origin,omitempty"`
	CreatedAt time.Time `json:"ts"`
}
type TaskGoalStore interface {
	ListTaskGoals(context.Context, int, int64) ([]TaskGoal, error)
	SaveTaskGoal(context.Context, int, int64, int64, string, string) (*TaskGoal, error)
	DeleteTaskGoal(context.Context, int, int64, int64) error
}

func (f *TaskFacade) Goals(ctx context.Context, user int, taskID int64) ([]TaskGoal, error) {
	if user <= 0 || taskID <= 0 {
		return nil, ErrInvalidInput
	}
	store, ok := f.store.(TaskGoalStore)
	if !ok {
		return nil, ErrTaskNotFound
	}
	return store.ListTaskGoals(ctx, user, taskID)
}
func (f *TaskFacade) SaveGoal(ctx context.Context, user int, taskID, id int64, text, vulnClass string) (*TaskGoal, error) {
	text, vulnClass = strings.TrimSpace(text), strings.TrimSpace(vulnClass)
	if user <= 0 || taskID <= 0 || id < 0 || text == "" || len(text) > 16000 || len(vulnClass) > 1000 || !utf8.ValidString(text+vulnClass) || strings.ContainsRune(text+vulnClass, '\x00') {
		return nil, ErrInvalidInput
	}
	store, ok := f.store.(TaskGoalStore)
	if !ok {
		return nil, ErrTaskNotFound
	}
	goal, err := store.SaveTaskGoal(ctx, user, taskID, id, text, vulnClass)
	if err == nil && f.controller != nil {
		f.controller.Notify()
	}
	return goal, err
}
func (f *TaskFacade) DeleteGoal(ctx context.Context, user int, taskID, id int64) error {
	if user <= 0 || taskID <= 0 || id <= 0 {
		return ErrInvalidInput
	}
	store, ok := f.store.(TaskGoalStore)
	if !ok {
		return ErrTaskNotFound
	}
	return store.DeleteTaskGoal(ctx, user, taskID, id)
}

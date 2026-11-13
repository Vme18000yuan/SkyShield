package application

import (
	"context"
	"strings"
	"unicode/utf8"
)

type TaskMetadataUpdate struct {
	Name          *string
	Remark        *string
	Pinned        *bool
	CategoryID    *int64
	SetCategoryID bool
}

type TaskListFilter struct {
	Query      string
	Status     string
	CategoryID *int64
	NoCategory bool
}

type TaskMetadataStore interface {
	UpdateTaskMetadata(context.Context, int, int64, TaskMetadataUpdate) (*Task, error)
	ListTaskPage(context.Context, int, int64, TaskListFilter) ([]Task, error)
}

func (f *TaskFacade) UpdateMetadata(ctx context.Context, user int, id int64, update TaskMetadataUpdate) (*Task, error) {
	if user <= 0 || id <= 0 || update.Name == nil && update.Remark == nil && update.Pinned == nil && !update.SetCategoryID || update.CategoryID != nil && *update.CategoryID <= 0 {
		return nil, ErrInvalidInput
	}
	if update.Name != nil {
		name := strings.TrimSpace(*update.Name)
		if name == "" || !utf8.ValidString(name) || strings.ContainsRune(name, '\x00') || utf8.RuneCountInString(name) > 200 {
			return nil, ErrInvalidInput
		}
		update.Name = &name
	}
	if update.Remark != nil {
		remark := strings.TrimSpace(*update.Remark)
		if !utf8.ValidString(remark) || strings.ContainsRune(remark, '\x00') || utf8.RuneCountInString(remark) > 2000 {
			return nil, ErrInvalidInput
		}
		update.Remark = &remark
	}
	store, ok := f.store.(TaskMetadataStore)
	if !ok {
		return nil, ErrTaskNotFound
	}
	return store.UpdateTaskMetadata(ctx, user, id, update)
}

func (f *TaskFacade) ListFiltered(ctx context.Context, user int, before int64, filter TaskListFilter) ([]Task, error) {
	filter.Query = strings.TrimSpace(filter.Query)
	if user <= 0 || before < 0 || len(filter.Query) > 1000 || !utf8.ValidString(filter.Query) || strings.ContainsRune(filter.Query, '\x00') || filter.CategoryID != nil && (*filter.CategoryID <= 0 || filter.NoCategory) {
		return nil, ErrInvalidInput
	}
	switch filter.Status {
	case "", "created", "queued", "running", "paused", "done", "failed", "timeout", "stopped":
	default:
		return nil, ErrInvalidInput
	}
	store, ok := f.store.(TaskMetadataStore)
	if !ok {
		return nil, ErrTaskNotFound
	}
	return store.ListTaskPage(ctx, user, before, filter)
}

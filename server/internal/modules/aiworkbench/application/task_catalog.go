package application

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	aiapproval "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/application"
)

type TaskCategory struct {
	ID        int64     `json:"id,string"`
	Name      string    `json:"name"`
	TaskCount int64     `json:"task_count"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type TaskTemplate struct {
	ID             int64              `json:"id,string"`
	Name           string             `json:"name"`
	Description    string             `json:"description"`
	Goal           string             `json:"goal"`
	Remark         string             `json:"remark"`
	CategoryID     *int64             `json:"category_id,string"`
	InterceptRules []TaskTemplateRule `json:"intercept_rules" gorm:"-"`
	WorkerCount    int                `json:"worker_count"`
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
}

type TaskTemplateRule struct {
	Enabled bool   `json:"enabled"`
	Action  string `json:"action"`
	Kind    string `json:"kind"`
	Pattern string `json:"pattern"`
	Note    string `json:"note"`
}

type TaskCatalogStore interface {
	ListTaskCategories(context.Context, int) ([]TaskCategory, error)
	SaveTaskCategory(context.Context, int, TaskCategory, bool) (TaskCategory, error)
	DeleteTaskCategory(context.Context, int, int64) error
	ListTaskTemplates(context.Context, int) ([]TaskTemplate, error)
	SaveTaskTemplate(context.Context, int, TaskTemplate, bool) (TaskTemplate, error)
	DeleteTaskTemplate(context.Context, int, int64) error
}

func (f *TaskFacade) catalog() (TaskCatalogStore, error) {
	store, ok := f.store.(TaskCatalogStore)
	if !ok {
		return nil, ErrTaskNotFound
	}
	return store, nil
}
func (f *TaskFacade) Categories(ctx context.Context, user int) ([]TaskCategory, error) {
	if user <= 0 {
		return nil, ErrInvalidInput
	}
	store, err := f.catalog()
	if err != nil {
		return nil, err
	}
	return store.ListTaskCategories(ctx, user)
}
func (f *TaskFacade) SaveCategory(ctx context.Context, user int, item TaskCategory, create bool) (TaskCategory, error) {
	item.Name = strings.Join(strings.Fields(item.Name), " ")
	if user <= 0 || !create && item.ID <= 0 || item.Name == "" || !utf8.ValidString(item.Name) || strings.ContainsRune(item.Name, '\x00') || utf8.RuneCountInString(item.Name) > 80 {
		return TaskCategory{}, ErrInvalidInput
	}
	store, err := f.catalog()
	if err != nil {
		return TaskCategory{}, err
	}
	return store.SaveTaskCategory(ctx, user, item, create)
}
func (f *TaskFacade) DeleteCategory(ctx context.Context, user int, id int64) error {
	if user <= 0 || id <= 0 {
		return ErrInvalidInput
	}
	store, err := f.catalog()
	if err != nil {
		return err
	}
	return store.DeleteTaskCategory(ctx, user, id)
}
func (f *TaskFacade) Templates(ctx context.Context, user int) ([]TaskTemplate, error) {
	if user <= 0 {
		return nil, ErrInvalidInput
	}
	store, err := f.catalog()
	if err != nil {
		return nil, err
	}
	return store.ListTaskTemplates(ctx, user)
}
func (f *TaskFacade) SaveTemplate(ctx context.Context, user int, item TaskTemplate, create bool) (TaskTemplate, error) {
	item.Name = strings.Join(strings.Fields(item.Name), " ")
	item.Description, item.Goal, item.Remark = strings.TrimSpace(item.Description), strings.TrimSpace(item.Goal), strings.TrimSpace(item.Remark)
	if user <= 0 || !create && item.ID <= 0 || item.Name == "" || item.Description == "" || item.Goal == "" || item.WorkerCount < 0 || item.WorkerCount > 50 || item.CategoryID != nil && *item.CategoryID <= 0 || len(item.InterceptRules) > 100 || !utf8.ValidString(item.Name+item.Description+item.Goal+item.Remark) || strings.ContainsRune(item.Name+item.Description+item.Goal+item.Remark, '\x00') || utf8.RuneCountInString(item.Name) > 120 || utf8.RuneCountInString(item.Description) > 16000 || utf8.RuneCountInString(item.Goal) > 16000 || utf8.RuneCountInString(item.Remark) > 2000 {
		return TaskTemplate{}, ErrInvalidInput
	}
	for i, rule := range item.InterceptRules {
		normalized, err := aiapproval.NormalizeAssetRule(1, aiapproval.AssetRule{Enabled: rule.Enabled, Action: rule.Action, Kind: rule.Kind, Pattern: rule.Pattern, Note: rule.Note})
		if err != nil {
			return TaskTemplate{}, ErrInvalidInput
		}
		item.InterceptRules[i] = TaskTemplateRule{Enabled: normalized.Enabled, Action: normalized.Action, Kind: normalized.Kind, Pattern: normalized.Pattern, Note: normalized.Note}
	}
	store, err := f.catalog()
	if err != nil {
		return TaskTemplate{}, err
	}
	return store.SaveTaskTemplate(ctx, user, item, create)
}
func (f *TaskFacade) DeleteTemplate(ctx context.Context, user int, id int64) error {
	if user <= 0 || id <= 0 {
		return ErrInvalidInput
	}
	store, err := f.catalog()
	if err != nil {
		return err
	}
	return store.DeleteTaskTemplate(ctx, user, id)
}

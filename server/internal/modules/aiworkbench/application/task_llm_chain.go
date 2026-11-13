package application

import (
	"context"
)

func normalizeTaskLLMChain(ids []string, active *string) ([]string, *string, error) {
	if len(ids) > 50 {
		return nil, nil, ErrInvalidInput
	}
	out := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if !validConversationID(id) || seen[id] {
			return nil, nil, ErrInvalidInput
		}
		seen[id] = true
		out = append(out, id)
	}
	if active != nil && *active == "" {
		active = nil
	}
	if active != nil && !seen[*active] {
		return nil, nil, ErrInvalidInput
	}
	if active == nil && len(out) > 0 {
		value := out[0]
		active = &value
	}
	return out, active, nil
}

func (f *TaskFacade) TaskModels(ctx context.Context, user int, id int64) (TaskLLMState, error) {
	if user <= 0 || id <= 0 {
		return TaskLLMState{}, ErrInvalidInput
	}
	store, ok := f.store.(TaskLLMStore)
	if !ok {
		return TaskLLMState{}, ErrTaskNotFound
	}
	return store.TaskLLMState(ctx, user, id)
}
func (f *TaskFacade) ReplaceTaskModels(ctx context.Context, user int, id int64, ids []string, active *string) (TaskLLMState, error) {
	if user <= 0 || id <= 0 {
		return TaskLLMState{}, ErrInvalidInput
	}
	ids, active, err := normalizeTaskLLMChain(ids, active)
	if err != nil {
		return TaskLLMState{}, err
	}
	store, ok := f.store.(TaskLLMStore)
	if !ok {
		return TaskLLMState{}, ErrTaskNotFound
	}
	state, err := store.ReplaceTaskLLMChain(ctx, user, id, ids, active)
	if err == nil && f.controller != nil {
		f.controller.Notify()
	}
	return state, err
}

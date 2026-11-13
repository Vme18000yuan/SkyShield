package application

import "context"

type TaskIntentRerunStore interface {
	RerunTaskIntent(context.Context, int, int64, int64) (int64, error)
}

// RerunIntent reopens one failed intent, or all blocked intents when intentID is zero.
func (f *TaskFacade) RerunIntent(ctx context.Context, user int, taskID, intentID int64) (int64, error) {
	if user <= 0 || taskID <= 0 || intentID < 0 {
		return 0, ErrInvalidInput
	}
	store, ok := f.store.(TaskIntentRerunStore)
	if !ok {
		return 0, ErrTaskNotFound
	}
	count, err := store.RerunTaskIntent(ctx, user, taskID, intentID)
	if err == nil && count > 0 && f.controller != nil {
		f.controller.Notify()
	}
	return count, err
}

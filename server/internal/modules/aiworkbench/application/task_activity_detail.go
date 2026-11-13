package application

import "context"

type TaskActivityDetailStore interface {
	ReadTaskActivityDetail(context.Context, int, int64, int64) (string, error)
}

func (f *TaskFacade) ActivityDetail(ctx context.Context, user int, taskID, activityID int64) (string, error) {
	if user <= 0 || taskID <= 0 || activityID <= 0 {
		return "", ErrInvalidInput
	}
	store, ok := f.store.(TaskActivityDetailStore)
	if !ok {
		return "", ErrTaskNotFound
	}
	return store.ReadTaskActivityDetail(ctx, user, taskID, activityID)
}

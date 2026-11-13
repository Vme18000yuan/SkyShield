package application

import "context"

type TaskMainTranscriptStore interface {
	ListMainTranscript(context.Context, int, int64, int, int64) ([]TaskActivity, error)
}

func (f *TaskFacade) MainTranscript(ctx context.Context, user int, taskID int64, segment int, before int64) ([]TaskActivity, error) {
	if user <= 0 || taskID <= 0 || segment < 0 || before < 0 {
		return nil, ErrInvalidInput
	}
	store, ok := f.store.(TaskMainTranscriptStore)
	if !ok {
		return nil, ErrTaskNotFound
	}
	return store.ListMainTranscript(ctx, user, taskID, segment, before)
}

package application

import (
	"context"
	"encoding/hex"
	"strings"
	"unicode/utf8"
)

type AutoTaskMutation struct {
	RunID, ToolUseID, InputHash string
	// Replayed is set by the repository when the mutation was already committed.
	Replayed *bool
}
type autoTaskMutationKey struct{}

func validAutoTaskMutation(value AutoTaskMutation) bool {
	_, err := hex.DecodeString(value.InputHash)
	return validConversationID(value.RunID) && value.ToolUseID != "" && len(value.ToolUseID) <= 256 && utf8.ValidString(value.ToolUseID) && !strings.ContainsRune(value.ToolUseID, '\x00') && len(value.InputHash) == 64 && err == nil && strings.ToLower(value.InputHash) == value.InputHash
}

func AutoTaskMutationFromContext(ctx context.Context) (AutoTaskMutation, bool) {
	value, ok := ctx.Value(autoTaskMutationKey{}).(AutoTaskMutation)
	return value, ok
}

func (f *TaskFacade) PauseFromAuto(ctx context.Context, user int, taskID int64, mutation AutoTaskMutation) (*Task, error) {
	if user <= 0 || taskID <= 0 || !validAutoTaskMutation(mutation) {
		return nil, ErrInvalidInput
	}
	replayed := false
	mutation.Replayed = &replayed
	ctx = context.WithValue(ctx, autoTaskMutationKey{}, mutation)
	task, err := f.store.ControlTask(ctx, user, taskID, "pause", false)
	if err == nil && f.controller != nil {
		f.controller.Notify()
		if !replayed {
			f.controller.CancelByAuto(taskID)
		}
	}
	return task, err
}

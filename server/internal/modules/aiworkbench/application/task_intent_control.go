package application

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

type TaskIntentControlStore interface {
	QueueIntentPause(context.Context, int, int64, int64) (string, error)
	IntentPauseStatus(context.Context, int, int64, string) (string, error)
	ResumeTaskIntent(context.Context, int, int64, int64) error
}

type TaskIntentDeleteStore interface {
	CancelTaskIntent(context.Context, int, int64, int64, string, string) (string, string, error)
	IntentPauseStatus(context.Context, int, int64, string) (string, error)
}

func (f *TaskFacade) CancelIntent(ctx context.Context, user int, taskID, intentID int64, reason, mode string) (string, error) {
	reason = strings.TrimSpace(reason)
	if mode == "" {
		mode = "soft"
	}
	if user <= 0 || taskID <= 0 || intentID <= 0 || reason == "" || len(reason) > 4000 || !utf8.ValidString(reason) || strings.ContainsRune(reason, '\x00') || (mode != "soft" && mode != "hard") {
		return "", ErrInvalidInput
	}
	store, ok := f.store.(TaskIntentDeleteStore)
	if !ok {
		return "", ErrTaskNotFound
	}
	id, state, err := store.CancelTaskIntent(ctx, user, taskID, intentID, reason, mode)
	if err != nil {
		return "", err
	}
	if f.controller != nil {
		f.controller.Notify()
	}
	if state != "pending" {
		return state, nil
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(20 * time.Second)
	defer timeout.Stop()
	for {
		status, err := store.IntentPauseStatus(ctx, user, taskID, id)
		if err != nil {
			return "", err
		}
		switch status {
		case "deleted":
			return "deleted", nil
		case "conflict":
			return "", ErrTaskControlConflict
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timeout.C:
			return "pending", nil
		case <-ticker.C:
		}
	}
}

// ControlIntent waits briefly for a paused worker to settle. If it is still
// stopping, the durable request remains pending and the graph will update later.
func (f *TaskFacade) ControlIntent(ctx context.Context, user int, taskID, intentID int64, action string) (string, error) {
	if user <= 0 || taskID <= 0 || intentID <= 0 || (action != "pause" && action != "resume") {
		return "", ErrInvalidInput
	}
	store, ok := f.store.(TaskIntentControlStore)
	if !ok {
		return "", ErrTaskNotFound
	}
	if action == "resume" {
		if err := store.ResumeTaskIntent(ctx, user, taskID, intentID); err != nil {
			return "", err
		}
		if f.controller != nil {
			f.controller.Notify()
		}
		return "open", nil
	}
	id, err := store.QueueIntentPause(ctx, user, taskID, intentID)
	if err != nil {
		return "", err
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(20 * time.Second)
	defer timeout.Stop()
	for {
		status, err := store.IntentPauseStatus(ctx, user, taskID, id)
		if err != nil {
			return "", err
		}
		switch status {
		case "paused":
			return "paused", nil
		case "conflict":
			return "", ErrTaskControlConflict
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timeout.C:
			return "pending", nil
		case <-ticker.C:
		}
	}
}

package application

import (
	"context"
	"strings"
	"unicode/utf8"
)

type TaskIntentMessageStore interface {
	ExistingIntentMessage(context.Context, int, int64, int64, string, string) (bool, error)
	SendIntentMessage(context.Context, int, int64, int64, string, string, string) error
}

func validIntentMessageRequestID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, ch := range id {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_' || ch == '.' || ch == ':' {
			continue
		}
		return false
	}
	return true
}

func (f *TaskFacade) SendWorkerMessage(ctx context.Context, user int, taskID, intentID int64, requestID, message string) error {
	requestID, message = strings.TrimSpace(requestID), strings.TrimSpace(message)
	if user <= 0 || taskID <= 0 || intentID <= 0 || !validIntentMessageRequestID(requestID) || message == "" || !utf8.ValidString(message) || strings.ContainsRune(message, '\x00') || utf8.RuneCountInString(message) > 4000 {
		return ErrInvalidInput
	}
	store, ok := f.store.(TaskIntentMessageStore)
	if !ok {
		return ErrTaskNotFound
	}
	exists, err := store.ExistingIntentMessage(ctx, user, taskID, intentID, requestID, message)
	if err != nil || exists {
		return err
	}
	modelText := message
	if f.mentions != nil {
		modelText, err = f.mentions.Compose(ctx, user, message)
		if err != nil {
			return err
		}
	}
	if len(modelText) > 384*1024 {
		return ErrInvalidInput
	}
	err = store.SendIntentMessage(ctx, user, taskID, intentID, requestID, message, modelText)
	if err == nil && f.controller != nil {
		f.controller.Notify()
	}
	return err
}

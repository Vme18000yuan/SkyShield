package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yyhuni/lunafox/server/internal/airuntime"
)

type TaskMainSession struct {
	Seq       int        `json:"seq"`
	CreatedAt *time.Time `json:"createdAt"`
}

type TaskMainSessionStore interface {
	ListMainSessions(context.Context, int, int64) ([]TaskMainSession, error)
	NewMainSession(context.Context, int, int64) (TaskMainSession, error)
	ListMainMessages(context.Context, int, int64, int, int64) ([]TaskActivity, error)
}

func (f *TaskFacade) MainMessages(ctx context.Context, user int, taskID int64, segment int, before int64) ([]TaskActivity, error) {
	if user <= 0 || taskID <= 0 || segment < 0 || before < 0 {
		return nil, ErrInvalidInput
	}
	store, ok := f.store.(TaskMainSessionStore)
	if !ok {
		return nil, ErrTaskNotFound
	}
	return store.ListMainMessages(ctx, user, taskID, segment, before)
}

func (f *TaskFacade) MainSessions(ctx context.Context, user int, taskID int64) ([]TaskMainSession, error) {
	if user <= 0 || taskID <= 0 {
		return nil, ErrInvalidInput
	}
	store, ok := f.store.(TaskMainSessionStore)
	if !ok {
		return nil, ErrTaskNotFound
	}
	return store.ListMainSessions(ctx, user, taskID)
}

func (f *TaskFacade) CreateMainSession(ctx context.Context, user int, taskID int64) (TaskMainSession, error) {
	if user <= 0 || taskID <= 0 {
		return TaskMainSession{}, ErrInvalidInput
	}
	store, ok := f.store.(TaskMainSessionStore)
	if !ok {
		return TaskMainSession{}, ErrTaskNotFound
	}
	return store.NewMainSession(ctx, user, taskID)
}

func (f *TaskFacade) mainSegmentContext(ctx context.Context, user int, taskID int64, segment *int) (context.Context, error) {
	if segment == nil {
		return ctx, nil
	}
	if *segment < 0 {
		return ctx, ErrInvalidInput
	}
	sessions, err := f.MainSessions(ctx, user, taskID)
	if err != nil {
		return ctx, err
	}
	for _, item := range sessions {
		if item.Seq == *segment {
			return airuntime.WithTaskChatSegment(ctx, *segment), nil
		}
	}
	return ctx, ErrTaskNotFound
}
func (f *TaskFacade) ChatInSegment(ctx context.Context, user int, taskID int64, message string, segment *int) (string, error) {
	ctx, err := f.mainSegmentContext(ctx, user, taskID, segment)
	if err != nil {
		return "", err
	}
	return f.Chat(ctx, user, taskID, message)
}
func (f *TaskFacade) SetAttachments(attachments *AttachmentFacade) { f.attachments = attachments }
func (f *TaskFacade) StartMainChat(ctx context.Context, user int, taskID int64, message string, segment *int, attachments []ChatAttachment) error {
	ctx, err := f.mainSegmentContext(ctx, user, taskID, segment)
	if err != nil {
		return err
	}
	original := strings.TrimSpace(message)
	ctx, message, err = f.prepareChat(ctx, user, taskID, original, len(attachments) > 0)
	if err != nil {
		return err
	}
	if len(attachments) > 0 {
		if f.attachments == nil {
			return ErrInvalidInput
		}
		composed, activity, err := f.attachments.Compose(ctx, user, "task", fmt.Sprint(taskID), original, attachments)
		if err != nil {
			return err
		}
		message += strings.TrimPrefix(composed, original)
		display := original
		if display == "" {
			display = activity.Summary
		}
		ctx = airuntime.WithTaskChatDisplay(ctx, display)
		ctx = airuntime.WithTaskChatDetail(ctx, activity.Detail)
	}
	return f.controller.StartChat(ctx, user, taskID, message)
}

func (f *TaskFacade) MainChatRunning(ctx context.Context, user int, taskID int64) (bool, error) {
	if user <= 0 || taskID <= 0 {
		return false, ErrInvalidInput
	}
	if f.controller == nil {
		return false, ErrTaskChatUnavailable
	}
	return f.controller.ChatRunning(ctx, user, taskID)
}
func (f *TaskFacade) StopMainChat(ctx context.Context, user int, taskID int64) (bool, error) {
	if user <= 0 || taskID <= 0 {
		return false, ErrInvalidInput
	}
	if f.controller == nil {
		return false, ErrTaskChatUnavailable
	}
	return f.controller.StopChat(ctx, user, taskID)
}

package application

import (
	"context"
	"errors"
	"github.com/yyhuni/lunafox/server/internal/pkg"
	"sync"
	"time"
)

type AgentTriggerJob struct {
	ID             int64
	OwnerUserID    int
	AgentKey       string
	ConversationID string
	Message        string
}
type AgentTriggerJobStore interface {
	InitializeAgentEvents(context.Context) error
	CollectAgentEvents(context.Context, time.Time) error
	ReconcileAgentJobs(context.Context) error
	ClaimAgentJob(context.Context) (*AgentTriggerJob, error)
	ReleaseAgentJob(context.Context, int64, bool, string) error
}
type triggerJobContextKey struct{}

func WithAgentTriggerJob(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, triggerJobContextKey{}, id)
}
func AgentTriggerJobFromContext(ctx context.Context) int64 {
	value, _ := ctx.Value(triggerJobContextKey{}).(int64)
	return value
}

type AgentTriggerController struct {
	store         AgentTriggerJobStore
	conversations *ConversationFacade
	seed          func(context.Context) error
	once          sync.Once
	done          chan struct{}
}

func NewAgentTriggerController(store AgentTriggerJobStore, conversations *ConversationFacade, seed func(context.Context) error) *AgentTriggerController {
	return &AgentTriggerController{store: store, conversations: conversations, seed: seed, done: make(chan struct{})}
}
func (c *AgentTriggerController) Start(ctx context.Context) { c.once.Do(func() { go c.run(ctx) }) }
func (c *AgentTriggerController) Done() <-chan struct{}     { return c.done }
func (c *AgentTriggerController) run(ctx context.Context) {
	defer close(c.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	initialized := false
	for ctx.Err() == nil {
		if !initialized {
			if err := c.store.InitializeAgentEvents(ctx); err == nil {
				initialized = true
			}
		}
		if initialized {
			if err := c.Tick(ctx, time.Now()); err != nil && ctx.Err() == nil {
				pkg.Warn("AI Agent trigger reconciliation failed")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (c *AgentTriggerController) Tick(ctx context.Context, now time.Time) error {
	if c.seed != nil {
		if err := c.seed(ctx); err != nil {
			return err
		}
	}
	if err := c.store.CollectAgentEvents(ctx, now); err != nil {
		return err
	}
	if err := c.store.ReconcileAgentJobs(ctx); err != nil {
		return err
	}
	for i := 0; i < 32; i++ {
		job, err := c.store.ClaimAgentJob(ctx)
		if err != nil {
			return err
		}
		if job == nil {
			return nil
		}
		_, err = c.conversations.Send(WithAgentTriggerJob(ctx, job.ID), job.OwnerUserID, job.ConversationID, job.Message)
		if err != nil {
			waiting := errors.Is(err, ErrModelNotConfigured) || errors.Is(err, ErrConversationBusy) || errors.Is(err, ErrAgentDisabled)
			message := "触发运行启动失败"
			if waiting {
				message = "等待模型、Agent 或会话就绪"
			}
			if releaseErr := c.store.ReleaseAgentJob(ctx, job.ID, waiting, message); releaseErr != nil {
				return releaseErr
			}
			if waiting {
				return nil
			}
		}
	}
	return nil
}

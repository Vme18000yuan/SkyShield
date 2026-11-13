package airuntime

import (
	"context"
	"errors"
	"github.com/Autumn-27/norma/llm"
	"github.com/google/uuid"
	"iter"
	"time"
)

var ErrExecutionCapacity = errors.New("AI 并发容量已满，请等待当前执行结束后重试")

type ExecutionPermitStore interface {
	ClaimExecution(context.Context, int, string, string, string) (bool, error)
	RenewExecution(context.Context, string, string) (bool, error)
	ReleaseExecution(context.Context, string, string) error
}

// ExecutionGovernor shares root/model admission across server instances. Renew
// failure cancels the holder; permits never authorize command replay.
type ExecutionGovernor struct {
	store ExecutionPermitStore
	owner string
}

func NewExecutionGovernor(store ExecutionPermitStore) *ExecutionGovernor {
	return &ExecutionGovernor{store: store, owner: uuid.NewString()}
}

func (g *ExecutionGovernor) Acquire(ctx context.Context, user int, kind string, wait bool) (context.Context, func(), error) {
	if g == nil {
		return ctx, func() {}, nil
	}
	if user <= 0 {
		return nil, nil, errors.New("AI 执行身份无效")
	}
	id := uuid.NewString()
	queueCtx := ctx
	if wait {
		var stop context.CancelFunc
		queueCtx, stop = context.WithTimeout(ctx, 10*time.Minute)
		defer stop()
	}
	waiting := false
	for {
		call, stop := context.WithTimeout(queueCtx, 3*time.Second)
		ok, err := g.store.ClaimExecution(call, user, id, g.owner, kind)
		stop()
		if err != nil {
			return nil, nil, err
		}
		if ok {
			if waiting {
				executionProgress(ctx, "已获得执行名额，继续运行。")
			}
			break
		}
		if !wait {
			return nil, nil, ErrExecutionCapacity
		}
		if !waiting {
			executionProgress(ctx, "正在等待 AI 执行容量，当前任务仍在队列中。所用时间计入运行预算。")
			waiting = true
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-queueCtx.Done():
			timer.Stop()
			return nil, nil, queueCtx.Err()
		case <-timer.C:
		}
	}
	run, cancel := context.WithCancelCause(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-run.Done():
				return
			case <-ticker.C:
				check, stop := context.WithTimeout(run, 3*time.Second)
				ok, err := g.store.RenewExecution(check, id, g.owner)
				stop()
				if err != nil || !ok {
					cancel(errors.New("AI 执行配额租约失效"))
					return
				}
			}
		}
	}()
	return run, func() {
		cancel(nil)
		<-done
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = g.store.ReleaseExecution(cleanup, id, g.owner)
	}, nil
}

type governedProvider struct {
	llm.Provider
	governor *ExecutionGovernor
	user     int
	profile  string
}

func (p governedProvider) RuntimeModelOptions() RuntimeModelOptions { return ModelOptions(p.Provider) }
func (p governedProvider) ContextWindowTokens() int                 { return ModelOptions(p.Provider).ContextWindow }
func (g *ExecutionGovernor) Provider(provider llm.Provider, user int) llm.Provider {
	if g == nil || provider == nil {
		return provider
	}
	return governedProvider{Provider: provider, governor: g, user: user}
}

func (g *ExecutionGovernor) ProfileProvider(provider llm.Provider, profile string) llm.Provider {
	if g == nil || provider == nil {
		return provider
	}
	return governedProvider{Provider: provider, governor: g, profile: profile}
}
func (p governedProvider) scope(ctx context.Context) (int, string) {
	user := p.user
	if user <= 0 {
		user, _ = ctx.Value(usageOwnerKey{}).(int)
	}
	kind := "model"
	if p.profile != "" {
		kind += ":" + p.profile
	}
	return user, kind
}
func (p governedProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	user, kind := p.scope(ctx)
	run, release, err := p.governor.Acquire(ctx, user, kind, true)
	if err != nil {
		return llm.Message{}, "", llm.Usage{}, err
	}
	defer release()
	return p.Provider.Complete(run, req)
}
func (p governedProvider) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		user, kind := p.scope(ctx)
		run, release, err := p.governor.Acquire(ctx, user, kind, true)
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}
		defer release()
		for ev, err := range p.Provider.Stream(run, req) {
			if !yield(ev, err) {
				return
			}
		}
	}
}

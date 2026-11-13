package agent

import (
	"context"
	"errors"
	"github.com/Autumn-27/norma/llm"
	"iter"
	"sync"
	"time"
)

var ErrModelIdle = errors.New("模型长时间没有返回有效输出，本次调用已停止")

type boundedProvider struct {
	llm.Provider
	limiter            *RequestLimiter
	first, idle, total time.Duration
	concurrency        int
}

func positiveDuration(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}

func (p boundedProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	ctx, cancel := context.WithTimeout(ctx, p.total)
	defer cancel()
	release, err := p.limiter.acquire(ctx, p.concurrency)
	if err != nil {
		return llm.Message{}, "", llm.Usage{}, err
	}
	defer release()
	message, reason, usage, err := p.Provider.Complete(ctx, req)
	observeBudget(ctx, usage)
	return message, reason, usage, err
}

func (p boundedProvider) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		parent, stop := context.WithTimeout(ctx, p.total)
		defer stop()
		release, err := p.limiter.acquire(parent, p.concurrency)
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}
		defer release()
		run, cancel := context.WithCancelCause(parent)
		defer cancel(nil)
		var mu sync.Mutex
		expires := time.Now().Add(p.first)
		var timer *time.Timer
		var watch func()
		watch = func() {
			mu.Lock()
			defer mu.Unlock()
			if run.Err() != nil {
				return
			}
			remaining := time.Until(expires)
			if remaining > 0 {
				timer.Reset(remaining)
			} else {
				cancel(ErrModelIdle)
			}
		}
		mu.Lock()
		timer = time.AfterFunc(p.first, watch)
		mu.Unlock()
		defer func() { mu.Lock(); cancel(nil); timer.Stop(); mu.Unlock() }()
		usage := llm.NewAccumulator()
		defer func() { observeBudget(ctx, usage.Usage) }()
		for event, err := range p.Provider.Stream(run, req) {
			if err != nil {
				if run.Err() != nil {
					err = context.Cause(run)
				}
				yield(llm.StreamEvent{}, err)
				return
			}
			usage.Add(event)
			// Empty keep-alives must not indefinitely postpone a stalled model.
			if event.Text != "" || event.Type == llm.SEToolUseStart || event.Type == llm.SEMessageStop {
				mu.Lock()
				expires = time.Now().Add(p.idle)
				timer.Reset(p.idle)
				mu.Unlock()
			}
			if !yield(event, nil) {
				return
			}
		}
		if run.Err() != nil {
			yield(llm.StreamEvent{}, context.Cause(run))
		}
	}
}

func (l *RequestLimiter) acquire(ctx context.Context, maximum int) (func(), error) {
	if maximum <= 0 {
		maximum = 4
	}
	for {
		l.mu.Lock()
		if err := ctx.Err(); err != nil {
			l.mu.Unlock()
			return nil, err
		}
		if l.active < maximum {
			l.active++
			l.mu.Unlock()
			return func() {
				l.mu.Lock()
				l.active--
				if l.available != nil {
					close(l.available)
					l.available = nil
				}
				l.mu.Unlock()
			}, nil
		}
		if l.available == nil {
			l.available = make(chan struct{})
		}
		ready := l.available
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ready:
		}
	}
}

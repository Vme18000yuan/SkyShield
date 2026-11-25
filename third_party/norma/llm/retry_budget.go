package llm

import (
	"context"
	"errors"
	"sync"
)

// ErrRequestAttemptsExhausted ends nested SDK, stream and fallback retries.
// The last transport error remains available through errors.Is/As.
var ErrRequestAttemptsExhausted = errors.New("model request retry budget exhausted")

type requestBudgetKey struct{}
type connectRetryLimitKey struct{}
type requestBudget struct {
	mu        sync.Mutex
	remaining int
	last      error
}

// WithRequestBudget shares one HTTP-attempt ceiling through nested providers.
// A caller's existing budget always wins; wrappers cannot reset it.
func WithRequestBudget(ctx context.Context, attempts int) context.Context {
	if _, ok := ctx.Value(requestBudgetKey{}).(*requestBudget); ok {
		return ctx
	}
	return context.WithValue(ctx, requestBudgetKey{}, &requestBudget{remaining: max(1, attempts)})
}

// WithConnectRetryLimit lets an outer recovery loop own retry scheduling while
// preserving the provider's normal retry settings for other callers.
func WithConnectRetryLimit(ctx context.Context, retries int) context.Context {
	return context.WithValue(ctx, connectRetryLimitKey{}, max(0, retries))
}

func takeRequestAttempt(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b, _ := ctx.Value(requestBudgetKey{}).(*requestBudget)
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.remaining == 0 {
		return errors.Join(ErrRequestAttemptsExhausted, b.last)
	}
	b.remaining--
	return nil
}

func noteRequestFailure(ctx context.Context, err error) {
	b, _ := ctx.Value(requestBudgetKey{}).(*requestBudget)
	if b == nil || err == nil {
		return
	}
	b.mu.Lock()
	b.last = err
	b.mu.Unlock()
}

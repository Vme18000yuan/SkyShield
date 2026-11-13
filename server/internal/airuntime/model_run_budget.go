package airuntime

import (
	"context"
	"errors"
	"github.com/Autumn-27/artex/agent"
)

// Zero disables the corresponding cumulative limit. Per-call deadlines,
// concurrency, context windows and administrator-configured Agent limits remain
// independent. Each execution resolves its configured scope once; children
// retain the shared run budget instead of resetting it.
type ModelRunBudgetLimits struct{ Requests, Tokens int64 }
type ModelRunBudgetResolver func(context.Context, int) (ModelRunBudgetLimits, error)

func withModelRunBudget(ctx context.Context, user int, resolve ModelRunBudgetResolver) (context.Context, error) {
	limits := ModelRunBudgetLimits{}
	if resolve != nil {
		var err error
		limits, err = resolve(ctx, user)
		if err != nil {
			return nil, err
		}
	}
	if limits.Requests < 0 || limits.Tokens < 0 {
		return nil, errors.New("invalid AI run budget")
	}
	return agent.WithRunBudget(ctx, limits.Requests, limits.Tokens), nil
}

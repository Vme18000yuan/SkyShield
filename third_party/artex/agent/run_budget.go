package agent

import (
	"context"
	"github.com/Autumn-27/norma/llm"
	"sync/atomic"
)

var ErrRunBudget = llm.ErrRunBudget

type runBudgetKey struct{}
type RunBudget struct {
	attempts, tokens                   atomic.Int64
	maxAttempts, maxTokens             int64
	settleAttempts, settleTokens       atomic.Int64
	maxSettleAttempts, maxSettleTokens int64
}

func WithRunBudget(ctx context.Context, attempts, tokens int64) context.Context {
	if _, ok := ctx.Value(runBudgetKey{}).(*RunBudget); ok {
		return ctx
	}
	budget := &RunBudget{maxAttempts: attempts, maxTokens: tokens}
	if attempts > 0 || tokens > 0 {
		budget.maxSettleAttempts = 16
		budget.maxSettleTokens = 1_000_000
	}
	return context.WithValue(ctx, runBudgetKey{}, budget)
}
func checkBudget(ctx context.Context) error {
	b, _ := ctx.Value(runBudgetKey{}).(*RunBudget)
	if b == nil {
		return nil
	}
	attempts, tokens, maxAttempts, maxTokens := &b.attempts, &b.tokens, b.maxAttempts, b.maxTokens
	settling := llm.IsBudgetSettlement(ctx)
	if settling {
		attempts, tokens, maxAttempts, maxTokens = &b.settleAttempts, &b.settleTokens, b.maxSettleAttempts, b.maxSettleTokens
	}
	if maxTokens > 0 && tokens.Load() >= maxTokens {
		return &llm.RunBudgetError{Kind: "累计 Token", Used: tokens.Load(), Limit: maxTokens, Settlement: settling}
	}
	if maxAttempts > 0 && attempts.Load() >= maxAttempts {
		return &llm.RunBudgetError{Kind: "模型请求次数", Used: attempts.Load(), Limit: maxAttempts, Settlement: settling}
	}
	return nil
}
func consumeAttempt(ctx context.Context) error {
	b, _ := ctx.Value(runBudgetKey{}).(*RunBudget)
	if b == nil {
		return nil
	}
	if err := checkBudget(ctx); err != nil {
		return err
	}
	attempts, maximum := &b.attempts, b.maxAttempts
	settling := llm.IsBudgetSettlement(ctx)
	if settling {
		attempts, maximum = &b.settleAttempts, b.maxSettleAttempts
	}
	for {
		used := attempts.Load()
		if maximum > 0 && used >= maximum {
			return &llm.RunBudgetError{Kind: "模型请求次数", Used: used, Limit: maximum, Settlement: settling}
		}
		if attempts.CompareAndSwap(used, used+1) {
			return nil
		}
	}
}
func observeBudget(ctx context.Context, usage llm.Usage) {
	if b, ok := ctx.Value(runBudgetKey{}).(*RunBudget); ok {
		// Canonical InputTokens already includes cache reads/writes. Count once.
		amount := max(0, int64(usage.InputTokens)) + max(0, int64(usage.OutputTokens))
		if llm.IsBudgetSettlement(ctx) {
			b.settleTokens.Add(amount)
		} else {
			b.tokens.Add(amount)
		}
	}
}

package agent

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

func TestZeroBudgetHasNoHiddenAttemptOrTokenLimit(t *testing.T) {
	ctx := WithRunBudget(context.Background(), 0, 0)
	for i := 0; i < 700; i++ {
		if err := consumeAttempt(ctx); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		observeBudget(ctx, llm.Usage{InputTokens: 100_000, OutputTokens: 1000, CacheReadTokens: 90_000})
	}
	for i := 0; i < 20; i++ {
		if err := consumeAttempt(llm.WithBudgetSettlement(ctx)); err != nil {
			t.Fatal(err)
		}
	}
	b := ctx.Value(runBudgetKey{}).(*RunBudget)
	if b.tokens.Load() != 70_700_000 {
		t.Fatalf("cache double counted: %d", b.tokens.Load())
	}
}

func TestBudgetReserveDoesNotResetMainBudget(t *testing.T) {
	ctx := WithRunBudget(context.Background(), 2, 100)
	if err := consumeAttempt(ctx); err != nil {
		t.Fatal(err)
	}
	observeBudget(ctx, llm.Usage{InputTokens: 90, OutputTokens: 9, CacheReadTokens: 80})
	if err := consumeAttempt(ctx); err != nil {
		t.Fatalf("cache should be a subset: %v", err)
	}
	observeBudget(ctx, llm.Usage{InputTokens: 2})
	if err := consumeAttempt(WithRunBudget(ctx, 0, 0)); !errors.Is(err, ErrRunBudget) {
		t.Fatalf("child reset shared budget: %v", err)
	}
	settle := llm.WithBudgetSettlement(ctx)
	if err := consumeAttempt(settle); err != nil {
		t.Fatalf("reserve unavailable: %v", err)
	}
	observeBudget(settle, llm.Usage{InputTokens: 1_000_000})
	var detail *llm.RunBudgetError
	err := consumeAttempt(settle)
	if !errors.As(err, &detail) || !detail.Settlement || !errors.Is(&url.Error{Op: "Post", Err: err}, ErrRunBudget) {
		t.Fatalf("reserve reason lost: %v", err)
	}
	if err := consumeAttempt(ctx); !errors.Is(err, ErrRunBudget) {
		t.Fatal("settlement reset main allowance")
	}
	fresh := WithRunBudget(context.Background(), 2, 100)
	if err := consumeAttempt(fresh); err != nil {
		t.Fatal("a new run inherited old consumption")
	}
}

func TestConcurrentAttemptAdmissionNeverExceedsLimit(t *testing.T) {
	ctx := WithRunBudget(context.Background(), 5, 0)
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Go(func() {
			if consumeAttempt(ctx) == nil {
				admitted.Add(1)
			}
		})
	}
	wg.Wait()
	if admitted.Load() != 5 {
		t.Fatalf("admitted %d", admitted.Load())
	}
}

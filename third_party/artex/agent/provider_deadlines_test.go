package agent

import (
	"context"
	"errors"
	"github.com/Autumn-27/norma/llm"
	"testing"
	"time"
)

func TestModelIdleTimeoutIgnoresEmptyKeepalives(t *testing.T) {
	provider := captureUsageProvider{stream: func(ctx context.Context, yield func(llm.StreamEvent, error) bool) {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				yield(llm.StreamEvent{}, ctx.Err())
				return
			case <-ticker.C:
				if !yield(llm.StreamEvent{Type: llm.SEMessageStart}, nil) {
					return
				}
			}
		}
	}}
	p := boundedProvider{Provider: provider, limiter: &RequestLimiter{}, first: 20 * time.Millisecond, idle: 20 * time.Millisecond, total: time.Second, concurrency: 1}
	start := time.Now()
	var failure error
	for _, err := range p.Stream(t.Context(), llm.CompletionRequest{}) {
		if err != nil {
			failure = err
		}
	}
	if !errors.Is(failure, ErrModelIdle) || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("idle stream not cancelled: %v %s", failure, time.Since(start))
	}
	if p.limiter.active != 0 {
		t.Fatal("model slot leaked")
	}
}

func TestModelConcurrencyWaitIsCancellable(t *testing.T) {
	limiter := &RequestLimiter{}
	release, err := limiter.acquire(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if _, err = limiter.acquire(ctx, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("overbooked model: %v", err)
	}
	release()
	release, err = limiter.acquire(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

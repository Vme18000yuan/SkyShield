package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSharedRequestBudgetBoundsNestedRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(503) }))
	defer srv.Close()
	cfg := Config{HTTPClient: srv.Client(), MaxRetries: 10, RetryInterval: time.Millisecond}
	ctx := WithRequestBudget(context.Background(), 2)
	ctx = WithRequestBudget(ctx, 100) // nested recovery must not reset it
	_, err := doStream(ctx, cfg, srv.URL, []byte(`{}`), func(*http.Request) {}, "test")
	if !errors.Is(err, ErrRequestAttemptsExhausted) || !strings.Contains(err.Error(), "503") || calls.Load() != 2 {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}
	_, _ = doStream(ctx, cfg, srv.URL, nil, func(*http.Request) {}, "fallback")
	if calls.Load() != 2 {
		t.Fatal("fallback escaped the shared request budget")
	}
}

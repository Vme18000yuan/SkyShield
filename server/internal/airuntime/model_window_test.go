package airuntime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/agent"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
)

func TestDefaultModelWindowAcrossExecutionPaths(t *testing.T) {
	plain := &fixtureTaskProvider{}
	if got := ModelOptions(plain).ContextWindow; got != 256_000 {
		t.Fatalf("plain provider window = %d", got)
	}
	wrapped := WithModelOptions(plain, RuntimeModelOptions{MaxTokens: 1000})
	if got := ModelOptions(wrapped); got.ContextWindow != 256_000 || got.MaxTokens != 1000 {
		t.Fatalf("unset wrapped window or other options changed: %+v", got)
	}
	role := roleProvider{ctx: context.Background(), resolve: func(context.Context, int, string) (llm.Provider, string, bool, error) {
		return nil, "", false, errors.New("not configured")
	}}
	if got := role.RuntimeModelOptions().ContextWindow; got != 256_000 {
		t.Fatalf("role fallback window = %d", got)
	}
	router := TaskModelRouter{Context: context.Background(), Select: func(context.Context) (TaskModelSelection, error) {
		return TaskModelSelection{}, errors.New("not configured")
	}}
	if got := router.RuntimeModelOptions().ContextWindow; got != 256_000 {
		t.Fatalf("task router fallback window = %d", got)
	}
	for _, tc := range []struct {
		configured int
		want       int
		allow      bool
	}{
		{0, 256_000, true},
		{32, 32_000, false},
		{64, 64_000, true},
		{800, 800_000, true},
	} {
		provider := ConfigureProvider(&fixtureTaskProvider{}, agent.Config{ContextWindowK: tc.configured}, artexdb.LLMRetryPolicy{}, false)
		if got := ModelOptions(provider).ContextWindow; got != tc.want {
			t.Fatalf("configured %dK: window = %d, want %d", tc.configured, got, tc.want)
		}
		request := llm.CompletionRequest{Messages: []llm.Message{llm.UserText(strings.Repeat("x", 120_000))}, MaxTokens: 1000}
		_, _, _, err := provider.Complete(context.Background(), request)
		if (err == nil) != tc.allow {
			t.Fatalf("configured %dK: allow=%v, error=%v", tc.configured, tc.allow, err)
		}
	}
}

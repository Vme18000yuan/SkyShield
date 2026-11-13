package airuntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/norma/llm"
	"github.com/google/uuid"
)

func TestDefaultModelRunBudgetExceedsAllFormerTaskCaps(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 5000, "completion_tokens": 5, "prompt_tokens_details": map[string]any{"cached_tokens": 4500}}})
	}))
	defer server.Close()
	provider, err := (agent.Config{Format: llm.FormatOpenAI, BaseURL: server.URL + "/v1", APIKey: "fixture", Model: "fixture"}).NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	root, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	ctx, err := withModelRunBudget(root, 7, nil)
	if err != nil {
		t.Fatal(err)
	}
	var total int
	for i := 0; i < 1100; i++ {
		_, _, usage, e := provider.Complete(ctx, llm.CompletionRequest{Messages: []llm.Message{llm.UserText("fixture")}})
		if e != nil {
			t.Fatalf("hidden task cap at request %d, tokens %d: %v", i, total, e)
		}
		total += usage.InputTokens + usage.OutputTokens
	}
	if requests.Load() != 1100 || total <= 5_000_000 {
		t.Fatalf("old thresholds not crossed: requests=%d tokens=%d", requests.Load(), total)
	}
}

func TestTaskAndMainChatEntryPointsResolveConfiguredBudget(t *testing.T) {
	provider, err := (agent.Config{Format: llm.FormatOpenAI, BaseURL: "http://unused.invalid/v1", APIKey: "fixture", Model: "fixture"}).NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	runner := &TaskRunner{connector: &TaskGraphConnector{}, root: t.TempDir()}
	marker := errors.New("budget resolver fixture")
	calls := 0
	runner.SetTaskBudgetResolver(func(_ context.Context, user int) (ModelRunBudgetLimits, error) {
		calls++
		if user != 7 {
			t.Errorf("wrong budget owner: %d", user)
		}
		return ModelRunBudgetLimits{}, marker
	})
	task := TaskExecution{ID: 1, ExplorationID: 2, UserID: 7, TenantID: "00000000-0000-0000-0000-000000000001", RunID: uuid.NewString()}
	if _, err = runner.RunTask(context.Background(), task, provider, "fixture"); !errors.Is(err, marker) {
		t.Fatalf("task bypassed runtime configuration: %v", err)
	}
	task.ExecutionMode = "main_chat"
	if _, err = runner.ChatIndependent(context.Background(), task, provider, "fixture", "fixture"); !errors.Is(err, marker) {
		t.Fatalf("task chat bypassed runtime configuration: %v", err)
	}
	if calls != 2 {
		t.Fatalf("resolver calls=%d", calls)
	}
}

func TestExplicitModelRunBudgetIsStillEnforcedAndCannotResetInChild(t *testing.T) {
	resolver := func(context.Context, int) (ModelRunBudgetLimits, error) {
		return ModelRunBudgetLimits{Requests: 1}, nil
	}
	ctx, err := withModelRunBudget(context.Background(), 7, resolver)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer server.Close()
	p, err := (agent.Config{Format: llm.FormatOpenAI, BaseURL: server.URL, APIKey: "fixture", Model: "fixture"}).NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	request := llm.CompletionRequest{Messages: []llm.Message{llm.UserText("fixture")}}
	if _, _, _, err = p.Complete(ctx, request); err != nil {
		t.Fatal(err)
	}
	child, err := withModelRunBudget(ctx, 7, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = p.Complete(child, request); !errors.Is(err, agent.ErrRunBudget) {
		t.Fatalf("explicit shared limit reset: %v", err)
	}
}

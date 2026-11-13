package airuntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
)

func TestChatDefaultsPassOldTwoMillionTokenBoundary(t *testing.T) {
	var calls, executions atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		message := map[string]any{"role": "assistant", "content": "budget-free done"}
		reason := "stop"
		if call <= 45 {
			reason = "tool_calls"
			message["content"] = ""
			message["tool_calls"] = []any{map[string]any{"id": fmt.Sprintf("probe-%d", call), "type": "function", "function": map[string]any{"name": "probe", "arguments": "{}"}}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": reason}}, "usage": map[string]any{"prompt_tokens": 50000, "completion_tokens": 100, "prompt_tokens_details": map[string]any{"cached_tokens": 45000}}})
	}))
	defer upstream.Close()
	provider, err := (agent.Config{Format: llm.FormatOpenAI, BaseURL: upstream.URL + "/v1", APIKey: "fixture", Model: "fixture"}).NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	probe := tool.Build(tool.Spec{Name: "probe", Schema: map[string]any{"type": "object", "properties": map[string]any{}}, Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
		executions.Add(1)
		return tool.Text("probe finished"), nil
	}})
	runner, err := NewChatRunner(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var terminal string
	var total int
	text, err := runner.Run(ctx, ChatRun{UserID: 7, TenantID: "00000000-0000-0000-0000-000000000001", SessionID: uuid.NewString(), ExecutionID: uuid.NewString(), AgentKey: "mainagent", Message: "fixture", Provider: provider, Model: "fixture", NonStreaming: true, ContextWindow: 1_000_000, Tools: []tool.CoreTool{probe}}, func(event Activity) {
		if event.TerminalReason != "" {
			terminal = event.TerminalReason
		}
		if event.InputTokens != nil && event.OutputTokens != nil {
			total = *event.InputTokens + *event.OutputTokens
		}
	})
	if err != nil || terminal != "completed" || text != "budget-free done" || total <= 2_000_000 || executions.Load() != 45 || calls.Load() != 46 {
		t.Fatalf("default chat still capped: terminal=%s err=%v tokens=%d tools=%d calls=%d", terminal, err, total, executions.Load(), calls.Load())
	}
	for _, key := range []string{"assistant", "mainagent", "pentest"} {
		if DefaultAgentConfig(key).MaxTurns != 0 {
			t.Fatalf("default %s tool rounds capped", key)
		}
	}
}

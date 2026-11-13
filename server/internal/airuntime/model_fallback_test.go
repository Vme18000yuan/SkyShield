package airuntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
)

type eofFallbackProvider struct {
	streamCalls   atomic.Int32
	completeCalls atomic.Int32
	committed     string
	malformed     bool
	completeErr   error
}

func (p *eofFallbackProvider) Stream(_ context.Context, _ llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		call := p.streamCalls.Add(1)
		if call == 1 {
			if !yield(llm.StreamEvent{Type: llm.SEMessageStart, Usage: llm.Usage{InputTokens: 5}}, nil) {
				return
			}
			if p.committed == "text" && !yield(llm.StreamEvent{Type: llm.SETextDelta, Text: "already visible"}, nil) {
				return
			}
			if p.committed == "tool" && !yield(llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "existing", ToolName: "probe"}, nil) {
				return
			}
			yield(llm.StreamEvent{}, fmt.Errorf("model POST: %w", io.EOF))
			return
		}
		for _, event := range []llm.StreamEvent{{Type: llm.SEMessageStart}, {Type: llm.SETextDelta, Text: "done"},
			{Type: llm.SEMessageDelta, StopReason: "end_turn", Usage: llm.Usage{InputTokens: 3, OutputTokens: 1}}, {Type: llm.SEMessageStop}} {
			if !yield(event, nil) {
				return
			}
		}
	}
}

func (p *eofFallbackProvider) Complete(_ context.Context, _ llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	p.completeCalls.Add(1)
	if p.completeErr != nil {
		return llm.Message{}, "", llm.Usage{}, p.completeErr
	}
	input := json.RawMessage(`{"value":"ok"}`)
	if p.malformed {
		input = json.RawMessage(`{"value":`)
	}
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
		{Type: llm.BlockThinking, Thinking: "check", Signature: "sig"},
		{Type: llm.BlockToolUse, ID: "fallback-call", Name: "probe", Input: input},
	}}, "tool_use", llm.Usage{InputTokens: 12, OutputTokens: 3, CacheReadTokens: 8}, nil
}

func TestEOFFallbackPreservesAgentToolCallAndUsage(t *testing.T) {
	provider := &eofFallbackProvider{}
	configured := ConfigureProvider(provider, agent.Config{Format: llm.FormatOpenAI, Retry: agent.RetryConfig{StreamAttempts: 1, StreamInterval: time.Millisecond}}, artexdb.LLMRetryPolicy{}, false)
	acc := llm.NewAccumulator()
	for event, err := range configured.Stream(context.Background(), llm.CompletionRequest{}) {
		if err != nil {
			t.Fatal(err)
		}
		acc.Add(event)
	}
	message := acc.Message()
	uses := message.ToolUses()
	if provider.streamCalls.Load() != 1 || provider.completeCalls.Load() != 1 || len(uses) != 1 || uses[0].ID != "fallback-call" || string(uses[0].Input) != `{"value":"ok"}` || acc.StopReason != "tool_use" || acc.Usage.InputTokens != 12 || acc.Usage.CacheReadTokens != 8 {
		t.Fatalf("fallback altered the response: provider=%+v message=%+v usage=%+v", provider, message, acc.Usage)
	}
}

func TestEOFFallbackNeverReplaysVisibleOutputOrDisabledRetries(t *testing.T) {
	for _, tc := range []struct {
		name, committed string
		format          llm.Format
		attempts        int
	}{
		{"text already visible", "text", llm.FormatOpenAI, 1},
		{"tool already started", "tool", llm.FormatOpenAI, 1},
		{"retries disabled", "", llm.FormatOpenAI, -1},
		{"other protocol", "", llm.FormatAnthropic, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := &eofFallbackProvider{committed: tc.committed}
			configured := ConfigureProvider(base, agent.Config{Format: tc.format, Retry: agent.RetryConfig{StreamAttempts: tc.attempts, StreamInterval: time.Millisecond}}, artexdb.LLMRetryPolicy{}, false)
			var gotErr error
			for _, err := range configured.Stream(context.Background(), llm.CompletionRequest{}) {
				if err != nil {
					gotErr = err
				}
			}
			if !errors.Is(gotErr, io.EOF) || base.completeCalls.Load() != 0 || base.streamCalls.Load() != 1 {
				t.Fatalf("replayed an unsafe call: error=%v streams=%d complete=%d", gotErr, base.streamCalls.Load(), base.completeCalls.Load())
			}
		})
	}
}

func TestEOFFallbackRejectsMalformedToolBeforeAnyEvent(t *testing.T) {
	base := &eofFallbackProvider{malformed: true}
	configured := ConfigureProvider(base, agent.Config{Format: llm.FormatOpenAI, Retry: agent.RetryConfig{StreamAttempts: 1, StreamInterval: time.Millisecond}}, artexdb.LLMRetryPolicy{}, false)
	acc := llm.NewAccumulator()
	for event, err := range configured.Stream(context.Background(), llm.CompletionRequest{}) {
		if err != nil {
			t.Fatal(err)
		}
		acc.Add(event)
	}
	message := acc.Message()
	if base.completeCalls.Load() != 1 || base.streamCalls.Load() != 2 || len(message.ToolUses()) != 0 || message.Text() != "done" {
		t.Fatalf("malformed tool leaked or safe retry failed: %+v", message)
	}
}

func TestEOFOtherProtocolUsesOnlyConfiguredStreamRetry(t *testing.T) {
	base := &eofFallbackProvider{}
	configured := ConfigureProvider(base, agent.Config{Format: llm.FormatAnthropic, Retry: agent.RetryConfig{StreamAttempts: 1, StreamInterval: time.Millisecond}}, artexdb.LLMRetryPolicy{}, false)
	for _, err := range configured.Stream(context.Background(), llm.CompletionRequest{}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if base.completeCalls.Load() != 0 || base.streamCalls.Load() != 2 {
		t.Fatalf("unexpected protocol fallback: streams=%d complete=%d", base.streamCalls.Load(), base.completeCalls.Load())
	}
}

func TestEOFFallbackExecutesRecoveredToolOnlyOnce(t *testing.T) {
	base := &eofFallbackProvider{}
	provider := ConfigureProvider(base, agent.Config{Format: llm.FormatOpenAI, Stream: true, Retry: agent.RetryConfig{StreamAttempts: 1, StreamInterval: time.Millisecond}}, artexdb.LLMRetryPolicy{}, false)
	var calls atomic.Int32
	probe := tool.Build(tool.Spec{Name: "probe", Schema: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}},
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(_ context.Context, _ json.RawMessage, _ *tool.ToolContext) (tool.Result, error) {
			calls.Add(1)
			return tool.Text("observed"), nil
		},
	})
	runner, err := NewChatRunner(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = runner.Run(context.Background(), ChatRun{TenantID: "00000000-0000-0000-0000-000000000001", UserID: 7,
		SessionID: uuid.NewString(), ExecutionID: uuid.NewString(), AgentKey: "info_gatherer", Message: "run fixture", Model: "fixture",
		Provider: provider, Tools: []tool.CoreTool{probe}, MaxTurns: 4, MaxDuration: time.Minute}, nil)
	if err != nil || calls.Load() != 1 || base.completeCalls.Load() != 1 || base.streamCalls.Load() != 2 {
		t.Fatalf("unexpected fallback execution: err=%v tools=%d complete=%d streams=%d", err, calls.Load(), base.completeCalls.Load(), base.streamCalls.Load())
	}
}

func TestEOFFallbackWithOpenAIHTTPAndUsageRecording(t *testing.T) {
	var streaming, nonStreaming atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("invalid request: %v", err)
			return
		}
		if body.Stream && streaming.Add(1) == 1 {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			_ = connection.Close() // EOF before an HTTP response, matching the report.
			return
		}
		if !body.Stream {
			nonStreaming.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":null,"tool_calls":[{"id":"http-call","type":"function","function":{"name":"probe","arguments":"{\"value\":\"ok\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":12,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":8}}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":1}}\n\n" + "data: [DONE]\n\n"))
	}))
	defer server.Close()
	config := agent.Config{Format: llm.FormatOpenAI, BaseURL: server.URL, APIKey: "fixture", Model: "fixture", Stream: true,
		Retry: agent.RetryConfig{ConnectAttempts: -1, StreamAttempts: 1, StreamInterval: time.Millisecond}}
	base, err := config.NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	sink := &usageSinkFixture{}
	provider := BindLLMUsageUser(ConfigureProvider(MeterLLMProvider(base, sink, "profile", "fixture", "fixture"), config, artexdb.LLMRetryPolicy{}, false), 7)
	var calls atomic.Int32
	probe := tool.Build(tool.Spec{Name: "probe", Schema: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}},
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(_ context.Context, _ json.RawMessage, _ *tool.ToolContext) (tool.Result, error) {
			calls.Add(1)
			return tool.Text("observed"), nil
		},
	})
	runner, err := NewChatRunner(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = runner.Run(context.Background(), ChatRun{TenantID: "00000000-0000-0000-0000-000000000001", UserID: 7,
		SessionID: uuid.NewString(), ExecutionID: uuid.NewString(), AgentKey: "info_gatherer", Message: "run fixture", Model: "fixture",
		Provider: provider, Tools: []tool.CoreTool{probe}, MaxTurns: 4, MaxDuration: time.Minute}, nil)
	if err != nil || calls.Load() != 1 || streaming.Load() != 2 || nonStreaming.Load() != 1 {
		t.Fatalf("HTTP fallback: err=%v tool=%d stream=%d complete=%d", err, calls.Load(), streaming.Load(), nonStreaming.Load())
	}
	if len(sink.records) != 3 || sink.records[0].Status != "error" || sink.records[1].Status != "ok" || sink.records[1].Usage.InputTokens != 12 || sink.records[2].Status != "ok" {
		t.Fatalf("model usage not recorded for both attempts: %+v", sink.records)
	}
}

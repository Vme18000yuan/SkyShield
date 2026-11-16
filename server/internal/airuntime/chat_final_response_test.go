package airuntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
)

func TestChatFinalResponseRecoversReasoningOnlyGatewayResponse(t *testing.T) {
	for _, nonStreaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "stream", true: "complete"}[nonStreaming], func(t *testing.T) {
			var calls, executions atomic.Int32
			summary := "已核对本轮证据 fixture-evidence，其他检查尚未验证。"
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Stream   bool              `json:"stream"`
					Tools    []json.RawMessage `json:"tools"`
					Messages []struct {
						Role, Content string
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				index := calls.Add(1)
				message := map[string]any{"role": "assistant"}
				stop := "stop"
				switch index {
				case 1, 4:
					if len(body.Tools) == 0 {
						t.Error("normal chat lost its tools after final-response recovery")
					}
					stop = "tool_calls"
					message["tool_calls"] = []map[string]any{{"index": 0, "id": fmt.Sprintf("probe-%d", index), "type": "function", "function": map[string]string{"name": "probe", "arguments": "{}"}}}
				case 2:
					message["reasoning_content"] = "private fixture reasoning without visible answer"
				case 3:
					if len(body.Tools) != 0 {
						t.Error("summary request advertised executable tools")
					}
					wire, _ := json.Marshal(body.Messages)
					if !strings.Contains(string(wire), "fixture-evidence") || !strings.Contains(body.Messages[len(body.Messages)-1].Content, "补写最终答复") {
						t.Error("summary request lost the existing execution context")
					}
					message["content"] = summary
				case 5:
					message["content"] = "后续对话已完成。"
				default:
					t.Errorf("unexpected extra model request %d", index)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				choice := map[string]any{"index": 0, "finish_reason": stop}
				usage := map[string]any{"prompt_tokens": 11, "completion_tokens": 2}
				if body.Stream {
					w.Header().Set("Content-Type", "text/event-stream")
					choice["delta"] = message
					data, _ := json.Marshal(map[string]any{"choices": []any{choice}, "usage": usage})
					fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
				} else {
					w.Header().Set("Content-Type", "application/json")
					choice["message"] = message
					_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{choice}, "usage": usage})
				}
			}))
			defer gateway.Close()
			provider, err := llm.NewProvider(llm.Config{Format: llm.FormatOpenAI, BaseURL: gateway.URL, APIKey: "fixture", Model: "fixture", HTTPClient: gateway.Client(), MaxRetries: -1, EmptyResponseRetries: -1})
			if err != nil {
				t.Fatal(err)
			}
			runner, err := NewChatRunner(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			probe := tool.Build(tool.Spec{Name: "probe", Schema: map[string]any{"type": "object"}, Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
				executions.Add(1)
				return tool.Text("fixture-evidence"), nil
			}})
			run := ChatRun{TenantID: "00000000-0000-0000-0000-000000000001", UserID: 7, SessionID: "a93788b7-b58d-4090-bc07-f025b5fd5097", AgentKey: "assistant", Model: "fixture", Provider: provider, Message: "test fixture", NonStreaming: nonStreaming, Tools: []tool.CoreTool{probe}}
			var activities []Activity
			reply, err := runner.Run(context.Background(), run, func(a Activity) { activities = append(activities, a) })
			if err != nil || reply != summary || executions.Load() != 1 || calls.Load() != 3 {
				t.Fatalf("reply=%q err=%v executions=%d calls=%d", reply, err, executions.Load(), calls.Load())
			}
			results := 0
			for _, a := range activities {
				if a.Kind != "result" {
					continue
				}
				results++
				if a.TerminalReason != "completed" || a.IsError || a.Detail != summary || *a.InputTokens != 33 || *a.OutputTokens != 6 {
					t.Fatalf("final result or cumulative usage was not saved: %+v", a)
				}
			}
			if results != 1 {
				t.Fatalf("terminal result count=%d", results)
			}
			// A later user turn resumes the same transcript with its normal tools;
			// finalization must never permanently disable this conversation's tools.
			run.Message = "follow up"
			if reply, err = runner.Run(context.Background(), run, nil); err != nil || reply != "后续对话已完成。" || executions.Load() != 2 || calls.Load() != 5 {
				t.Fatalf("follow-up: reply=%q err=%v executions=%d calls=%d", reply, err, executions.Load(), calls.Load())
			}
		})
	}
}

func TestChatFinalResponseDoesNotExposeReasoningAsSuccessfulSummary(t *testing.T) {
	provider := captureReasoningOnlyProvider{}
	runner, err := NewChatRunner(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run := ChatRun{TenantID: "00000000-0000-0000-0000-000000000001", UserID: 7, SessionID: "a93788b7-b58d-4090-bc07-f025b5fd5097", AgentKey: "assistant", Model: "fixture", Provider: provider, Message: "test fixture"}
	var final Activity
	_, err = runner.Run(context.Background(), run, func(a Activity) {
		if a.Kind == "result" {
			final = a
		}
	})
	if !errors.Is(err, harness.ErrEmptyFinalResponse) || final.TerminalReason != "empty_response" || !final.IsError || strings.Contains(final.Detail, "private fixture reasoning") || !strings.Contains(final.Detail, "自动补写也未成功") {
		t.Fatalf("missing summary was reported as success or used reasoning as prose: err=%v final=%+v", err, final)
	}
}

type captureReasoningOnlyProvider struct{}

func (captureReasoningOnlyProvider) Stream(context.Context, llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		for _, event := range []llm.StreamEvent{{Type: llm.SEThinkingDelta, Text: "private fixture reasoning"}, {Type: llm.SEMessageDelta, StopReason: "end_turn"}, {Type: llm.SEMessageStop}} {
			if !yield(event, nil) {
				return
			}
		}
	}
}

func (captureReasoningOnlyProvider) Complete(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockThinking, Thinking: "private fixture reasoning"}}}, "end_turn", llm.Usage{}, nil
}

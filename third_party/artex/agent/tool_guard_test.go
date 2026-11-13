package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
)

type toolFailureSequence struct {
	calls        int
	first, input string
	sawError     bool
}

func (p *toolFailureSequence) Stream(_ context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		p.calls++
		name, id, input := p.first, "failed-call", p.input
		if p.calls == 2 {
			for _, message := range request.Messages {
				for _, block := range message.Content {
					if block.Type == llm.BlockToolResult && block.ToolUseID == "failed-call" {
						p.sawError = block.IsError
						if strings.Contains(blocksText(block.Content), "private-panic-token") {
							yield(llm.StreamEvent{}, errors.New("panic details leaked"))
							return
						}
					}
				}
			}
			if !p.sawError {
				yield(llm.StreamEvent{}, errors.New("tool error was not returned to model"))
				return
			}
			name, id, input = "healthy", "healthy-call", `{}`
		}
		events := []llm.StreamEvent{{Type: llm.SEMessageStart}}
		if p.calls <= 2 {
			events = append(events, llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: id, ToolName: name}, llm.StreamEvent{Type: llm.SEToolInputJSON, Text: input}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"})
		} else {
			events = append(events, llm.StreamEvent{Type: llm.SETextDelta, Text: "done"}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"})
		}
		events = append(events, llm.StreamEvent{Type: llm.SEMessageStop})
		for _, event := range events {
			if !yield(event, nil) {
				return
			}
		}
	}
}
func (p *toolFailureSequence) Complete(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	return llm.Message{}, "", llm.Usage{}, errors.New("unexpected complete")
}

func TestRecoverableToolFailuresReturnToModelWithoutReplay(t *testing.T) {
	for _, kind := range []string{"error result", "Go error", "tool deadline", "panic", "denied", "permission panic", "read-only panic", "scheduling panic", "invalid arguments", "unknown tool", "raw input"} {
		t.Run(kind, func(t *testing.T) {
			var failedCalls, healthyCalls atomic.Int32
			probe := tool.Spec{Name: "probe", Schema: map[string]any{"type": "object"}, Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
				if kind == "permission panic" {
					panic("private-panic-token")
				}
				if kind == "denied" {
					return permission.Denied("fixture denied")
				}
				return permission.Allowed()
			}, Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
				failedCalls.Add(1)
				switch kind {
				case "panic":
					panic("private-panic-token")
				case "Go error":
					return tool.Result{}, errors.New("fixture tool failed")
				case "tool deadline":
					return tool.Result{}, fmt.Errorf("tool timeout: %w", context.DeadlineExceeded)
				default:
					return tool.Errorf("fixture failure"), nil
				}
			}}
			model := &toolFailureSequence{first: "probe", input: `{}`}
			wantCalls := int32(1)
			switch kind {
			case "denied", "permission panic":
				wantCalls = 0
			case "read-only panic":
				probe.ReadOnly = func(json.RawMessage) bool { panic("fixture") }
				wantCalls = 0
			case "scheduling panic":
				probe.Concurrent = func(json.RawMessage) bool { panic("fixture") }
				wantCalls = 0
			case "invalid arguments":
				probe.Schema["required"] = []any{"value"}
				probe.Schema["properties"] = map[string]any{"value": map[string]any{"type": "string"}}
				wantCalls = 0
			case "unknown tool":
				model.first = "not-installed"
				wantCalls = 0
			case "raw input":
				probe.RawInput = true
				model.input = `{"value":`
			}
			healthy := tool.Build(tool.Spec{Name: "healthy", Schema: map[string]any{"type": "object"}, Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
				healthyCalls.Add(1)
				return tool.Text("healthy observation"), nil
			}})
			var activities []db.Activity
			text, reason, err := captureRun(context.Background(), agentcore.Options{Provider: model, Tools: []tool.CoreTool{tool.Build(probe), healthy}, PermissionMode: permission.ModeBypass, MaxTurns: 4}, "local fixture", func(a db.Activity) { activities = append(activities, a) })
			if err != nil || reason != harness.ReasonCompleted || text != "done" || !model.sawError || model.calls != 3 || failedCalls.Load() != wantCalls || healthyCalls.Load() != 1 {
				t.Fatalf("error aborted or replayed tool: err=%v reason=%s turns=%d failed=%d healthy=%d", err, reason, model.calls, failedCalls.Load(), healthyCalls.Load())
			}
			found := false
			for _, a := range activities {
				if a.Kind == "tool_result" && a.ToolUseID == "failed-call" {
					found = a.IsError
				}
			}
			if !found {
				t.Fatal("tool failure was hidden from the execution record")
			}
		})
	}
}

func TestToolGuardHonorsRunCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := &toolFailureSequence{first: "probe", input: `{}`}
	probe := tool.Build(tool.Spec{Name: "probe", Schema: map[string]any{"type": "object"}, Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
		cancel()
		return tool.Result{}, context.Canceled
	}})
	_, _, err := captureRun(ctx, agentcore.Options{Provider: model, Tools: []tool.CoreTool{probe}, PermissionMode: permission.ModeBypass, MaxTurns: 4}, "cancel fixture", nil)
	if !errors.Is(err, context.Canceled) || model.calls != 1 {
		t.Fatalf("continued after user cancellation: calls=%d err=%v", model.calls, err)
	}
}

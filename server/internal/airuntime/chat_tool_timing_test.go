package airuntime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
)

type timingFixtureProvider struct {
	recordingProvider
	turns int
}

func (p *timingFixtureProvider) Complete(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	p.turns++
	if p.turns == 1 {
		return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ID: "fixture-id", Name: "ReadInventoryFixture", Input: json.RawMessage("{}")}}}, "tool_use", llm.Usage{}, nil
	}
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockText, Text: "Fixture complete"}}}, "end_turn", llm.Usage{}, nil
}
func TestIndependentChatToolTimingPropagation(t *testing.T) {
	for _, observe := range []bool{false, true} {
		runner, err := NewChatRunner(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		var inside string
		fixture := tool.Build(tool.Spec{Name: "ReadInventoryFixture", Description: "Return a fixture", Schema: map[string]any{"type": "object"},
			Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
				return permission.Allowed()
			},
			Run: func(ctx context.Context, _ json.RawMessage, _ *tool.ToolContext) (tool.Result, error) {
				inside = harness.ToolOccurrence(ctx)
				return tool.Text("2 stored records"), nil
			}})
		provider := &timingFixtureProvider{}
		run := ChatRun{ObserveToolTiming: observe, TenantID: "00000000-0000-0000-0000-000000000001", UserID: 3, SessionID: uuid.NewString(), ExecutionID: uuid.NewString(), AgentKey: "assistant", Message: "Summarize local fixture", Model: "fixture", Provider: provider, Tools: []tool.CoreTool{fixture}, NonStreaming: true}
		var use, result *harness.ToolTiming
		_, err = runner.Run(context.Background(), run, func(e Activity) {
			if e.Kind == "tool_use" {
				use = e.ToolTiming
			}
			if e.Kind == "tool_result" {
				result = e.ToolTiming
			}
		})
		if err != nil || provider.turns != 2 {
			t.Fatalf("fixture run turns=%d error=%v", provider.turns, err)
		}
		if observe {
			if use == nil || result == nil || use.Occurrence != inside || result.Occurrence != inside || result.StartedAt.IsZero() || result.FinishedAt.Before(result.StartedAt) {
				t.Fatal("tool boundary lost in chat capture")
			}
		} else if use != nil || result != nil || inside != "" {
			t.Fatal("unobserved session changed")
		}
	}
}

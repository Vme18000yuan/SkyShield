package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
)

func TestGoalDecompositionDefaultAndConfiguredTurnLimits(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configured *int
		wantCalls  int
	}{
		{name: "default permits more than eight turns", wantCalls: 11},
		{name: "explicit zero is unlimited", configured: new(int), wantCalls: 11},
		{name: "explicit limit is honored", configured: intPointer(2), wantCalls: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			calls := 0
			provider := captureUsageProvider{stream: func(_ context.Context, yield func(llm.StreamEvent, error) bool) {
				calls++
				events := []llm.StreamEvent{{Type: llm.SETextDelta, Text: "done"}, {Type: llm.SEMessageDelta, StopReason: "end_turn"}}
				if calls <= 10 {
					events = []llm.StreamEvent{
						{Type: llm.SEToolUseStart, ToolID: fmt.Sprintf("probe-%d", calls), ToolName: "turn_probe"},
						{Type: llm.SEToolInputJSON, Text: `{}`},
						{Type: llm.SEMessageDelta, StopReason: "tool_use"},
					}
				}
				for _, event := range events {
					if !yield(event, nil) {
						return
					}
				}
			}}
			probe := tool.Build(tool.Spec{Name: "turn_probe", Schema: map[string]any{"type": "object"},
				Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
					return tool.Text("ok"), nil
				}})
			policy := HostPolicy{TransformTools: func(context.Context, string, []tool.CoreTool) []tool.CoreTool {
				return []tool.CoreTool{probe}
			}}
			if tc.configured != nil {
				policy.MaxTurns = func(string, int) int { return *tc.configured }
			}
			ctx = WithHostPolicy(ctx, policy)
			DecomposeGoalsWithProvider(ctx, provider, t.TempDir(), "fixture", "", nil, nil, 0, false, 0, nil)
			if ctx.Err() != nil {
				t.Fatal(ctx.Err())
			}
			if calls != tc.wantCalls {
				t.Fatalf("model calls = %d, want %d", calls, tc.wantCalls)
			}
		})
	}
}

func intPointer(value int) *int { return &value }

package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
)

func TestCapturePersistsActualToolProgress(t *testing.T) {
	turn := 0
	provider := captureUsageProvider{stream: func(_ context.Context, yield func(llm.StreamEvent, error) bool) {
		turn++
		events := []llm.StreamEvent{{Type: llm.SETextDelta, Text: "完成"}, {Type: llm.SEMessageDelta, StopReason: "end_turn"}}
		if turn == 1 {
			events = []llm.StreamEvent{{Type: llm.SEToolUseStart, ToolID: "phase-1", ToolName: "phase_probe"},
				{Type: llm.SEToolInputJSON, Text: `{}`}, {Type: llm.SEMessageDelta, StopReason: "tool_use"}}
		}
		for _, event := range events {
			if !yield(event, nil) {
				return
			}
		}
	}}
	probe := tool.Build(tool.Spec{Name: "phase_probe", Schema: map[string]any{"type": "object"},
		Run: func(_ context.Context, _ json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
			tc.Emit(tool.ProgressInfo{Message: "正在执行：DNS 解析。"})
			return tool.Text("实际结果"), nil
		},
	})
	var activities []db.Activity
	_, _, err := captureRun(context.Background(), agentcore.Options{Provider: provider, Tools: []tool.CoreTool{probe},
		PermissionMode: permission.ModeBypass, WorkingDir: t.TempDir(), MaxTurns: 3}, "run", func(a db.Activity) {
		activities = append(activities, a)
	})
	if err != nil {
		t.Fatal(err)
	}
	use, progress, result := -1, -1, -1
	for i, activity := range activities {
		switch activity.Kind {
		case "tool_use":
			use = i
		case "progress":
			progress = i
			if activity.Summary != "正在执行：DNS 解析。" {
				t.Fatalf("unexpected progress: %+v", activity)
			}
		case "tool_result":
			result = i
		}
	}
	if use < 0 || progress <= use || result <= progress {
		t.Fatalf("progress did not appear between real tool events: %+v", activities)
	}
}

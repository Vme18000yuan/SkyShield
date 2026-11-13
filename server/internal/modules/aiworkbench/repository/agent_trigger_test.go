package repository

import (
	artexdb "github.com/Autumn-27/artex/db"
	"testing"
)

func TestToolTriggerSkipsFailedFindingButKeepsOtherErrors(t *testing.T) {
	trigger := artexdb.AgentTrigger{OnToolCall: true, ToolNames: []string{"report_finding", "custom_tool"}, ToolCallMessage: "run"}
	if _, ok := triggerMessage(trigger, "tool_call", map[string]any{"tool": "REPORT_FINDING", "is_error": true}); ok {
		t.Fatal("failed finding started a report")
	}
	if message, ok := triggerMessage(trigger, "tool_call", map[string]any{"tool": "REPORT_FINDING", "is_error": false}); !ok || message != "run" {
		t.Fatal("successful finding did not trigger")
	}
	if _, ok := triggerMessage(trigger, "tool_call", map[string]any{"tool": "CUSTOM_TOOL", "is_error": true}); !ok {
		t.Fatal("custom error trigger was suppressed")
	}
}

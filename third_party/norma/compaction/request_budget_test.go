package compaction

import (
	"context"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

func TestNewToolOutputCountsBeforeNextRequest(t *testing.T) {
	c := New(Config{ContextWindow: 40000, KeepRecent: 2}, func(context.Context, []llm.Message) (string, error) {
		return "Previous work completed; keep the latest tool result.", nil
	})
	history := []llm.Message{bigMsg(llm.RoleUser, 18000), bigMsg(llm.RoleAssistant, 18000), bigMsg(llm.RoleUser, 18000)}
	history = c.Pre(t.Context(), history, 0)
	history = append(history, toolPair("probe", "scan", strings.Repeat("large result ", 6000))...)
	out := c.Pre(t.Context(), history, 14000)
	if llm.LastBoundaryIndex(out) < 0 {
		t.Fatal("new tool output was ignored because the preceding request was small")
	}
	api := llm.MessagesForAPI(out)
	if len(api[len(api)-1].Content) == 0 || api[len(api)-1].Content[0].ToolUseID != "scan" {
		t.Fatal("latest tool result lost")
	}
}

func TestCompactionBoundaryKeepsToolExchangeTogether(t *testing.T) {
	c := New(Config{ContextWindow: 40000, KeepRecent: 1}, func(context.Context, []llm.Message) (string, error) { return "Saved earlier findings.", nil })
	history := []llm.Message{bigMsg(llm.RoleUser, 16000), bigMsg(llm.RoleAssistant, 16000)}
	history = append(history, toolPair("run_info_gather_phase", "ports", "6 completed; 5 timed out; run-id: saved")...)
	out, ok := c.autoCompact(t.Context(), history, 30000, "test")
	if !ok {
		t.Fatal("did not compact")
	}
	api := llm.MessagesForAPI(out)
	if len(api) != 3 || len(api[1].ToolUses()) != 1 || api[2].Content[0].ToolUseID != "ports" {
		t.Fatalf("split tool exchange: %+v", api)
	}
}

func TestEmptyOrLargerSummaryNeverReplacesHistory(t *testing.T) {
	for _, summary := range []string{"", "  ", strings.Repeat("expansion", 10000)} {
		c := New(Config{KeepRecent: 1}, func(context.Context, []llm.Message) (string, error) { return summary, nil })
		history := []llm.Message{llm.UserText("scope and user requirements"), bigMsg(llm.RoleAssistant, 12), llm.UserText("continue")}
		out, ok := c.autoCompact(t.Context(), history, 50, "test")
		if ok || llm.LastBoundaryIndex(out) >= 0 {
			t.Fatal("invalid summary hid original instructions")
		}
	}
}

func TestStaticRequestBudgetIsIncluded(t *testing.T) {
	c := New(Config{ContextWindow: 40000}, nil)
	c.SetRequestBudget(17000, 12000)
	if got := c.count([]llm.Message{llm.UserText("short")}, 0); got < 17000 {
		t.Fatalf("system/tool budget omitted: %d", got)
	}
	if c.effectiveWindow() != 28000 {
		t.Fatal("output reservation ignored")
	}
}

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net/url"
	"testing"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
)

func TestHostBudgetEntersSettlementWithoutReplayingCompletedTools(t *testing.T) {
	for _, syncMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "stream", true: "complete"}[syncMode], func(t *testing.T) {
			calls, probes, saves := 0, 0, 0
			probe := tool.Build(tool.Spec{Name: "probe", Schema: map[string]any{"type": "object"}, Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
				probes++
				return tool.Text("saved probe output"), nil
			}})
			save := tool.Build(tool.Spec{Name: "save", Schema: map[string]any{"type": "object"}, Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
				saves++
				return tool.Text("checkpoint saved"), nil
			}})
			call := func(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
				i := calls
				calls++
				return func(yield func(llm.StreamEvent, error) bool) {
					if i == 1 {
						yield(llm.StreamEvent{}, &url.Error{Op: "Post", Err: llm.ErrRunBudget})
						return
					}
					if i >= 2 && !llm.IsBudgetSettlement(ctx) {
						t.Error("missing settlement reserve context")
					}
					var events []llm.StreamEvent
					switch i {
					case 0:
						events = toolTurn("probe-1", "probe", "{}")
					case 2:
						events = toolTurn("save-1", "save", "{}")
					default:
						events = textTurn("已保存结果；剩余工作等待继续")
					}
					for _, event := range events {
						if !yield(event, nil) {
							return
						}
					}
				}
			}
			deps := QueryDeps{CallModel: call, CallModelSync: func(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
				acc := llm.NewAccumulator()
				for event, err := range call(ctx, req) {
					if err != nil {
						return llm.Message{}, "", llm.Usage{}, err
					}
					acc.Add(event)
				}
				return acc.Message(), acc.StopReason, acc.Usage, nil
			}}
			term := drain(t, QueryInput{Messages: []llm.Message{llm.UserText("go")}, NonStreaming: syncMode, Tools: tool.NewRegistry(probe, save), PermissionMode: permission.ModeBypass, Settlement: &Settlement{Prompt: "Save and summarize", MaxTurns: 3, DisabledTools: []string{"probe"}}}, deps)
			if term.Reason != ReasonRunBudget || term.Err != nil || term.Text == "" || probes != 1 || saves != 1 || calls != 4 {
				t.Fatalf("bad settlement: reason=%s err=%v probes=%d saves=%d calls=%d", term.Reason, term.Err, probes, saves, calls)
			}
		})
	}
}

func TestExhaustedSettlementReserveRemainsBudgetTermination(t *testing.T) {
	calls := 0
	term := drain(t, QueryInput{Messages: []llm.Message{llm.UserText("go")}, Settlement: &Settlement{Prompt: "Summarize"}}, QueryDeps{CallModel: func(context.Context, llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
		return func(yield func(llm.StreamEvent, error) bool) { calls++; yield(llm.StreamEvent{}, llm.ErrRunBudget) }
	}})
	if term.Reason != ReasonRunBudget || !errors.Is(term.Err, llm.ErrRunBudget) || calls != 2 {
		t.Fatalf("reserve retried forever or mislabeled: %#v %d", term, calls)
	}
}

type boundedHistoryForBudgetTest struct{}

func (boundedHistoryForBudgetTest) Pre(_ context.Context, messages []llm.Message, _ int) []llm.Message {
	if len(messages) > 4 {
		return messages[len(messages)-4:]
	}
	return messages
}
func (boundedHistoryForBudgetTest) Reactive(_ context.Context, messages []llm.Message) ([]llm.Message, bool) {
	return messages, false
}
func (boundedHistoryForBudgetTest) IsOverflow(error) bool { return false }

func TestUnlimitedSuccessfulToolRoundsDoNotHitRecoveryBackstop(t *testing.T) {
	calls, executions := 0, 0
	probe := tool.Build(tool.Spec{Name: "probe", Schema: map[string]any{"type": "object"}, Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
		executions++
		return tool.Text("ok"), nil
	}})
	term := drain(t, QueryInput{Messages: []llm.Message{llm.UserText("go")}, NonStreaming: true, Tools: tool.NewRegistry(probe), PermissionMode: permission.ModeBypass, Compactor: boundedHistoryForBudgetTest{}, FinalResponsePrompt: "Summarize recorded results."}, QueryDeps{CallModelSync: func(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
		calls++
		if calls > 10002 {
			return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockText, Text: "done"}}}, "end_turn", llm.Usage{}, nil
		}
		return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ID: "probe", Name: "probe", Input: json.RawMessage(`{}`)}}}, "tool_use", llm.Usage{}, nil
	}})
	if term.Reason != ReasonCompleted || executions != 10002 {
		t.Fatalf("hidden tool-round cap: %s, executions=%d", term.Reason, executions)
	}
}

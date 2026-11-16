package harness

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
)

func finalResponseDeps(call func(context.Context, llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error]) QueryDeps {
	return QueryDeps{CallModel: call, CallModelSync: func(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
		acc := llm.NewAccumulator()
		for event, err := range call(ctx, req) {
			if err != nil {
				return llm.Message{}, "", llm.Usage{}, err
			}
			acc.Add(event)
		}
		return acc.Message(), acc.StopReason, acc.Usage, nil
	}}
}

func TestFinalResponseRecoversEmptyStopWithoutReplayingTools(t *testing.T) {
	for _, syncMode := range []bool{false, true} {
		for _, empty := range []string{"", " \n\t", "thinking-only"} {
			t.Run(map[bool]string{false: "stream/", true: "complete/"}[syncMode]+empty, func(t *testing.T) {
				var executions atomic.Int32
				probe := tool.Build(tool.Spec{Name: "probe", Schema: map[string]any{"type": "object"}, Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
					executions.Add(1)
					return tool.Text("recorded-fixture-evidence"), nil
				}})
				registry := tool.NewRegistry(probe)
				var requests []llm.CompletionRequest
				call := func(_ context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
					i := len(requests)
					requests = append(requests, req)
					return func(yield func(llm.StreamEvent, error) bool) {
						var events []llm.StreamEvent
						switch i {
						case 0:
							events = toolTurn("probe-once", "probe", "{}")
						case 1:
							events = textTurn(empty)
							if empty == "thinking-only" {
								events[0] = llm.StreamEvent{Type: llm.SEThinkingDelta, Text: "private fixture reasoning"}
							}
						case 2:
							events = textTurn("Verified the recorded fixture; remaining checks are unverified.")
						default:
							t.Fatal("unbounded final-response retry")
						}
						for _, event := range events {
							if event.Type == llm.SEMessageDelta {
								event.Usage = llm.Usage{InputTokens: 11, OutputTokens: 2, CacheReadTokens: 5}
							}
							if !yield(event, nil) {
								return
							}
						}
					}
				}
				term := drain(t, QueryInput{Messages: []llm.Message{llm.UserText("check fixture")}, Tools: registry, PermissionMode: permission.ModeBypass, NonStreaming: syncMode, FinalResponsePrompt: "Summarize recorded evidence without tools."}, finalResponseDeps(call))
				if term.Reason != ReasonCompleted || term.Err != nil || !strings.HasPrefix(term.Text, "Verified") || term.Turns != 3 || executions.Load() != 1 || len(requests) != 3 {
					t.Fatalf("terminal=%+v executions=%d requests=%d", term, executions.Load(), len(requests))
				}
				if len(requests[2].Tools) != 0 || len(requests[0].Tools) != 1 {
					t.Fatal("final response retained tools or normal execution lost tools")
				}
				wire, _ := json.Marshal(requests[2].Messages)
				if !strings.Contains(string(wire), "recorded-fixture-evidence") || !strings.Contains(string(wire), "Summarize recorded evidence") {
					t.Fatal("final response did not receive the existing tool results")
				}
				if term.Usage != (llm.Usage{InputTokens: 33, OutputTokens: 6, CacheReadTokens: 15}) {
					t.Fatalf("recovery usage was lost: %+v", term.Usage)
				}
				if _, ok := registry.Get("probe"); !ok {
					t.Fatal("recovery mutated the caller's registry")
				}
			})
		}
	}
}

func TestFinalResponseCannotExecuteUnadvertisedToolOrLoopOnEmptyOutput(t *testing.T) {
	for _, syncMode := range []bool{false, true} {
		for _, triesTool := range []bool{false, true} {
			t.Run(map[bool]string{false: "stream/", true: "complete/"}[syncMode]+map[bool]string{false: "empty", true: "tool"}[triesTool], func(t *testing.T) {
				var executions atomic.Int32
				probe := tool.Build(tool.Spec{Name: "probe", Schema: map[string]any{"type": "object"}, Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
					executions.Add(1)
					return tool.Text("must not execute"), nil
				}})
				calls := 0
				call := func(context.Context, llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
					calls++
					if calls > 2 {
						t.Fatal("empty response retried indefinitely")
					}
					events := textTurn(" ")
					if calls == 2 && triesTool {
						events = toolTurn("forbidden-probe", "probe", "{}")
					}
					return func(yield func(llm.StreamEvent, error) bool) {
						for _, event := range events {
							if !yield(event, nil) {
								return
							}
						}
					}
				}
				term := drain(t, QueryInput{Tools: tool.NewRegistry(probe), PermissionMode: permission.ModeBypass, NonStreaming: syncMode, FinalResponsePrompt: "Reply without tools."}, finalResponseDeps(call))
				if term.Reason != ReasonEmptyResponse || !errors.Is(term.Err, ErrEmptyFinalResponse) || calls != 2 || executions.Load() != 0 {
					t.Fatalf("terminal=%+v calls=%d executions=%d", term, calls, executions.Load())
				}
				if triesTool {
					last := term.Messages[len(term.Messages)-1]
					if len(last.Content) != 1 || last.Content[0].Type != llm.BlockToolResult || !last.Content[0].IsError {
						t.Fatalf("rejected call was not paired in the transcript: %+v", last)
					}
				}
			})
		}
	}
}

func TestFinalResponseDoesNotRetryValidTextOrUnconfiguredToolOnlyCompletion(t *testing.T) {
	for _, tc := range []struct{ text, prompt string }{{"done", "summarize"}, {"", ""}} {
		m := &scriptedModel{turns: [][]llm.StreamEvent{textTurn(tc.text)}}
		term := drain(t, QueryInput{FinalResponsePrompt: tc.prompt}, QueryDeps{CallModel: m.call})
		if term.Reason != ReasonCompleted || term.Text != tc.text || m.calls != 1 {
			t.Fatalf("normal completion changed: %+v calls=%d", term, m.calls)
		}
	}
}

func TestFinalResponseRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	deps := QueryDeps{CallModel: func(context.Context, llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
		calls++
		return func(yield func(llm.StreamEvent, error) bool) {
			if calls == 2 {
				cancel()
				yield(llm.StreamEvent{}, ctx.Err())
				return
			}
			for _, event := range textTurn("") {
				if !yield(event, nil) {
					return
				}
			}
		}
	}}
	var term *Terminal
	for event, err := range Query(ctx, QueryInput{FinalResponsePrompt: "summarize"}, deps) {
		if err != nil {
			t.Fatal(err)
		}
		if event.Terminal != nil {
			term = event.Terminal
		}
	}
	if term == nil || term.Reason != ReasonAbortedStreaming || !errors.Is(term.Err, context.Canceled) || calls != 2 {
		t.Fatalf("cancelled summary continued: %+v calls=%d", term, calls)
	}
}

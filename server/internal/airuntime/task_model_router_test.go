package airuntime

import (
	"context"
	"errors"
	"iter"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

type fixtureTaskProvider struct {
	events []llm.StreamEvent
	err    error
	calls  atomic.Int32
}

func (p *fixtureTaskProvider) Stream(_ context.Context, _ llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		p.calls.Add(1)
		for _, event := range p.events {
			if !yield(event, nil) {
				return
			}
		}
		if p.err != nil {
			yield(llm.StreamEvent{}, p.err)
		}
	}
}
func (p *fixtureTaskProvider) Complete(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	p.calls.Add(1)
	return llm.Message{}, "", llm.Usage{}, p.err
}

func TestTaskModelRouterOnlyReplaysBeforeOutput(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "before output", true: "after output"}[committed], func(t *testing.T) {
			first := &fixtureTaskProvider{err: errors.New("insufficient_quota: quota exhausted")}
			if committed {
				first.events = []llm.StreamEvent{{Type: llm.SETextDelta, Text: "partial"}}
			} else {
				first.events = []llm.StreamEvent{{Type: llm.SEMessageStart}}
			}
			second := &fixtureTaskProvider{events: []llm.StreamEvent{{Type: llm.SETextDelta, Text: "fallback"}, {Type: llm.SEMessageStop}}}
			index := 0
			router := &TaskModelRouter{Context: context.Background(), Select: func(context.Context) (TaskModelSelection, error) {
				if index == 0 {
					return TaskModelSelection{Provider: first, ProfileID: "first", Revision: 1}, nil
				}
				return TaskModelSelection{Provider: second, ProfileID: "second", Revision: 2}, nil
			}, Exhaust: func(_ context.Context, selection TaskModelSelection, cause error) (TaskModelTransition, error) {
				if selection.ProfileID != "first" || !strings.Contains(cause.Error(), "quota") {
					t.Fatal("unexpected transition")
				}
				index = 1
				return TaskModelTransition{Advanced: true}, nil
			}}
			var text string
			var failure error
			for event, err := range router.Stream(context.Background(), llm.CompletionRequest{}) {
				if err != nil {
					failure = err
					break
				}
				if event.Type == llm.SETextDelta {
					text += event.Text
				}
			}
			if committed {
				if text != "partial" || failure == nil || second.calls.Load() != 0 {
					t.Fatalf("committed stream replayed: %q %v calls=%d", text, failure, second.calls.Load())
				}
			} else if text != "fallback" || failure != nil || second.calls.Load() != 1 {
				t.Fatalf("safe failover failed: %q %v calls=%d", text, failure, second.calls.Load())
			}
		})
	}
}

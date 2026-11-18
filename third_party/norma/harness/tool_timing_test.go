package harness

import (
	"context"
	"testing"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
)

func TestToolTimingIsOptInAndMeasuresRealCalls(t *testing.T) {
	for _, observe := range []bool{false, true} {
		p := &concurrencyProbe{}
		// The provider repeats t1 and t2 in a second model response.
		model := &scriptedModel{turns: [][]llm.StreamEvent{twoToolTurn("fixture"), twoToolTurn("fixture"), textTurn("done")}}
		in := QueryInput{ObserveToolTiming: observe, Messages: []llm.Message{llm.UserText("test")}, PermissionMode: permission.ModeBypass, Tools: tool.NewRegistry(probeTool("fixture", true, p))}
		used := map[string]ToolTiming{}
		var results []*ToolTiming
		for ev, err := range Query(context.Background(), in, QueryDeps{CallModel: model.call}) {
			if err != nil {
				t.Fatal(err)
			}
			if !observe {
				if ev.ToolTiming != nil || ev.Kind == KindToolStarted {
					t.Fatal("timing escaped into an ordinary session")
				}
				continue
			}
			switch ev.Kind {
			case KindToolUse:
				if ev.ToolTiming == nil || ev.ToolTiming.Occurrence == "" || !ev.ToolTiming.StartedAt.IsZero() {
					t.Fatal("invalid tool-use observation")
				}
				used[ev.ToolTiming.Occurrence] = *ev.ToolTiming
			case KindToolResult:
				timing := ev.ToolTiming
				if timing == nil || timing.Round != used[timing.Occurrence].Round || timing.StartedAt.IsZero() || !timing.FinishedAt.After(timing.StartedAt) {
					t.Fatal("unpaired result or missing tool boundary")
				}
				results = append(results, timing)
			}
		}
		if model.calls != 3 || p.maxSeen != 2 {
			t.Fatalf("observation changed scheduling: %d/%d", model.calls, p.maxSeen)
		}
		if observe {
			if len(used) != 4 || len(results) != 4 || results[0].Batch == results[2].Batch || results[0].Round != 1 || results[2].Round != 2 {
				t.Fatal("reused provider IDs or model rounds collapsed")
			}
			if !results[0].StartedAt.Before(results[1].FinishedAt) || !results[1].StartedAt.Before(results[0].FinishedAt) {
				t.Fatal("overlapping in-process tools were not measured")
			}
		}
	}
}

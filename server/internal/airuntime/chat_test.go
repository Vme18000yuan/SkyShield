package airuntime

import (
	"context"
	"encoding/json"
	"iter"
	"strings"
	"sync"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

type recordingProvider struct {
	mu       sync.Mutex
	requests []llm.CompletionRequest
}

func (p *recordingProvider) Stream(_ context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	return func(yield func(llm.StreamEvent, error) bool) {
		for _, event := range []llm.StreamEvent{
			{Type: llm.SEMessageStart},
			{Type: llm.SETextDelta, Text: "已收到"},
			{Type: llm.SEMessageDelta, StopReason: "end_turn", Usage: llm.Usage{InputTokens: 7, OutputTokens: 2}},
			{Type: llm.SEMessageStop},
		} {
			if !yield(event, nil) {
				return
			}
		}
	}
}

func (p *recordingProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for ev, err := range p.Stream(ctx, req) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(ev)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func TestChatRunnerResumesTranscriptAndSeparatesUsers(t *testing.T) {
	runner, err := NewChatRunner(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	provider := &recordingProvider{}
	run := ChatRun{
		TenantID: "00000000-0000-0000-0000-000000000001", UserID: 3,
		SessionID: "6c76a079-5df6-4e16-a420-65fa583b28cb", AgentKey: "assistant",
		Model: "fixture", Provider: provider, Message: "first-message", MaxTurns: 4,
	}
	var events []Activity
	if reply, err := runner.Run(context.Background(), run, func(a Activity) { events = append(events, a) }); err != nil || reply != "已收到" {
		t.Fatalf("first run: reply %q, err %v", reply, err)
	}
	if len(events) == 0 || events[len(events)-1].Kind != "result" {
		t.Fatalf("missing ARTEX capture event: %+v", events)
	}
	run.Message = "second-message"
	if _, err := runner.Run(context.Background(), run, nil); err != nil {
		t.Fatal(err)
	}
	run.UserID = 4
	run.Message = "other-user"
	if _, err := runner.Run(context.Background(), run, nil); err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("provider requests=%d", len(provider.requests))
	}
	second, _ := json.Marshal(provider.requests[1].Messages)
	third, _ := json.Marshal(provider.requests[2].Messages)
	if !strings.Contains(string(second), "first-message") || !strings.Contains(string(second), "second-message") {
		t.Fatalf("conversation was not resumed: %s", second)
	}
	if strings.Contains(string(third), "first-message") || strings.Contains(string(third), "second-message") {
		t.Fatalf("conversation leaked across users: %s", third)
	}
	for _, schema := range provider.requests[0].Tools {
		switch schema.Name {
		case "Bash", "Read", "Write", "Edit", "WebFetch":
			t.Fatalf("unprovided host tool exposed: %s", schema.Name)
		}
	}
}

func TestChatRunnerRejectsUnsafeSessionPath(t *testing.T) {
	runner, err := NewChatRunner(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = runner.Run(context.Background(), ChatRun{
		TenantID: "00000000-0000-0000-0000-000000000001", UserID: 1,
		SessionID: "../../outside", AgentKey: "assistant", Provider: &recordingProvider{}, Message: "test",
	}, nil)
	if err == nil {
		t.Fatal("unsafe session ID was accepted")
	}
}

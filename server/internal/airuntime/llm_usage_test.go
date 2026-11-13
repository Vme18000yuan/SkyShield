package airuntime

import (
	"context"
	"errors"
	"iter"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/transcript"
)

type usageSinkFixture struct {
	mu      sync.Mutex
	records []LLMUsageRecord
}
type recordingSinkFixture struct {
	usageSinkFixture
	enabled  bool
	payloads []LLMRecordingPayload
}

func (s *recordingSinkFixture) StartLLMRecording(_ context.Context, item LLMUsageRecord) (bool, LLMRecordingLease, error) {
	return s.enabled, LLMRecordingLease{TaskID: item.TaskID, Generation: 2}, nil
}
func (s *recordingSinkFixture) RecordLLMRecording(_ context.Context, _ LLMUsageRecord, lease LLMRecordingLease, payload LLMRecordingPayload) error {
	if lease.Generation != 2 {
		return errors.New("wrong recording lease")
	}
	s.payloads = append(s.payloads, payload)
	return nil
}

type retryUsageFixture struct{ calls atomic.Int32 }

func (p *retryUsageFixture) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return usageProviderFixture{fail: p.calls.Add(1) == 1}.Stream(ctx, req)
}
func (p *retryUsageFixture) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	return usageProviderFixture{fail: p.calls.Add(1) == 1}.Complete(ctx, req)
}

func (s *usageSinkFixture) RecordLLMUsage(_ context.Context, record LLMUsageRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, record)
	return nil
}

type usageProviderFixture struct{ fail bool }

func (p usageProviderFixture) Stream(_ context.Context, _ llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		if !yield(llm.StreamEvent{Type: llm.SEMessageStart, Usage: llm.Usage{InputTokens: 11, CacheReadTokens: 3}}, nil) {
			return
		}
		if p.fail {
			yield(llm.StreamEvent{}, errors.New("fixture model error"))
			return
		}
		yield(llm.StreamEvent{Type: llm.SEMessageDelta, Usage: llm.Usage{OutputTokens: 7}}, nil)
	}
}
func (p usageProviderFixture) Complete(_ context.Context, _ llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	if p.fail {
		return llm.Message{}, "", llm.Usage{InputTokens: 5}, errors.New("fixture model error")
	}
	return llm.Message{}, "end_turn", llm.Usage{InputTokens: 13, OutputTokens: 9}, nil
}

func TestLLMUsageTracksActualCallsAndOwnerScope(t *testing.T) {
	sink := &usageSinkFixture{}
	base := MeterLLMProvider(usageProviderFixture{}, sink, "profile", "模型 A", "fixture-model")
	var wg sync.WaitGroup
	for user := 1; user <= 2; user++ {
		wg.Add(1)
		go func(user int) {
			defer wg.Done()
			ctx := WithTaskExecution(context.Background(), TaskExecution{ID: int64(user * 10), ExplorationID: int64(user * 100), UserID: user})
			ctx = transcript.WithSessionID(ctx, "exp100-worker-i1")
			for _, err := range BindLLMUsageUser(base, user).Stream(ctx, llm.CompletionRequest{}) {
				if err != nil {
					t.Errorf("stream: %v", err)
				}
			}
		}(user)
	}
	wg.Wait()
	if len(sink.records) != 2 {
		t.Fatalf("usage calls: %+v", sink.records)
	}
	for _, item := range sink.records {
		if item.TaskID != int64(item.UserID*10) || item.ExplorationID != int64(item.UserID*100) || item.Worker != "worker" || item.Model != "fixture-model" || item.Usage.InputTokens != 11 || item.Usage.OutputTokens != 7 || item.Usage.CacheReadTokens != 3 || item.Status != "ok" {
			t.Fatalf("usage owner or tokens: %+v", item)
		}
	}
	wrongOwner := WithTaskExecution(context.Background(), TaskExecution{ID: 999, UserID: 2})
	_, _, _, _ = BindLLMUsageUser(base, 1).Complete(wrongOwner, llm.CompletionRequest{})
	if len(sink.records) != 2 {
		t.Fatal("usage accepted a task belonging to another bound user")
	}
	ctx := WithLLMUsageScope(context.Background(), LLMUsageScope{UserID: 1, ConversationID: "conversation", Worker: "auto"})
	failed := BindLLMUsageUser(MeterLLMProvider(usageProviderFixture{fail: true}, sink, "", "", "failed"), 1)
	_, _, _, _ = failed.Complete(ctx, llm.CompletionRequest{})
	last := sink.records[len(sink.records)-1]
	if last.ConversationID != "conversation" || last.Worker != "auto" || last.Status != "error" || last.Usage.InputTokens != 5 {
		t.Fatalf("failed completion usage: %+v", last)
	}
	for range BindLLMUsageUser(base, 1).Stream(ctx, llm.CompletionRequest{}) {
		break
	}
	last = sink.records[len(sink.records)-1]
	if last.Status != "interrupted" || last.Usage.InputTokens != 11 || last.Usage.OutputTokens != 0 {
		t.Fatalf("interrupted usage: %+v", last)
	}
}

func TestLLMUsageCountsEachRetriedModelAttempt(t *testing.T) {
	sink := &usageSinkFixture{}
	raw := MeterLLMProvider(&retryUsageFixture{}, sink, "", "profile", "model")
	configured := ConfigureProvider(raw, agent.Config{Retry: agent.RetryConfig{StreamAttempts: 1, StreamInterval: time.Millisecond}}, artexdb.LLMRetryPolicy{}, false)
	for _, err := range BindLLMUsageUser(configured, 1).Stream(context.Background(), llm.CompletionRequest{}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(sink.records) != 2 || sink.records[0].Status != "error" || sink.records[1].Status != "ok" || sink.records[0].Usage.InputTokens != 11 || sink.records[1].Usage.OutputTokens != 7 {
		t.Fatalf("retry usage: %+v", sink.records)
	}
}

func TestLLMRecordingIsOptIn(t *testing.T) {
	sink := &recordingSinkFixture{}
	provider := BindLLMUsageUser(MeterLLMProvider(usageProviderFixture{}, sink, "", "", "fixture"), 1)
	ctx := WithLLMUsageScope(context.Background(), LLMUsageScope{UserID: 1, TaskID: 7, Worker: "worker"})
	request := llm.CompletionRequest{Messages: []llm.Message{llm.UserText("recording-fixture")}}
	_, _, _, _ = provider.Complete(ctx, request)
	if len(sink.records) != 1 || len(sink.payloads) != 0 {
		t.Fatal("disabled recording captured model contents")
	}
	sink.enabled = true
	_, _, _, _ = provider.Complete(ctx, request)
	if len(sink.records) != 2 || len(sink.payloads) != 1 || !strings.Contains(sink.payloads[0].NormalizedRequest, "recording-fixture") || sink.payloads[0].NormalizedResponse == "" {
		t.Fatalf("enabled recording: %+v", sink.payloads)
	}
}

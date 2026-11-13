package airuntime

import (
	"context"
	"github.com/Autumn-27/norma/llm"
	"iter"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type stallThenSettleProvider struct {
	recordingProvider
	calls int
}

func (p *stallThenSettleProvider) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	p.calls++
	if p.calls > 1 {
		return p.recordingProvider.Stream(ctx, req)
	}
	return func(yield func(llm.StreamEvent, error) bool) { <-ctx.Done(); yield(llm.StreamEvent{}, ctx.Err()) }
}
func reliabilityChatRun(provider llm.Provider) ChatRun {
	return ChatRun{TenantID: "00000000-0000-0000-0000-000000000001", UserID: 7, SessionID: "11111111-1111-4111-8111-111111111111", AgentKey: "assistant", Message: "fixture", Provider: provider}
}

func TestConversationDeadlineInterruptsSilentModelAndPreservesReason(t *testing.T) {
	runner, err := NewChatRunner(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	provider := &stallThenSettleProvider{}
	run := reliabilityChatRun(provider)
	run.MaxDuration = 20 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	var reason string
	start := time.Now()
	_, err = runner.Run(ctx, run, func(a Activity) {
		if a.Kind == "result" {
			reason = a.TerminalReason
		}
	})
	if err != nil || reason != "timeout" || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("reason=%s elapsed=%s err=%v", reason, time.Since(start), err)
	}
}

func TestConversationRejectsCorruptAndUnwritableTranscripts(t *testing.T) {
	for _, failure := range []string{"corrupt", "unwritable", "missing"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			runner, err := NewChatRunner(root)
			if err != nil {
				t.Fatal(err)
			}
			provider := &recordingProvider{}
			run := reliabilityChatRun(provider)
			dir := filepath.Join(root, run.TenantID, "user-7", "transcripts")
			if failure == "corrupt" || failure == "missing" {
				if _, err = runner.Run(t.Context(), run, nil); err != nil {
					t.Fatal(err)
				}
				if failure == "missing" {
					if err = os.Remove(filepath.Join(dir, run.SessionID+".jsonl")); err != nil {
						t.Fatal(err)
					}
					run.HistoryRequired = func(context.Context) (bool, error) { return true, nil }
				} else {
					f, err := os.OpenFile(filepath.Join(dir, run.SessionID+".jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
					if err != nil {
						t.Fatal(err)
					}
					_, err = f.WriteString("{incomplete\n")
					f.Close()
					if err != nil {
						t.Fatal(err)
					}
				}
			} else {
				if err = os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(dir, []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := len(provider.requests)
			if _, err = runner.Run(t.Context(), run, nil); err == nil {
				t.Fatal("storage failure was reported as success")
			}
			if len(provider.requests) != before {
				t.Fatal("model was called without a durable conversation")
			}
		})
	}
}

func TestConversationLockWaitCanBeCancelled(t *testing.T) {
	runner, err := NewChatRunner(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	release, err := runner.lockConversation(t.Context(), "one")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if _, err := runner.lockConversation(ctx, "one"); err == nil {
		t.Fatal("cancelled lock wait was admitted")
	}
	release()
	if len(runner.locks.entries) != 0 {
		t.Fatal("cancelled waiter retained lock entry")
	}
}

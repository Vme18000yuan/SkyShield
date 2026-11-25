package airuntime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
)

func TestRecoveryDoesNotMultiplyHTTPAttemptsOrRetryWholeIntent(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	}))
	defer srv.Close()
	config := agent.Config{Format: llm.FormatOpenAI, BaseURL: srv.URL, Model: "fixture", Stream: true,
		Retry: agent.RetryConfig{ConnectAttempts: 6, ConnectInterval: time.Millisecond, StreamAttempts: 2, StreamInterval: time.Millisecond}}
	base, err := config.NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	p := ConfigureProvider(base, config, artexdb.LLMRetryPolicy{}, false)
	var failure error
	progress := 0
	ctx := withExecutionProgress(t.Context(), func(string) { progress++ })
	for _, err := range p.Stream(ctx, llm.CompletionRequest{Messages: []llm.Message{llm.UserText("fixture")}}) {
		if err != nil {
			failure = err
		}
	}
	if calls.Load() != 4 || progress != 2 || !errors.Is(failure, io.EOF) || !errors.Is(failure, ErrModelRecoveryExhausted) || retryableStreamError(failure) {
		t.Fatalf("calls=%d progress=%d failure=%v", calls.Load(), progress, failure)
	}
}

type savedResultProvider struct {
	calls               int
	recovered, sawSaved bool
}

func (p *savedResultProvider) Complete(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	return llm.Message{}, "", llm.Usage{}, io.EOF
}
func (p *savedResultProvider) Stream(_ context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		p.calls++
		if p.recovered {
			raw, _ := json.Marshal(req.Messages)
			p.sawSaved = strings.Contains(string(raw), "saved-scan-result")
			for _, event := range []llm.StreamEvent{{Type: llm.SETextDelta, Text: "Results retained; continuing analysis."}, {Type: llm.SEMessageDelta, StopReason: "end_turn"}, {Type: llm.SEMessageStop}} {
				if !yield(event, nil) {
					return
				}
			}
			return
		}
		if p.calls > 1 {
			yield(llm.StreamEvent{}, io.EOF)
			return
		}
		for _, event := range []llm.StreamEvent{{Type: llm.SEToolUseStart, ToolID: "scan-once", ToolName: "probe"}, {Type: llm.SEToolInputJSON, Text: `{}`}, {Type: llm.SEMessageDelta, StopReason: "tool_use"}, {Type: llm.SEMessageStop}} {
			if !yield(event, nil) {
				return
			}
		}
	}
}

func TestResumeAfterModelFailureReadsSavedToolResultWithoutReexecution(t *testing.T) {
	for _, noa := range []bool{false, true} {
		name := "builtin"
		if noa {
			name = "noa"
		}
		t.Run(name, func(t *testing.T) {
			base := &savedResultProvider{}
			p := ConfigureProvider(base, agent.Config{Format: llm.FormatOpenAI, Stream: true, Retry: agent.RetryConfig{StreamAttempts: 1, StreamInterval: time.Millisecond}}, artexdb.LLMRetryPolicy{}, noa)
			var executions atomic.Int32
			probe := tool.Build(tool.Spec{Name: "probe", Schema: map[string]any{"type": "object"},
				Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
					return permission.Allowed()
				},
				Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
					executions.Add(1)
					return tool.Text("saved-scan-result"), nil
				},
			})
			runner, err := NewChatRunner(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			run := ChatRun{TenantID: "00000000-0000-0000-0000-000000000001", UserID: 7, SessionID: uuid.NewString(), ExecutionID: uuid.NewString(), AgentKey: "info_gatherer", Message: "run fixture", Model: "fixture", Provider: p, Tools: []tool.CoreTool{probe}, MaxTurns: 4}
			if _, err = runner.Run(t.Context(), run, nil); err == nil || executions.Load() != 1 {
				t.Fatalf("first run err=%v tools=%d", err, executions.Load())
			}
			base.recovered = true
			run.ExecutionID, run.Message = uuid.NewString(), "Continue from saved results."
			if _, err = runner.Run(t.Context(), run, nil); err != nil || !base.sawSaved || executions.Load() != 1 {
				t.Fatalf("resume err=%v saved=%v tools=%d", err, base.sawSaved, executions.Load())
			}

		})
	}
}

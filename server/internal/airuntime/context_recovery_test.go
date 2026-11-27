package airuntime

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/agent"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
)

type longContextFixture struct {
	t                 *testing.T
	turns, maxInput   int
	resume, sawLatest bool
}

func (p *longContextFixture) Complete(_ context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	size := llm.EstimateRequestTokens(req)
	p.maxInput = max(p.maxInput, size)
	if size+req.MaxTokens > 256000 {
		return llm.Message{}, "", llm.Usage{}, fmt.Errorf("context length exceeds configured model window (256000 tokens); compact before retry")
	}
	if len(req.Tools) == 0 { // Simulate a model which fails to produce a useful automatic summary.
		return llm.Message{Role: llm.RoleAssistant}, "stop", llm.Usage{}, nil
	}
	wire, _ := json.Marshal(req.Messages)
	if !strings.Contains(string(wire), "original authorized scope") {
		p.t.Error("user scope lost")
	}
	if p.resume {
		p.sawLatest = strings.Contains(string(wire), "observation-64")
		return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock("Continued from saved results.")}}, "end_turn", llm.Usage{InputTokens: size}, nil
	}
	p.turns++
	if p.turns > 64 {
		return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock("Finished the fixture.")}}, "end_turn", llm.Usage{InputTokens: size}, nil
	}
	args, _ := json.Marshal(map[string]string{"command": fmt.Sprintf("fixture-%d", p.turns)})
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
		{Type: llm.BlockThinking, Thinking: strings.Repeat("intermediate reasoning ", 650), Signature: "signature"},
		{Type: llm.BlockToolUse, Name: "Bash", ID: fmt.Sprintf("call-%d", p.turns), Input: args},
	}}, "tool_use", llm.Usage{InputTokens: size, OutputTokens: 500}, nil
}
func (p *longContextFixture) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		m, reason, usage, err := p.Complete(ctx, req)
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}
		events, err := completionAsStream(m, reason, usage)
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}
		for _, event := range events {
			if !yield(event, nil) {
				return
			}
		}
	}
}

func Test256KLongConversationAndColdResume(t *testing.T) {
	for _, noa := range []bool{false, true} {
		t.Run(fmt.Sprintf("noa=%v", noa), func(t *testing.T) {
			root := t.TempDir()
			base := &longContextFixture{t: t}
			provider := ConfigureProvider(base, agent.Config{Format: llm.FormatOpenAI, ContextWindowK: 256, Stream: false}, artexdb.LLMRetryPolicy{}, noa)
			executions := 0
			bash := tool.Build(tool.Spec{Name: "Bash", Schema: map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}},
				Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
					return permission.Allowed()
				},
				Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
					executions++
					return tool.Text(fmt.Sprintf("observation-%d\n", executions) + strings.Repeat("evidence ", 380)), nil
				}})
			run := ChatRun{TenantID: "00000000-0000-0000-0000-000000000001", UserID: 7, SessionID: uuid.NewString(), ExecutionID: uuid.NewString(),
				AgentKey: "assistant", Message: "Keep the original authorized scope.", Model: "fixture", Provider: provider, Tools: []tool.CoreTool{bash}, NonStreaming: true}
			runner, err := NewChatRunner(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = runner.Run(t.Context(), run, nil); err != nil {
				t.Fatal(err)
			}
			if executions != 64 {
				t.Fatalf("executions=%d", executions)
			}
			archives, _ := filepath.Glob(filepath.Join(root, run.TenantID, "user-7", "work", "sessions", run.SessionID, ".context", "context-*.json"))
			if len(archives) == 0 {
				t.Fatal("long fixture did not exercise overflow checkpoints")
			}
			base.resume = true
			run.ExecutionID, run.Message = uuid.NewString(), "Continue without repeating completed operations."
			runner, err = NewChatRunner(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = runner.Run(t.Context(), run, nil); err != nil {
				t.Fatalf("cold resume: %v", err)
			}
			if !base.sawLatest || executions != 64 {
				t.Fatalf("resume lost tool result or reran tools: latest=%v executions=%d", base.sawLatest, executions)
			}
		})
	}
}

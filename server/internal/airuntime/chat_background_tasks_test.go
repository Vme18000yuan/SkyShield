package airuntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
)

type backgroundControlModel struct {
	turn          int
	task, monitor string
}

func (p *backgroundControlModel) Stream(_ context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		p.turn++
		for _, schema := range request.Tools {
			if RequiresTaskContext(schema.Name) {
				yield(llm.StreamEvent{}, errors.New("task-only tool exposed in independent conversation"))
				return
			}
		}
		var output string
		for _, message := range request.Messages {
			for _, block := range message.Content {
				if block.Type == llm.BlockToolResult && block.ToolUseID == fmt.Sprintf("bg-%d", p.turn-1) {
					if block.IsError {
						yield(llm.StreamEvent{}, fmt.Errorf("background control failed: %s", block.Content[0].Text))
						return
					}
					for _, part := range block.Content {
						output += part.Text
					}
				}
			}
		}
		name, input := "Bash", `{"command":"printf 'background fixture'; sleep 0.05; printf ' complete'","run_in_background":true}`
		findID := func() string { return regexp.MustCompile(`task_\d+`).FindString(output) }
		switch p.turn {
		case 2:
			p.task = findID()
			if p.task == "" {
				yield(llm.StreamEvent{}, errors.New("Bash did not return a background task ID"))
				return
			}
			name, input = "TaskOutput", fmt.Sprintf(`{"task_id":%q,"block":true,"timeout_ms":2000}`, p.task)
		case 3:
			if !strings.Contains(output, "background fixture complete") {
				yield(llm.StreamEvent{}, errors.New("TaskOutput did not read the Bash worker's output"))
				return
			}
			name, input = "TaskList", `{}`
		case 4:
			if !strings.Contains(output, p.task) {
				yield(llm.StreamEvent{}, errors.New("TaskList used another manager"))
				return
			}
			name, input = "Monitor", `{"command":"sleep 5","description":"local fixture"}`
		case 5:
			p.monitor = findID()
			if p.monitor == "" || p.monitor == p.task {
				yield(llm.StreamEvent{}, errors.New("Monitor did not use the shared worker manager"))
				return
			}
			name, input = "TaskStop", fmt.Sprintf(`{"task_id":%q}`, p.monitor)
		case 6:
			name, input = "TaskOutput", fmt.Sprintf(`{"task_id":%q}`, p.monitor)
		case 7:
			if !strings.Contains(strings.ToLower(output), "killed") {
				yield(llm.StreamEvent{}, errors.New("TaskStop did not stop the monitor"))
				return
			}
		}
		events := []llm.StreamEvent{{Type: llm.SEMessageStart}}
		if p.turn < 7 {
			events = append(events, llm.StreamEvent{Type: llm.SEToolUseStart, ToolName: name, ToolID: fmt.Sprintf("bg-%d", p.turn)}, llm.StreamEvent{Type: llm.SEToolInputJSON, Text: input}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"})
		} else {
			events = append(events, llm.StreamEvent{Type: llm.SETextDelta, Text: "background controls verified"}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"})
		}
		for _, event := range append(events, llm.StreamEvent{Type: llm.SEMessageStop}) {
			if !yield(event, nil) {
				return
			}
		}
	}
}
func (p *backgroundControlModel) Complete(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	return llm.Message{}, "", llm.Usage{}, errors.New("unexpected complete")
}

func TestHostedChatBackgroundToolsShareWorkerManager(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux sandbox")
	}
	executable := filepath.Join(t.TempDir(), "ai-tool-worker")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", executable, "./cmd/ai-tool-worker")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build local fixture worker: %v %s", err, output)
	}
	t.Setenv("TIANQIONG_AI_TOOL_RUNNER", executable)
	runner, err := NewChatRunner(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	model := &backgroundControlModel{}
	var unsupported []tool.CoreTool
	for _, name := range []string{"report_finding", "list_findings"} {
		unsupported = append(unsupported, tool.Build(tool.Spec{Name: name, Schema: map[string]any{"type": "object"}, Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
			t.Error("taskless graph tool executed")
			return tool.Errorf("no task context"), nil
		}}))
	}
	text, err := runner.Run(ctx, ChatRun{TenantID: "00000000-0000-0000-0000-000000000001", UserID: 7, SessionID: uuid.NewString(), ExecutionID: uuid.NewString(), AgentKey: "auto", Message: "exercise local background controls", Model: "fixture", Provider: model, LocalTools: true, Tools: unsupported, MaxTurns: 10}, nil)
	if err != nil || text != "background controls verified" || model.turn != 7 {
		t.Fatalf("hosted background controls: err=%v turns=%d", err, model.turn)
	}
}

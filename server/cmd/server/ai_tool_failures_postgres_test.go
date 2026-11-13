package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"iter"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type failurePersistenceModel struct {
	turns        atomic.Int32
	missingError atomic.Bool
}

func (m *failurePersistenceModel) Stream(_ context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		turn := m.turns.Add(1)
		if turn == 2 {
			found := false
			for _, message := range request.Messages {
				for _, block := range message.Content {
					if block.Type == llm.BlockToolResult && block.ToolUseID == "fixture-failed" {
						found = block.IsError
					}
				}
			}
			if !found {
				m.missingError.Store(true)
				yield(llm.StreamEvent{}, errors.New("fixture did not receive tool failure"))
				return
			}
		}
		events := []llm.StreamEvent{{Type: llm.SEMessageStart}}
		if turn <= 3 {
			name, id := "failing", "fixture-failed"
			if turn == 2 {
				name, id = "binary", "fixture-binary"
			}
			if turn == 3 {
				name, id = "healthy", "fixture-healthy"
			}
			events = append(events, llm.StreamEvent{Type: llm.SEToolUseStart, ToolName: name, ToolID: id}, llm.StreamEvent{Type: llm.SEToolInputJSON, Text: `{}`}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"})
		} else {
			events = append(events, llm.StreamEvent{Type: llm.SETextDelta, Text: "failure matrix complete"}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"})
		}
		events = append(events, llm.StreamEvent{Type: llm.SEMessageStop})
		for _, event := range events {
			if !yield(event, nil) {
				return
			}
		}
	}
}
func (m *failurePersistenceModel) Complete(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	return llm.Message{}, "", llm.Usage{}, errors.New("unexpected complete")
}

func verifyRecoverableToolFailuresPostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var owner int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []string{"returned error", "panic", "permission panic"} {
		t.Run("persist tool failure/"+failure, func(t *testing.T) {
			runner, err := airuntime.NewChatRunner(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			model := &failurePersistenceModel{}
			var failedCalls, binaryCalls, healthyCalls atomic.Int32
			failing := tool.Build(tool.Spec{Name: "failing", Schema: map[string]any{"type": "object"}, Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
				if failure == "permission panic" {
					panic("fixture permission callback")
				}
				return permission.Allowed()
			}, Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
				failedCalls.Add(1)
				if failure == "panic" {
					panic("fixture callback")
				}
				return tool.Result{}, errors.New("fixture returned error")
			}})
			binary := tool.Build(tool.Spec{Name: "binary", Schema: map[string]any{"type": "object"}, Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
				binaryCalls.Add(1)
				return tool.Text("binary\x00output\xff"), nil
			}})
			healthy := tool.Build(tool.Spec{Name: "healthy", Schema: map[string]any{"type": "object"}, Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
				healthyCalls.Add(1)
				return tool.Text("healthy output"), nil
			}})
			facade := aiapp.NewConversationFacade(airepo.NewConversationStore(orm), runner, func(context.Context, int) (llm.Provider, string, bool, error) { return model, "fixture", true, nil }, func(int) []tool.CoreTool { return []tool.CoreTool{failing, binary, healthy} })
			runCtx, cancel := context.WithCancel(ctx)
			facade.Start(runCtx)
			defer func() { cancel(); <-facade.Done() }()
			conversation, err := facade.Create(ctx, owner, "recoverable failure fixture")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = facade.Send(ctx, owner, conversation.ID, "exercise local fixture"); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				page, err := facade.Activities(ctx, owner, conversation.ID, 0)
				if err != nil {
					t.Fatal(err)
				}
				if !page.Running {
					if page.RunStatus != "completed" || model.missingError.Load() || model.turns.Load() != 4 || healthyCalls.Load() != 1 || binaryCalls.Load() != 1 {
						t.Fatalf("single-tool failure aborted the run: status=%s turns=%d", page.RunStatus, model.turns.Load())
					}
					want := int32(1)
					if failure == "permission panic" {
						want = 0
					}
					if failedCalls.Load() != want {
						t.Fatal("failed tool was replayed or permission check bypassed")
					}
					errorRecorded, binaryRecorded, finalRecorded := false, false, false
					for _, activity := range page.Items {
						if activity.Kind == "tool_result" && activity.ToolUseID == "fixture-failed" {
							errorRecorded = activity.IsError
						}
						if activity.Kind == "tool_result" && activity.ToolUseID == "fixture-binary" {
							binaryRecorded = strings.Contains(activity.Detail, `\x00`)
						}
						if activity.Kind == "result" && activity.Detail == "failure matrix complete" {
							finalRecorded = !activity.IsError
						}
					}
					if !errorRecorded || !binaryRecorded || !finalRecorded {
						t.Fatal("failure, binary output or final answer was lost")
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("fixture run failed to finish")
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

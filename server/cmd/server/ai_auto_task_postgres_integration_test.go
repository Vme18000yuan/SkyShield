package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	aiinfra "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/infrastructure"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type autoSpawnProvider struct {
	mu       sync.Mutex
	calls    int
	db       *sql.DB
	sourceID int64
}

func (p *autoSpawnProvider) Stream(ctx context.Context, _ llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	p.mu.Lock()
	index := p.calls
	p.calls++
	p.mu.Unlock()
	return func(yield func(llm.StreamEvent, error) bool) {
		events := []llm.StreamEvent{{Type: llm.SEMessageStart}}
		if index == 0 {
			events = append(events, llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "auto-create-1", ToolName: "spawn_task"},
				llm.StreamEvent{Type: llm.SEToolInputJSON, Text: fmt.Sprintf(`{"description":"Auto 创建测试","goal":"测试授权范围","source_task_ids":["%d"],"timeout_seconds":0,"plan_heartbeat_seconds":600,"seed_first_intent":true}`, p.sourceID)}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"})
		} else if index == 1 {
			var id int64
			if err := p.db.QueryRowContext(ctx, `SELECT id FROM ai_task WHERE name='Auto 创建测试' ORDER BY id DESC LIMIT 1`).Scan(&id); err != nil {
				yield(llm.StreamEvent{}, err)
				return
			}
			events = append(events, llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "auto-pause-1", ToolName: "pause_task"},
				llm.StreamEvent{Type: llm.SEToolInputJSON, Text: fmt.Sprintf(`{"task_id":"%d"}`, id)}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"})
		} else {
			events = append(events, llm.StreamEvent{Type: llm.SETextDelta, Text: "任务已创建"}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"})
		}
		events = append(events, llm.StreamEvent{Type: llm.SEMessageStop})
		for _, event := range events {
			if !yield(event, nil) {
				return
			}
		}
	}
}

func (p *autoSpawnProvider) Complete(ctx context.Context, request llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, request) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func verifyAIAutoTaskPostgresContract(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var owner, other int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conversations := airepo.NewConversationStore(orm)
	conversationID, runID, instanceID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := conversations.CreateAgentConversation(ctx, owner, conversationID, "auto guard", "auto"); err != nil {
		t.Fatal(err)
	}
	if err := conversations.BeginRun(ctx, owner, conversationID, runID, instanceID, "create"); err != nil {
		t.Fatal(err)
	}
	provider := &autoSpawnProvider{db: db}
	resolve := func(context.Context, int) (llm.Provider, string, bool, error) { return provider, "fixture", true, nil }
	tasks := aiapp.NewTaskFacade(airepo.NewTaskStore(orm), resolve, nil)
	source, err := tasks.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "Auto 来源任务", Goal: "提供已有结论"})
	if err != nil {
		t.Fatal(err)
	}
	provider.sourceID = source.ID
	input := aiapp.CreateTaskInput{UserID: owner, Name: "并发去重", Goal: "验证去重", SourceTaskIDs: []int64{source.ID}, ConversationRunID: runID, ToolUseID: "same-tool-call", ToolInputHash: strings.Repeat("a", 64)}
	results := make(chan int64, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			item, err := tasks.Create(ctx, input)
			if err != nil {
				errs <- err
				return
			}
			results <- item.ID
		}()
	}
	var ids []int64
	for i := 0; i < 2; i++ {
		select {
		case err := <-errs:
			t.Fatal(err)
		case id := <-results:
			ids = append(ids, id)
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent task creation timed out")
		}
	}
	if ids[0] != ids[1] {
		t.Fatalf("replayed tool call created multiple tasks: %v", ids)
	}
	if _, err := db.ExecContext(ctx, `UPDATE ai_task SET deleted_at=clock_timestamp() WHERE id=$1`, source.ID); err != nil {
		t.Fatal(err)
	}
	if replay, err := tasks.Create(ctx, input); err != nil || replay.ID != ids[0] {
		t.Fatalf("task creation replay depended on deleted source: %+v %v", replay, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE ai_task SET deleted_at=NULL WHERE id=$1`, source.ID); err != nil {
		t.Fatal(err)
	}
	conflict := input
	conflict.ToolInputHash = strings.Repeat("b", 64)
	if _, err := tasks.Create(ctx, conflict); !errors.Is(err, aiapp.ErrInvalidInput) {
		t.Fatalf("different input reused tool ID: %v", err)
	}
	foreign := input
	foreign.UserID, foreign.ToolUseID = other, "other-user"
	if _, err := tasks.Create(ctx, foreign); !errors.Is(err, aiapp.ErrRunLeaseLost) {
		t.Fatalf("other user used run lease: %v", err)
	}
	mutation := aiapp.AutoTaskMutation{RunID: runID, ToolUseID: "pause-call", InputHash: strings.Repeat("c", 64)}
	paused, err := tasks.PauseFromAuto(ctx, owner, ids[0], mutation)
	if err != nil || paused.Status != "paused" {
		t.Fatalf("auto pause: %+v %v", paused, err)
	}
	if replay, err := tasks.PauseFromAuto(ctx, owner, ids[0], mutation); err != nil || replay.Status != "paused" {
		t.Fatalf("auto pause replay: %+v %v", replay, err)
	}
	if _, err := tasks.Control(ctx, owner, ids[0], "resume"); err != nil {
		t.Fatal(err)
	}
	if replay, err := tasks.PauseFromAuto(ctx, owner, ids[0], mutation); err != nil || replay.Status != "created" || !replay.Queued {
		t.Fatalf("old pause replay affected a resumed task: %+v %v", replay, err)
	}
	changed := mutation
	changed.InputHash = strings.Repeat("d", 64)
	if _, err := tasks.PauseFromAuto(ctx, owner, ids[0], changed); !errors.Is(err, aiapp.ErrInvalidInput) {
		t.Fatalf("conflicting pause replay: %v", err)
	}
	if _, err := conversations.StopRun(ctx, owner, conversationID); err != nil {
		t.Fatal(err)
	}
	input.ToolUseID = "after-stop"
	if _, err := tasks.Create(ctx, input); !errors.Is(err, aiapp.ErrRunLeaseLost) {
		t.Fatalf("stopped run created a task: %v", err)
	}
	mutation.ToolUseID = "pause-after-stop"
	if _, err := tasks.PauseFromAuto(ctx, owner, ids[0], mutation); !errors.Is(err, aiapp.ErrRunLeaseLost) {
		t.Fatalf("stopped run paused a task: %v", err)
	}

	// Exercise the real ChatAgent/tool bridge, not just the transaction fence.
	runner, err := airuntime.NewChatRunner(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	chat := aiapp.NewConversationFacade(conversations, runner, resolve, nil)
	chat.SetAutoTools(func(user int) []tool.CoreTool { return aiinfra.AutoTaskWriteTools(tasks, user) })
	jobCtx, cancel := context.WithCancel(ctx)
	chat.Start(jobCtx)
	defer func() { cancel(); <-chat.Done() }()
	auto, err := chat.CreateWithAgent(ctx, owner, "auto execution", "auto")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Send(ctx, owner, auto.ID, "创建任务"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		page, err := chat.Activities(ctx, owner, auto.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if !page.Running {
			if page.RunStatus != "completed" {
				data, _ := json.Marshal(page)
				t.Fatalf("Auto run failed: %s", data)
			}
			var count int
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM ai_task WHERE owner_user_id=$1 AND name='Auto 创建测试' AND status='paused' AND queued=FALSE`, owner).Scan(&count); err != nil || count != 1 {
				t.Fatalf("Auto task creation: %d %v", count, err)
			}
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM ai_task t JOIN ai_task_relation r ON r.task_id=t.id WHERE t.owner_user_id=$1 AND t.name='Auto 创建测试' AND r.source_task_id=$2 AND t.timeout_seconds=0 AND t.plan_heartbeat_seconds=600`, owner, source.ID).Scan(&count); err != nil || count != 1 {
				t.Fatalf("Auto task source or defaults missing: %d %v", count, err)
			}
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM ai_task t JOIN tianqiong_ai.exploration_nodes n ON n.exploration_id=t.exploration_id WHERE t.owner_user_id=$1 AND t.name='Auto 创建测试' AND n.kind='intent' AND n.origin='seed'`, owner).Scan(&count); err != nil || count != 1 {
				t.Fatalf("Auto seed intent missing: %d %v", count, err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Auto conversation did not finish")
}

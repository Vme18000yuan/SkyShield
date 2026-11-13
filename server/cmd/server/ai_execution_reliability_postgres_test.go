package main

import (
	"context"
	"database/sql"
	"github.com/Autumn-27/norma/llm"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"iter"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func verifyAIExecutionReliability(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var user int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := repository.NewConversationStore(orm)
	conversation, run, owner := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = store.CreateConversation(ctx, user, conversation, "reconnect fixture"); err != nil {
		t.Fatal(err)
	}
	if err = store.BeginRun(ctx, user, conversation, run, owner, "start"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 450; i++ {
		if err = store.AppendActivity(ctx, user, conversation, run, owner, airuntime.Activity{Kind: "progress", Summary: "fixture"}); err != nil {
			t.Fatal(err)
		}
	}
	if err = store.AppendActivity(ctx, user, conversation, run, owner, airuntime.Activity{Kind: "result", Detail: "final result"}); err != nil {
		t.Fatal(err)
	}
	if err = store.FinishRunOutcome(ctx, user, conversation, run, owner, "timeout", "timeout", "time budget"); err != nil {
		t.Fatal(err)
	}
	var cursor int64
	count := 0
	pages := 0
	for {
		page, err := store.ListActivities(ctx, user, conversation, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if page.Running || page.RunStatus != "timeout" || page.TerminalReason != "timeout" {
			t.Fatalf("wrong terminal projection: %+v", page)
		}
		pages++
		count += len(page.Items)
		if len(page.Items) > 0 {
			cursor = page.Items[len(page.Items)-1].ID
		}
		if !page.HasMore && cursor >= page.FinalCursor {
			break
		}
		if pages > 5 {
			t.Fatal("feed failed to converge")
		}
	}
	if pages != 3 || count != 452 {
		t.Fatalf("lost terminal tail: pages=%d rows=%d", pages, count)
	}

	permits := repository.NewExecutionPermitStore(orm)
	var admitted atomic.Int64
	var wg sync.WaitGroup
	errors := make(chan error, 24)
	ids := make(chan string, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := uuid.NewString()
			ok, err := permits.ClaimExecution(ctx, user, id, owner, "chat")
			if err != nil {
				errors <- err
			}
			if ok {
				admitted.Add(1)
				ids <- id
			}
		}()
	}
	wg.Wait()
	close(errors)
	close(ids)
	for err := range errors {
		t.Error(err)
	}
	for id := range ids {
		if err = permits.ReleaseExecution(ctx, id, owner); err != nil {
			t.Error(err)
		}
	}
	if admitted.Load() != 8 {
		t.Fatalf("per-user permits overbooked or underfilled: %d", admitted.Load())
	}
	var processes []string
	for i := 0; i < 8; i++ {
		kind := "process"
		if i >= 6 {
			kind = "process_aux"
		}
		id := uuid.NewString()
		ok, err := permits.ClaimExecution(ctx, user, id, owner, kind)
		if err != nil || !ok {
			t.Fatalf("reserved process capacity unavailable: %s %v", kind, err)
		}
		processes = append(processes, id)
		if i == 5 {
			ok, err = permits.ClaimExecution(ctx, user, uuid.NewString(), owner, "process")
			if err != nil || ok {
				t.Fatalf("persistent process consumed reserved capacity: %v %v", ok, err)
			}
		}
	}
	for _, id := range processes {
		if err = permits.ReleaseExecution(ctx, id, owner); err != nil {
			t.Fatal(err)
		}
	}
}

type plannerDispatchProbe struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	plans   atomic.Int64
}

func (p *plannerDispatchProbe) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	role := "goals"
	for _, item := range req.Tools {
		if item.Name == "add_intent" {
			role = "planner"
			break
		}
		if item.Name == "record_fact" {
			role = "worker"
		}
	}
	return func(yield func(llm.StreamEvent, error) bool) {
		if role == "worker" {
			p.once.Do(func() { close(p.started) })
		}
		if role == "planner" && p.plans.Add(1) == 1 {
			for _, ev := range []llm.StreamEvent{{Type: llm.SEToolUseStart, ToolID: "dispatch-probe", ToolName: "add_intent"}, {Type: llm.SEToolInputJSON, Text: `{"summary":"dispatch while planner waits","priority":5}`}, {Type: llm.SEMessageDelta, StopReason: "tool_use"}} {
				if !yield(ev, nil) {
					return
				}
			}
			select {
			case <-p.release:
			case <-ctx.Done():
				yield(llm.StreamEvent{}, ctx.Err())
				return
			}
		} else {
			if !yield(llm.StreamEvent{Type: llm.SETextDelta, Text: "fixture"}, nil) {
				return
			}
			if !yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"}, nil) {
				return
			}
		}
		yield(llm.StreamEvent{Type: llm.SEMessageStop}, nil)
	}
}
func (p *plannerDispatchProbe) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	a := llm.NewAccumulator()
	for ev, err := range p.Stream(ctx, req) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		a.Add(ev)
	}
	return a.Message(), a.StopReason, a.Usage, nil
}

func verifyPlannerDoesNotBlockWorkerDispatch(t *testing.T, ctx context.Context, db *sql.DB, dsn string) {
	t.Helper()
	var user int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := repository.NewTaskStore(orm)
	task, err := store.CreateTask(ctx, aiapp.CreateTaskInput{UserID: user, Name: "dispatch isolation", Goal: "fixture", WorkerCount: 1, PlanHeartbeatSeconds: 600})
	if err != nil {
		t.Fatal(err)
	}
	run, owner := uuid.NewString(), uuid.NewString()
	if _, err = db.ExecContext(ctx, `UPDATE ai_task SET status='running',queued=FALSE,active_run=$1,lease_owner=$2,lease_epoch=1,lease_until=clock_timestamp()+interval '45 seconds' WHERE id=$3`, run, owner, task.ID); err != nil {
		t.Fatal(err)
	}
	task, err = store.GetTask(ctx, user, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	connector, err := airuntime.NewTaskGraphConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	runner := airuntime.NewTaskRunner(connector, t.TempDir(), nil)
	provider := &plannerDispatchProbe{started: make(chan struct{}), release: make(chan struct{})}
	runCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	done := make(chan struct{})
	execution := taskExecution(task)
	execution.Bootstrap = true
	var outcome airuntime.TaskOutcome
	var runErr error
	go func() { defer close(done); outcome, runErr = runner.RunTask(runCtx, execution, provider, "fixture") }()
	defer func() {
		cancel()
		close(provider.release)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("planner did not stop")
		}
		_, _ = db.ExecContext(context.Background(), `UPDATE ai_task SET status='paused',lease_until=NULL WHERE id=$1`, task.ID)
	}()
	select {
	case <-provider.started:
	case <-done:
		t.Fatalf("runtime exited before dispatch: %+v %v", outcome, runErr)
	case <-time.After(8 * time.Second):
		t.Fatal("worker dispatch waited for a blocked planner")
	}
}

package main

import (
	"context"
	"database/sql"
	"iter"
	"strings"
	"testing"
	"time"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type recoveryMessageProvider struct {
	mentionModelFixture
	block bool
	input chan string
}

func (p *recoveryMessageProvider) Stream(ctx context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	worker := false
	for _, item := range request.Tools {
		worker = worker || item.Name == "record_fact"
	}
	if !worker {
		return p.mentionModelFixture.Stream(ctx, request)
	}
	var input string
	for _, item := range request.Messages {
		input += item.Text() + "\n"
	}
	return func(yield func(llm.StreamEvent, error) bool) {
		select {
		case p.input <- input:
		default:
		}
		if p.block {
			<-ctx.Done()
			yield(llm.StreamEvent{}, ctx.Err())
			return
		}
		for event, err := range p.mentionModelFixture.Stream(ctx, request) {
			if !yield(event, err) {
				return
			}
		}
	}
}

func (p *recoveryMessageProvider) Complete(ctx context.Context, request llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, request) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func verifyARTEXIntentMessageRecoveryPostgres(t *testing.T, ctx context.Context, db *sql.DB, dsn string) {
	t.Helper()
	owner, target, asset := taskFixtureScope(t, ctx, db, "intent-message-recovery.example")
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := airepo.NewTaskStore(orm)
	facade := aiapp.NewTaskFacade(store, nil, nil)
	task, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "消息恢复", TargetID: target, AssetIDs: []int64{asset}, WorkerCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ControlTask(ctx, owner, task.ID, "start", false); err != nil {
		t.Fatal(err)
	}
	claim := func() *aiapp.Task {
		t.Helper()
		run, e := store.ClaimTask(ctx, uuid.NewString())
		if e != nil || run == nil || run.ID != task.ID {
			t.Fatalf("claim message recovery task: %+v %v", run, e)
		}
		run, e = store.StampTaskStart(ctx, run)
		if e != nil {
			t.Fatal(e)
		}
		return run
	}
	first := claim()
	connector, err := airuntime.NewTaskGraphConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := connector.OpenForExecution(ctx, taskExecution(first))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	graph := (&artexdb.DB{DB: connection}).Exploration(task.ExplorationID)
	if _, err = graph.AddGoal(map[string]any{"text": "保持任务活动"}, "human"); err != nil {
		t.Fatal(err)
	}
	intent, err := graph.AddIntent(map[string]any{"summary": "恢复人工消息"}, 1, []int64{asset}, "planner")
	if err != nil {
		t.Fatal(err)
	}
	if err = graph.SetIntentState(intent, "paused"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	start := func(run *aiapp.Task, provider *recoveryMessageProvider) (context.CancelFunc, <-chan struct{}) {
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			_, _ = airuntime.NewTaskRunner(connector, root, nil).RunTask(runCtx, taskExecution(run), provider, "fixture")
			close(done)
		}()
		return cancel, done
	}
	waitInput := func(ch <-chan string) string {
		t.Helper()
		select {
		case input := <-ch:
			return input
		case <-time.After(6 * time.Second):
			t.Fatal("worker message did not start")
			return ""
		}
	}
	waitDone := func(ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(6 * time.Second):
			t.Fatal("message task did not stop")
		}
	}
	p1 := &recoveryMessageProvider{block: true, input: make(chan string, 1)}
	cancel1, done1 := start(first, p1)
	defer cancel1()
	if err = facade.SendWorkerMessage(ctx, owner, task.ID, intent, "recovery-request", "人工恢复消息"); err != nil {
		t.Fatal(err)
	}
	marker := "ARTEX_WORKER_CHAT:recovery-request"
	if input := waitInput(p1.input); !strings.Contains(input, marker) {
		t.Fatalf("first Worker input did not contain request marker: %s", input)
	}
	cancel1()
	waitDone(done1)
	if err = store.FinishTask(ctx, first, "paused", "interrupted"); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = db.QueryRowContext(ctx, `SELECT status FROM ai_task_intent_message WHERE task_id=$1 AND request_id='recovery-request'`, task.ID).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("interrupted message was lost: %s %v", status, err)
	}
	if _, err = store.ControlTask(ctx, owner, task.ID, "resume", false); err != nil {
		t.Fatal(err)
	}
	second := claim()
	p2 := &recoveryMessageProvider{input: make(chan string, 1)}
	cancel2, done2 := start(second, p2)
	defer cancel2()
	if input := waitInput(p2.input); strings.Count(input, marker) != 1 || !strings.Contains(input, "人工恢复消息") {
		t.Fatalf("recovered transcript duplicated or lost human input: %s", input)
	}
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if err = db.QueryRowContext(ctx, `SELECT status FROM ai_task_intent_message WHERE task_id=$1 AND request_id='recovery-request'`, task.ID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status == "delivered" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status != "delivered" {
		t.Fatalf("recovered message did not finish: %s", status)
	}
	var activities int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.activity WHERE exploration_id=$1 AND node_id=$2 AND worker='user' AND kind='user' AND detail='人工恢复消息'`, task.ExplorationID, intent).Scan(&activities); err != nil || activities != 1 {
		t.Fatalf("recovery duplicated user activity: %d %v", activities, err)
	}
	cancel2()
	waitDone(done2)
	if err = store.FinishTask(ctx, second, "paused", ""); err != nil {
		t.Fatal(err)
	}
}

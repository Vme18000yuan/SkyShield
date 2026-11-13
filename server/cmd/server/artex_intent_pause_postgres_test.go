package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	registryapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	registryrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type pauseModelFixture struct {
	mentionModelFixture
	calls      atomic.Int64
	started    chan struct{}
	cancelled  chan struct{}
	release    chan struct{}
	resumed    chan struct{}
	resumeOnce sync.Once
	busy       chan struct{}
	busyDone   chan struct{}
	message    chan string
}

func (p *pauseModelFixture) Stream(ctx context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	worker := false
	for _, item := range request.Tools {
		worker = worker || item.Name == "record_fact"
	}
	if !worker {
		return p.mentionModelFixture.Stream(ctx, request)
	}
	system := strings.Join(request.System, "\n")
	if strings.Contains(system, "占用执行槽") {
		return func(yield func(llm.StreamEvent, error) bool) {
			close(p.busy)
			select {
			case <-p.busyDone:
			case <-ctx.Done():
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
	if strings.Contains(system, "人工消息意图") {
		var input string
		for _, item := range request.Messages {
			input += item.Text()
		}
		select {
		case p.message <- input:
		default:
		}
		return p.mentionModelFixture.Stream(ctx, request)
	}
	if p.calls.Add(1) == 1 {
		return func(yield func(llm.StreamEvent, error) bool) {
			close(p.started)
			<-ctx.Done()
			close(p.cancelled)
			<-p.release
			yield(llm.StreamEvent{}, ctx.Err())
		}
	}
	p.resumeOnce.Do(func() { close(p.resumed) })
	return p.mentionModelFixture.Stream(ctx, request)
}

func (p *pauseModelFixture) Complete(ctx context.Context, request llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, request) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func verifyARTEXIntentPausePostgres(t *testing.T, ctx context.Context, db *sql.DB, dsn string) {
	t.Helper()
	owner, target, asset := taskFixtureScope(t, ctx, db, "intent-pause.example")
	var other int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := airepo.NewTaskStore(orm)
	facade := aiapp.NewTaskFacade(store, nil, nil)
	facade.SetChatMentions(aiapp.NewChatMentionFacade(airepo.NewChatMentionStore(orm), registryapp.NewFacade(registryrepo.NewAssetRegistryStore(orm))))
	task, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "暂停后继续", TargetID: target, AssetIDs: []int64{asset}, WorkerCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ControlTask(ctx, owner, task.ID, "start", false); err != nil {
		t.Fatal(err)
	}
	run, err := store.ClaimTask(ctx, uuid.NewString())
	if err != nil || run == nil || run.ID != task.ID {
		t.Fatalf("claim pause task: %+v %v", run, err)
	}
	run, err = store.StampTaskStart(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	connector, err := airuntime.NewTaskGraphConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := connector.OpenForExecution(ctx, taskExecution(run))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	graph := (&artexdb.DB{DB: connection}).Exploration(task.ExplorationID)
	if _, err = graph.AddGoal(map[string]any{"text": "保持任务运行"}, "human"); err != nil {
		t.Fatal(err)
	}
	intent, err := graph.AddIntent(map[string]any{"summary": "可暂停的意图"}, 1, []int64{asset}, "planner")
	if err != nil {
		t.Fatal(err)
	}
	provider := &pauseModelFixture{started: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{}), resumed: make(chan struct{}), busy: make(chan struct{}), busyDone: make(chan struct{}), message: make(chan string, 1)}
	var release sync.Once
	releaseWorker := func() { release.Do(func() { close(provider.release) }) }
	var releaseBusyOnce sync.Once
	releaseBusy := func() { releaseBusyOnce.Do(func() { close(provider.busyDone) }) }
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	defer releaseWorker()
	defer releaseBusy()
	type outcome struct {
		result airuntime.TaskOutcome
		err    error
	}
	done := make(chan outcome, 1)
	workspace := t.TempDir()
	go func() {
		result, e := airuntime.NewTaskRunner(connector, workspace, nil).RunTask(runCtx, taskExecution(run), provider, "fixture")
		done <- outcome{result, e}
	}()
	wait := func(ch <-chan struct{}, label string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(6 * time.Second):
			t.Fatalf("timed out waiting for %s", label)
		}
	}
	wait(provider.started, "worker start")
	if _, err = facade.ControlIntent(ctx, other, task.ID, intent, "pause"); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign worker pause: %v", err)
	}
	type controlResult struct {
		state string
		err   error
	}
	pauseResult := make(chan controlResult, 1)
	go func() {
		state, e := facade.ControlIntent(ctx, owner, task.ID, intent, "pause")
		pauseResult <- controlResult{state, e}
	}()
	wait(provider.cancelled, "worker cancellation")
	if _, err = store.QueueIntentPause(ctx, owner, task.ID, intent); !errors.Is(err, aiapp.ErrTaskControlConflict) {
		t.Fatalf("duplicate pending pause: %v", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_task_intent_control SET status='paused',finished_at=clock_timestamp() WHERE task_id=$1 AND status='pending'`, task.ID); err == nil {
		t.Fatal("unfenced pause acknowledgement accepted")
	}
	select {
	case result := <-pauseResult:
		t.Fatalf("pause returned before worker settled: %+v", result)
	default:
	}
	releaseWorker()
	select {
	case result := <-pauseResult:
		if result.err != nil || result.state != "paused" {
			t.Fatalf("pause result: %+v", result)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("pause did not settle")
	}
	var state string
	if err = db.QueryRowContext(ctx, `SELECT state FROM tianqiong_ai.exploration_nodes WHERE id=$1`, intent).Scan(&state); err != nil || state != "paused" {
		t.Fatalf("paused state: %s %v", state, err)
	}
	if state, err = facade.ControlIntent(ctx, owner, task.ID, intent, "resume"); err != nil || state != "open" {
		t.Fatalf("resume intent: %s %v", state, err)
	}
	wait(provider.resumed, "resumed worker")
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if err = db.QueryRowContext(ctx, `SELECT state FROM tianqiong_ai.exploration_nodes WHERE id=$1`, intent).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "done" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if state != "done" {
		t.Fatalf("resumed worker did not complete: %s", state)
	}
	messageIntent, err := graph.AddIntent(map[string]any{"summary": "人工消息意图"}, 1, []int64{asset}, "planner")
	if err != nil {
		t.Fatal(err)
	}
	if err = graph.SetIntentState(messageIntent, "paused"); err != nil {
		t.Fatal(err)
	}
	if _, err = graph.AddIntent(map[string]any{"summary": "占用执行槽"}, 1, []int64{asset}, "planner"); err != nil {
		t.Fatal(err)
	}
	wait(provider.busy, "busy worker slot")
	humanMessage := fmt.Sprintf("先核验 @[资产#%d 用户所选资产]", asset)
	if err = facade.SendWorkerMessage(ctx, other, task.ID, messageIntent, "fixture-request", humanMessage); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign Worker message: %v", err)
	}
	if err = facade.SendWorkerMessage(ctx, owner, task.ID, messageIntent, "missing-ref", "检查 @[风险#999999999 不存在]"); !errors.Is(err, aiapp.ErrInvalidInput) {
		t.Fatalf("invalid Worker reference: %v", err)
	}
	if err = facade.SendWorkerMessage(ctx, owner, task.ID, messageIntent, "fixture-request", humanMessage); err != nil {
		t.Fatalf("send Worker message: %v", err)
	}
	if err = facade.SendWorkerMessage(ctx, owner, task.ID, messageIntent, "fixture-request", humanMessage); err != nil {
		t.Fatalf("duplicate Worker message: %v", err)
	}
	if err = facade.SendWorkerMessage(ctx, owner, task.ID, messageIntent, "fixture-request", "different"); !errors.Is(err, aiapp.ErrTaskControlConflict) {
		t.Fatalf("request id reused with different input: %v", err)
	}
	select {
	case input := <-provider.message:
		if !strings.Contains(input, humanMessage) || !strings.Contains(input, "api.intent-pause.example") || !strings.Contains(input, "ARTEX_WORKER_CHAT:fixture-request") {
			t.Fatalf("Worker did not receive human message: %s", input)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("Worker message waited for the occupied pool")
	}
	deadline = time.Now().Add(6 * time.Second)
	var delivery string
	for time.Now().Before(deadline) {
		if err = db.QueryRowContext(ctx, `SELECT status FROM ai_task_intent_message WHERE task_id=$1 AND request_id='fixture-request'`, task.ID).Scan(&delivery); err != nil {
			t.Fatal(err)
		}
		if delivery == "delivered" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if delivery != "delivered" {
		t.Fatalf("Worker message not acknowledged: %s", delivery)
	}
	var messages int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.activity WHERE exploration_id=$1 AND node_id=$2 AND worker='user' AND kind='user' AND detail=$3`, task.ExplorationID, messageIntent, humanMessage).Scan(&messages); err != nil || messages != 1 {
		t.Fatalf("Worker message activity was not deduplicated: %d %v", messages, err)
	}
	releaseBusy()
	cancelRun()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("task runner did not stop")
	}
	if err = store.FinishTask(ctx, run, "paused", ""); err != nil {
		t.Fatal(err)
	}
}

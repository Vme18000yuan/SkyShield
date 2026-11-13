package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"iter"
	"sync/atomic"
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

type rerunModelFixture struct {
	mentionModelFixture
	worker  atomic.Int64
	planner atomic.Int64
}

func (p *rerunModelFixture) Stream(ctx context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	for _, item := range request.Tools {
		switch item.Name {
		case "record_fact":
			p.worker.Add(1)
		case "add_intent":
			p.planner.Add(1)
		}
	}
	return p.mentionModelFixture.Stream(ctx, request)
}

func (p *rerunModelFixture) Complete(ctx context.Context, request llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, request) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func verifyARTEXIntentRerunPostgres(t *testing.T, ctx context.Context, db *sql.DB, dsn string) {
	t.Helper()
	owner, target, asset := taskFixtureScope(t, ctx, db, "intent-rerun.example")
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
	task, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "执行原有意图", TargetID: target, AssetIDs: []int64{asset}, WorkerCount: 1, TimeoutSeconds: 3600})
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
			t.Fatalf("claim rerun task: %+v %v", run, e)
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
	var intents []int64
	for _, state := range []string{"blocked", "blocked", "exhausted", "done"} {
		id, e := graph.AddIntent(map[string]any{"summary": "重跑 " + state}, 1, []int64{asset}, "planner")
		if e != nil {
			t.Fatal(e)
		}
		if e = graph.SetIntentState(id, state); e != nil {
			t.Fatal(e)
		}
		intents = append(intents, id)
	}
	// This fixture models an already bootstrapped task with no remaining goals.
	if _, err = connection.ExecContext(ctx, `UPDATE public.ai_task SET bootstrap_complete=TRUE WHERE id=$1 AND active_run=$2::uuid`, task.ID, first.ActiveRun); err != nil {
		t.Fatal(err)
	}
	if err = store.FinishTask(ctx, first, "done", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = facade.RerunIntent(ctx, other, task.ID, intents[0]); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign rerun: %v", err)
	}
	if _, err = facade.RerunIntent(ctx, owner, task.ID, intents[3]); !errors.Is(err, aiapp.ErrTaskControlConflict) {
		t.Fatalf("completed intent rerun: %v", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `SELECT set_config('tianqiong.task_id',$1,TRUE),set_config('tianqiong.owner_user_id',$2,TRUE),set_config('tianqiong.execution_mode','human_intent',TRUE),set_config('tianqiong.intent_id',$3,TRUE)`, fmt.Sprint(task.ID), fmt.Sprint(owner), fmt.Sprint(intents[0])); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE tianqiong_ai.exploration_nodes SET state='open',owner=NULL,blocked_reason=NULL,completed_at=NULL,content_version=content_version+1,payload='{"summary":"tampered"}' WHERE id=$1`, intents[0]); err == nil {
		t.Fatal("human intent scope changed payload")
	}
	_ = tx.Rollback()
	if count, e := facade.RerunIntent(ctx, owner, task.ID, intents[0]); e != nil || count != 1 {
		t.Fatalf("single intent rerun: %d %v", count, e)
	}
	if _, e := facade.RerunIntent(ctx, owner, task.ID, intents[0]); !errors.Is(e, aiapp.ErrTaskControlConflict) {
		t.Fatalf("duplicate intent rerun: %v", e)
	}
	provider := &rerunModelFixture{}
	runner := airuntime.NewTaskRunner(connector, t.TempDir(), nil)
	runWorker := func(run *aiapp.Task, bootstrap bool) {
		t.Helper()
		execution := taskExecution(run)
		execution.Bootstrap = bootstrap
		runCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		outcome, e := runner.RunTask(runCtx, execution, provider, "fixture")
		if e != nil || outcome.Status != "done" {
			t.Fatalf("rerun worker: %+v %v", outcome, e)
		}
		if e = store.FinishTask(ctx, run, "done", ""); e != nil {
			t.Fatal(e)
		}
	}
	runWorker(claim(), false)
	var state string
	if err = db.QueryRowContext(ctx, `SELECT state FROM tianqiong_ai.exploration_nodes WHERE id=$1`, intents[0]).Scan(&state); err != nil || state != "done" {
		t.Fatalf("reopened intent did not execute: %s %v", state, err)
	}
	if provider.worker.Load() == 0 || provider.planner.Load() != 0 {
		t.Fatalf("goalless rerun must use worker without planner: worker=%d planner=%d", provider.worker.Load(), provider.planner.Load())
	}
	var anchors int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.exploration_anchors WHERE node_id=$1 AND asset_id=$2`, intents[0], asset).Scan(&anchors); err != nil || anchors != 1 {
		t.Fatalf("rerun lost existing asset anchors: %d %v", anchors, err)
	}
	if count, e := facade.RerunIntent(ctx, owner, task.ID, 0); e != nil || count != 1 {
		t.Fatalf("batch blocked rerun: %d %v", count, e)
	}
	second := claim()
	if count, e := facade.RerunIntent(ctx, owner, task.ID, intents[2]); e != nil || count != 1 {
		t.Fatalf("rerun during active task: %d %v", count, e)
	}
	if err = store.FinishTask(ctx, second, "timeout", "deadline"); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetTask(ctx, owner, task.ID)
	if err != nil || !loaded.Queued || loaded.Status != "created" || loaded.FirstRunAt != nil || loaded.DeadlineAt != nil || !loaded.BootstrapComplete {
		t.Fatalf("timeout lost pending rerun: %+v %v", loaded, err)
	}
	runWorker(claim(), false)
	var completed, goals, pending int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.exploration_nodes WHERE id IN ($1,$2) AND state='done'`, intents[1], intents[2]).Scan(&completed); err != nil || completed != 2 {
		t.Fatalf("batch and timeout rerun did not execute: %d %v", completed, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.exploration_nodes WHERE exploration_id=$1 AND kind='goal'`, task.ExplorationID).Scan(&goals); err != nil || goals != 0 {
		t.Fatalf("rerun bootstrapped unwanted goals: %d %v", goals, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_task_human_event WHERE task_id=$1 AND processed_at IS NULL`, task.ID).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("rerun events not acknowledged: %d %v", pending, err)
	}
}

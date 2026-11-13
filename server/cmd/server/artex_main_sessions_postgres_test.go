package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyARTEXMainSessionsPostgres(t *testing.T, ctx context.Context, db *sql.DB, dsn string) {
	t.Helper()
	owner, target, asset := taskFixtureScope(t, ctx, db, "main-segments.example")
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
	task, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "会话隔离", TargetID: target, AssetIDs: []int64{asset}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = facade.CreateMainSession(ctx, other, task.ID); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign session creation: %v", err)
	}
	const workers = 12
	type outcome struct {
		seq int
		err error
	}
	results := make(chan outcome, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			item, err := facade.CreateMainSession(ctx, owner, task.ID)
			results <- outcome{item.Seq, err}
		}()
	}
	wg.Wait()
	close(results)
	seen := map[int]bool{}
	for result := range results {
		if result.err != nil || result.seq < 1 || result.seq > workers || seen[result.seq] {
			t.Fatalf("concurrent session creation: %+v %v", result, seen)
		}
		seen[result.seq] = true
	}
	sessions, err := facade.MainSessions(ctx, owner, task.ID)
	if err != nil || len(sessions) != workers+1 || sessions[0].Seq != workers || sessions[len(sessions)-1].Seq != 0 {
		t.Fatalf("main sessions: %+v %v", sessions, err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO tianqiong_ai.main_sessions(exploration_id,seq) VALUES($1,99)`, task.ExplorationID); err == nil {
		t.Fatal("unfenced session write accepted")
	}
	if _, err = store.ControlTask(ctx, owner, task.ID, "start", false); err != nil {
		t.Fatal(err)
	}
	var run *aiapp.Task
	for attempts := 0; attempts < 100; attempts++ {
		run, err = store.ClaimTask(ctx, uuid.NewString())
		if err != nil || run == nil {
			t.Fatalf("claim session task: %+v %v", run, err)
		}
		if run.ID == task.ID {
			break
		}
		if err = store.FinishTask(ctx, run, "paused", "fixture cleanup"); err != nil {
			t.Fatal(err)
		}
		run = nil
	}
	if run == nil || run.ID != task.ID {
		t.Fatal("session task not claimed")
	}
	run, err = store.StampTaskStart(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	connector, err := airuntime.NewTaskGraphConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := connector.OpenForExecution(ctx, taskExecution(run))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	graph := (&artexdb.DB{DB: conn}).Exploration(task.ExplorationID)
	zero, one := 0, 1
	for i := 0; i < 102; i++ {
		if _, err = graph.AppendActivity(artexdb.Activity{Worker: "mainagent", Kind: "human_message", MainSeg: &zero, Summary: fmt.Sprintf("old-%03d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = graph.AppendActivity(artexdb.Activity{Worker: "mainagent", Kind: "human_message", MainSeg: &one, Summary: "other segment"}); err != nil {
		t.Fatal(err)
	}
	page, err := facade.MainMessages(ctx, owner, task.ID, 0, 0)
	if err != nil || len(page) != 101 || page[0].Summary != "old-101" || page[99].Summary != "old-002" {
		t.Fatalf("session message page: %+v %v", page, err)
	}
	older, err := facade.MainMessages(ctx, owner, task.ID, 0, page[99].ID)
	if err != nil || len(older) != 2 || older[0].Summary != "old-001" || older[1].Summary != "old-000" {
		t.Fatalf("older session messages: %+v %v", older, err)
	}
	second, err := facade.MainMessages(ctx, owner, task.ID, 1, 0)
	if err != nil || len(second) != 1 || second[0].Summary != "other segment" {
		t.Fatalf("separate segment: %+v %v", second, err)
	}
	if _, err = facade.MainMessages(ctx, other, task.ID, 0, 0); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign messages: %v", err)
	}
	if err = store.FinishTask(ctx, run, "paused", ""); err != nil {
		t.Fatal(err)
	}
}

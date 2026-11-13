package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyARTEXTaskGoalsPostgres(t *testing.T, ctx context.Context, db *sql.DB, dsn string) {
	t.Helper()
	owner, target, asset := taskFixtureScope(t, ctx, db, "human-goal.example")
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
	task, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "人工目标 fixture", Goal: "原始任务目标", TargetID: target, AssetIDs: []int64{asset}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = facade.SaveGoal(ctx, other, task.ID, 0, "foreign", ""); err == nil {
		t.Fatal("foreign goal accepted")
	}
	if _, err = facade.SaveGoal(ctx, owner, task.ID, 0, "  ", ""); !errors.Is(err, aiapp.ErrInvalidInput) {
		t.Fatalf("empty goal: %v", err)
	}
	goal, err := facade.SaveGoal(ctx, owner, task.ID, 0, "验证公开接口", "api")
	if err != nil || goal.State != "open" || goal.Origin != "human" {
		t.Fatalf("create goal: %+v %v", goal, err)
	}
	loaded, err := store.GetTask(ctx, owner, task.ID)
	if err != nil || !loaded.Queued || loaded.Status != "created" {
		t.Fatalf("goal did not queue task: %+v %v", loaded, err)
	}
	var edges int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.exploration_edges WHERE exploration_id=$1 AND dst_id=$2 AND rel='spawns'`, task.ExplorationID, goal.ID).Scan(&edges); err != nil || edges != 1 {
		t.Fatalf("goal origin edge: %d %v", edges, err)
	}
	claimed, err := store.ClaimTask(ctx, uuid.NewString())
	if err != nil || claimed == nil || claimed.ID != task.ID {
		t.Fatalf("claim goal task: %+v %v", claimed, err)
	}
	claimed, err = store.StampTaskStart(ctx, claimed)
	if err != nil {
		t.Fatal(err)
	}
	connector, err := airuntime.NewTaskGraphConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := connector.OpenForExecution(ctx, taskExecution(claimed))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	graph := (&artexdb.DB{DB: connection}).Exploration(task.ExplorationID)
	if err = graph.Anchor(goal.ID, asset); err != nil {
		t.Fatal(err)
	}
	activity, err := graph.AppendActivity(artexdb.Activity{NodeID: &goal.ID, Worker: "planner", Kind: "note", Summary: "goal reference"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.FinishTask(ctx, claimed, "done", ""); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.GetTask(ctx, owner, task.ID)
	if err != nil || loaded.Status != "created" || !loaded.Queued {
		t.Fatalf("unprocessed goal lost at task completion: %+v %v", loaded, err)
	}
	if _, err = store.ControlTask(ctx, owner, task.ID, "pause", false); err != nil {
		t.Fatal(err)
	}
	goal, err = facade.SaveGoal(ctx, owner, task.ID, goal.ID, "验证只读接口", "api")
	if err != nil || goal.Text != "验证只读接口" {
		t.Fatalf("edit goal: %+v %v", goal, err)
	}
	loaded, err = store.GetTask(ctx, owner, task.ID)
	if err != nil || loaded.Status != "created" || !loaded.Queued {
		t.Fatalf("goal edit did not revive task: %+v %v", loaded, err)
	}
	if _, err = store.ControlTask(ctx, owner, task.ID, "pause", false); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `SELECT set_config('tianqiong.task_id',$1,TRUE),set_config('tianqiong.owner_user_id',$2,TRUE),set_config('tianqiong.execution_mode','human_goal',TRUE),set_config('tianqiong.goal_id',$3,TRUE)`, fmt.Sprint(task.ID), fmt.Sprint(owner), fmt.Sprint(goal.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE tianqiong_ai.exploration_nodes SET state='met' WHERE id=$1`, goal.ID); err == nil {
		t.Fatal("human goal scope changed state")
	}
	_ = tx.Rollback()
	if err = facade.DeleteGoal(ctx, other, task.ID, goal.ID); err == nil {
		t.Fatal("foreign goal delete accepted")
	}
	if err = facade.DeleteGoal(ctx, owner, task.ID, goal.ID); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.GetTask(ctx, owner, task.ID)
	if err != nil || loaded.Status != "paused" || loaded.Queued {
		t.Fatalf("goal delete unexpectedly revived task: %+v %v", loaded, err)
	}
	goals, err := facade.Goals(ctx, owner, task.ID)
	if err != nil || len(goals) != 0 {
		t.Fatalf("deleted goal: %+v %v", goals, err)
	}
	var anchorCount int
	var node sql.NullInt64
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.exploration_anchors WHERE node_id=$1`, goal.ID).Scan(&anchorCount); err != nil || anchorCount != 0 {
		t.Fatalf("goal anchor cleanup: %d %v", anchorCount, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT node_id FROM tianqiong_ai.activity WHERE id=$1`, activity).Scan(&node); err != nil || node.Valid {
		t.Fatalf("goal activity cleanup: %+v %v", node, err)
	}
	var events int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_task_human_event WHERE task_id=$1 AND processed_at IS NULL`, task.ID).Scan(&events); err != nil || events != 3 {
		t.Fatalf("durable goal events: %d %v", events, err)
	}
	if _, err = store.ControlTask(ctx, owner, task.ID, "resume", false); err != nil {
		t.Fatal(err)
	}
	resumed, err := store.ClaimTask(ctx, uuid.NewString())
	if err != nil || resumed == nil || resumed.ID != task.ID {
		t.Fatalf("resume goal task: %+v %v", resumed, err)
	}
	resumed, err = store.StampTaskStart(ctx, resumed)
	if err != nil {
		t.Fatal(err)
	}
	runtime := airuntime.NewTaskRunner(connector, t.TempDir(), nil)
	outcome, err := runtime.RunTask(ctx, taskExecution(resumed), &mentionModelFixture{}, "fixture")
	if err != nil || outcome.Status != "done" {
		t.Fatalf("resume without goals: %+v %v", outcome, err)
	}
	if err = store.FinishTask(ctx, resumed, "done", ""); err != nil {
		t.Fatal(err)
	}
	goals, err = facade.Goals(ctx, owner, task.ID)
	if err != nil || len(goals) != 0 {
		t.Fatalf("deleted goals recreated on resume: %+v %v", goals, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_task_human_event WHERE task_id=$1 AND processed_at IS NULL`, task.ID).Scan(&events); err != nil || events != 0 {
		t.Fatalf("goal events were not acknowledged: %d %v", events, err)
	}
}

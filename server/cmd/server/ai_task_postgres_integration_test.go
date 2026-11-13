package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	registryapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	registryrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func taskFixtureScope(t *testing.T, ctx context.Context, db *sql.DB, name string) (int, int, int64) {
	t.Helper()
	var owner, target int
	var website, asset int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES($1,'domain') RETURNING id`, name).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO website(target_id,url) VALUES($1,$2) RETURNING id`, target, "https://api."+name+"/").Scan(&website); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT asset_id FROM asset_website_binding WHERE website_id=$1`, website).Scan(&asset); err != nil {
		t.Fatal(err)
	}
	return owner, target, asset
}

func taskExecution(task *aiapp.Task) airuntime.TaskExecution {
	target := 0
	if task.TargetID != nil {
		target = *task.TargetID
	}
	var deadline time.Time
	if task.DeadlineAt != nil {
		deadline = *task.DeadlineAt
	}
	return airuntime.TaskExecution{ID: task.ID, ExplorationID: task.ExplorationID, TenantID: task.TenantID, UserID: task.OwnerUserID, RunID: task.ActiveRun, TargetID: target,
		Goal: task.Goal, Description: task.Description, WorkerCount: task.WorkerCount, MaxTurns: task.MaxTurns, Deadline: deadline, PlanHeartbeatSeconds: task.PlanHeartbeatSeconds}
}

func verifyAITaskPostgresContract(t *testing.T, ctx context.Context, db *sql.DB, dsn string) {
	t.Helper()
	owner, target, asset := taskFixtureScope(t, ctx, db, "task-scope.example")
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := airepo.NewTaskStore(orm)
	facade := aiapp.NewTaskFacade(store, nil, nil)
	_, _, foreignAsset := taskFixtureScope(t, ctx, db, "outside-task-scope.example")
	if _, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "范围测试", TargetID: target, AssetIDs: []int64{foreignAsset}}); !errors.Is(err, registryapp.ErrAssetOutsideTarget) {
		t.Fatalf("cross-target asset accepted: %v", err)
	}
	task, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "图租约测试", Goal: "核验资产", TargetID: target, AssetIDs: []int64{asset}, WorkerCount: 1, TimeoutSeconds: 3600})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO tianqiong_ai.task_constraints(exploration_id,kind,text) VALUES($1,'deny','unfenced')`, task.ExplorationID); err == nil {
		t.Fatal("unfenced constraint accepted")
	}
	constraint, err := facade.SaveConstraint(ctx, owner, task.ID, 0, "deny", "禁止写入生产数据库")
	if err != nil || constraint.Origin != "human" {
		t.Fatalf("create constraint: %+v %v", constraint, err)
	}
	if _, err = facade.SaveConstraint(ctx, owner, task.ID, constraint.ID, "allow", "仅限读取"); err != nil {
		t.Fatal(err)
	}
	constraints, err := facade.Constraints(ctx, owner, task.ID)
	if err != nil || len(constraints) != 1 || constraints[0].Kind != "allow" || constraints[0].Text != "仅限读取" {
		t.Fatalf("constraint update: %+v %v", constraints, err)
	}
	if _, err = facade.SaveConstraint(ctx, owner, task.ID, 0, "invalid", "invalid"); !errors.Is(err, aiapp.ErrInvalidInput) {
		t.Fatalf("invalid constraint: %v", err)
	}
	var foreignConstraintUser int
	if err = db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&foreignConstraintUser); err != nil {
		t.Fatal(err)
	}
	txConstraint, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = txConstraint.ExecContext(ctx, `SELECT set_config('tianqiong.task_id',$1,TRUE),set_config('tianqiong.owner_user_id',$2,TRUE),set_config('tianqiong.execution_mode','human_constraint',TRUE)`, fmt.Sprint(task.ID), fmt.Sprint(foreignConstraintUser)); err != nil {
		t.Fatal(err)
	}
	if _, err = txConstraint.ExecContext(ctx, `UPDATE tianqiong_ai.task_constraints SET text='foreign' WHERE id=$1`, constraint.ID); err == nil {
		t.Fatal("foreign human constraint scope accepted")
	}
	_ = txConstraint.Rollback()
	if _, err = facade.Constraints(ctx, foreignConstraintUser, task.ID); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign constraint read: %v", err)
	}
	if err = facade.DeleteConstraint(ctx, foreignConstraintUser, task.ID, constraint.ID); err == nil {
		t.Fatal("foreign constraint deletion accepted")
	}
	if err = facade.DeleteConstraint(ctx, owner, task.ID, constraint.ID); err != nil {
		t.Fatal(err)
	}
	if err = facade.DeleteConstraint(ctx, owner, task.ID, constraint.ID); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("duplicate constraint delete: %v", err)
	}
	graph, err := store.ReadTaskGraph(ctx, owner, task.ID)
	if err != nil || len(graph.Nodes) != 2 {
		t.Fatalf("task root graph: %+v %v", graph, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO tianqiong_ai.exploration_nodes(exploration_id,kind,state) VALUES($1,'hint','active')`, task.ExplorationID); err == nil {
		t.Fatal("unfenced graph mutation was accepted")
	}
	if _, err := store.ControlTask(ctx, owner, task.ID, "start", false); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimTask(ctx, uuid.NewString())
	if err != nil || claimed == nil || claimed.ID != task.ID {
		t.Fatalf("task claim: %+v %v", claimed, err)
	}
	claimed, err = store.StampTaskStart(ctx, claimed)
	if err != nil || claimed.DeadlineAt == nil {
		t.Fatalf("task start: %+v %v", claimed, err)
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
	graphStore := (&artexdb.DB{DB: connection}).Exploration(task.ExplorationID)
	if _, err := graphStore.AddGoal(map[string]any{"text": "测试目标"}, "human"); err != nil {
		t.Fatal(err)
	}
	activityID, err := graphStore.AppendActivity(artexdb.Activity{Worker: "planner", Kind: "tool_use", Tool: "fixture", ToolUseID: "reused", Detail: "old input"})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := facade.ActivityDetail(ctx, owner, task.ID, activityID)
	if err != nil || detail != "old input" {
		t.Fatalf("task activity detail: %q %v", detail, err)
	}
	var foreignUser int
	if err = db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&foreignUser); err != nil {
		t.Fatal(err)
	}
	if _, err = facade.ActivityDetail(ctx, foreignUser, task.ID, activityID); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign task activity: %v", err)
	}
	if _, err := store.ControlTask(ctx, owner, task.ID, "pause", false); err != nil {
		t.Fatal(err)
	}
	if _, err := graphStore.AddGoal(map[string]any{"text": "late"}, "worker"); err == nil {
		t.Fatal("paused task accepted a late graph write")
	}
	writer := registryapp.NewRegistrationFacade(registryrepo.NewAssetRegistrationStore(orm, assetRegistrationCreators(orm)), nil)
	late, err := writer.Register(ctx, registryapp.BatchRegisterInput{UserID: owner, Origin: "ai", RequestID: "late-task-asset", Task: &registryapp.RegistrationTaskScope{
		TaskID: task.ID, ExplorationID: task.ExplorationID, ExecutionID: claimed.ActiveRun,
	}, Assets: []registryapp.AssetInput{{Type: "service", TargetID: target, URL: "https://late.task-scope.example/"}}})
	if err != nil || len(late.Errors) != 1 || len(late.Results) != 0 {
		t.Fatalf("paused task asset registration: %+v %v", late, err)
	}
	var leaked int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM website WHERE target_id=$1 AND url='https://late.task-scope.example/'`, target).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("late projected asset was not rolled back: %d %v", leaked, err)
	}
	if _, err := store.ControlTask(ctx, owner, task.ID, "resume", false); err != nil {
		t.Fatal(err)
	}
	second, err := store.ClaimTask(ctx, uuid.NewString())
	if err != nil || second == nil || second.ActiveRun == claimed.ActiveRun {
		t.Fatalf("new execution token: %+v %v", second, err)
	}
	second, err = store.StampTaskStart(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := graphStore.AddGoal(map[string]any{"text": "stale lease"}, "worker"); err == nil {
		t.Fatal("old run token accepted a graph write after reassignment")
	}
	newConnection, err := connector.OpenForExecution(ctx, taskExecution(second))
	if err != nil {
		t.Fatal(err)
	}
	defer newConnection.Close()
	newGraph := (&artexdb.DB{DB: newConnection}).Exploration(task.ExplorationID)
	if _, err := newGraph.AppendActivity(artexdb.Activity{Worker: "planner", Kind: "tool_result", Tool: "fixture", ToolUseID: "reused", Detail: "other run output"}); err != nil {
		t.Fatal(err)
	}
	oldCalls, err := facade.ToolExecutions(ctx, owner, "fixture", 0)
	if err != nil || len(oldCalls) != 1 || oldCalls[0].Output != "" || oldCalls[0].Status != "unknown" {
		t.Fatalf("paired tool result across runs: %+v %v", oldCalls, err)
	}
	var newlyAnchored int64
	var newWebsite int64
	if err := db.QueryRowContext(ctx, `INSERT INTO website(target_id,url) VALUES($1,'https://new.task-scope.example/') RETURNING id`, target).Scan(&newWebsite); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT asset_id FROM asset_website_binding WHERE website_id=$1`, newWebsite).Scan(&newlyAnchored); err != nil {
		t.Fatal(err)
	}
	if _, err := newGraph.AddIntent(map[string]any{"summary": "核验资产"}, 5, []int64{asset, newlyAnchored}, "planner"); err != nil {
		t.Fatal(err)
	}
	linked, err := facade.Assets(ctx, owner, task.ID, 0)
	if err != nil || len(linked) != 2 || linked[1].ID != newlyAnchored || linked[1].Source != "anchored" {
		t.Fatalf("graph anchor was not linked to canonical task asset: %+v %v", linked, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE target SET deleted_at=CURRENT_TIMESTAMP WHERE id=$1`, target); err != nil {
		t.Fatal(err)
	}
	paused, err := store.GetTask(ctx, owner, task.ID)
	if err != nil || paused.Status != "paused" {
		t.Fatalf("deleted target task: %+v %v", paused, err)
	}
	if _, err := newGraph.AddGoal(map[string]any{"text": "deleted target"}, "worker"); err == nil {
		t.Fatal("inactive target accepted a graph write")
	}
	if _, err := store.ControlTask(ctx, owner, task.ID, "resume", false); err == nil {
		t.Fatal("task resumed against an inactive target")
	}
	var other int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetTask(ctx, other, task.ID); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("task read across accounts: %v", err)
	}
}

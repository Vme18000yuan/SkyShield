package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/auth"
	serverdatabase "github.com/yyhuni/lunafox/server/internal/database"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	approval "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/application"
	approvalhandler "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/handler"
	approvalrepo "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/repository"
	approvalrouter "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/router"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestARTEXApprovalTaskContextIsolatedPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv(emptyPostgresMigrationTestDSNEnv))
	if dsn == "" {
		t.Skip(emptyPostgresMigrationTestDSNEnv + " is not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var database string
	if err = db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&database); err != nil || !strings.HasPrefix(database, "lunafox_migration_test_") {
		t.Fatalf("refusing non-test database %q: %v", database, err)
	}
	if countPublicTables(t, ctx, db, false) != 0 {
		t.Fatal("test database is not empty")
	}
	previousFS, previousPath := serverdatabase.MigrationsFS, serverdatabase.MigrationsPath
	serverdatabase.MigrationsFS, serverdatabase.MigrationsPath = migrationsFS, "migrations"
	defer func() { serverdatabase.MigrationsFS, serverdatabase.MigrationsPath = previousFS, previousPath }()
	if err = serverdatabase.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO auth_user(username,password,is_superuser) VALUES('registry-admin','unused',TRUE),('ai-other','unused',FALSE)`); err != nil {
		t.Fatal(err)
	}
	owner, target, asset := taskFixtureScope(t, ctx, db, "approval-context.example")
	var other int
	if err = db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	tasks := airepo.NewTaskStore(orm)
	facade := aiapp.NewTaskFacade(tasks, nil, nil)
	task, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "审批上下文", Goal: "验证", TargetID: target, AssetIDs: []int64{asset}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = facade.SaveGoal(ctx, owner, task.ID, 0, "测试审批", ""); err != nil {
		t.Fatal(err)
	}
	claimed, err := tasks.ClaimTask(ctx, uuid.NewString())
	if err != nil || claimed == nil || claimed.ID != task.ID {
		t.Fatalf("claim task: %+v %v", claimed, err)
	}
	claimed, err = tasks.StampTaskStart(ctx, claimed)
	if err != nil {
		t.Fatal(err)
	}
	connector, err := airuntime.NewTaskGraphConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := connector.OpenForExecution(ctx, taskExecution(claimed))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	graph := (&artexdb.DB{DB: conn}).Exploration(task.ExplorationID)
	intent, err := graph.AddIntent(map[string]any{"summary": "目标意图"}, 5, []int64{asset}, "planner")
	if err != nil {
		t.Fatal(err)
	}
	otherIntent, err := graph.AddIntent(map[string]any{"summary": "其他意图"}, 5, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	appendActivity := func(node *int64, worker, kind, toolName, useID, summary string) int64 {
		id, e := graph.AppendActivity(artexdb.Activity{NodeID: node, Worker: worker, Kind: kind, Tool: toolName, ToolUseID: useID, Summary: summary, Detail: "detail:" + summary})
		if e != nil {
			t.Fatal(e)
		}
		return id
	}
	for i := 0; i < 6; i++ {
		appendActivity(&intent, "work#1", "text", "", "", fmt.Sprintf("before-%d", i))
	}
	appendActivity(&otherIntent, "work#2", "text", "", "", "foreign-session")
	focus := appendActivity(&intent, "work#1", "tool_use", "fixture_probe", "context-use", "focus command")
	appendActivity(&intent, "work#1", "tool_result", "fixture_probe", "context-use", "focus output")
	for i := 0; i < 6; i++ {
		appendActivity(&intent, "work#1", "text", "", "", fmt.Sprintf("after-%d", i))
	}
	approvalStore := approvalrepo.NewStore(orm)
	service := approval.NewService(approvalStore)
	entry, err := approvalStore.CreateApproval(ctx, approval.Call{UserID: owner, RunID: claimed.ActiveRun, TaskID: task.ID, IntentID: intent, AgentName: "worker", ToolName: "fixture_probe", ToolUseID: "context-use"}, approval.Rule{Name: "fixture", TimeoutSeconds: 60, TimeoutAction: "deny"}, "allow")
	if err != nil {
		t.Fatal(err)
	}
	source, err := service.Execution(ctx, owner, entry.ID)
	if err != nil || source.Seq != focus || source.Session != fmt.Sprintf("intent:%d", intent) {
		t.Fatalf("approval source: %+v %v", source, err)
	}
	page, err := service.ExecutionContext(ctx, owner, entry.ID, 0, 0, 2)
	if err != nil || len(page.Items) != 4 || !page.HasBefore || !page.HasAfter || page.Items[2].Seq != focus {
		t.Fatalf("initial context: %+v %v", page, err)
	}
	for _, item := range page.Items {
		if item.Summary == "foreign-session" {
			t.Fatal("context crossed intent boundary")
		}
	}
	older, err := service.ExecutionContext(ctx, owner, entry.ID, page.Items[0].Seq, 0, 2)
	if err != nil || len(older.Items) != 2 || older.Items[1].Seq >= page.Items[0].Seq {
		t.Fatalf("older context: %+v %v", older, err)
	}
	newer, err := service.ExecutionContext(ctx, owner, entry.ID, 0, page.Items[len(page.Items)-1].Seq, 2)
	if err != nil || len(newer.Items) != 2 || newer.Items[0].Seq <= page.Items[len(page.Items)-1].Seq {
		t.Fatalf("newer context: %+v %v", newer, err)
	}
	if _, err = service.ExecutionContext(ctx, other, entry.ID, 0, 0, 2); !errors.Is(err, approval.ErrNotFound) {
		t.Fatalf("foreign context read: %v", err)
	}
	if _, err = service.ExecutionContext(ctx, owner, entry.ID, 1, 1, 2); !errors.Is(err, approval.ErrInvalidInput) {
		t.Fatalf("ambiguous cursor accepted: %v", err)
	}
	if detail, e := facade.ActivityDetail(ctx, owner, task.ID, focus); e != nil || detail != "detail:focus command" {
		t.Fatalf("activity detail: %q %v", detail, e)
	}
	segment, otherSegment := 2, 1
	appendMain := func(seg *int, kind, toolName, useID, summary string) int64 {
		id, e := graph.AppendActivity(artexdb.Activity{Worker: "mainagent", Kind: kind, Tool: toolName, ToolUseID: useID, Summary: summary, Detail: "detail:" + summary, MainSeg: seg})
		if e != nil {
			t.Fatal(e)
		}
		return id
	}
	appendMain(&otherSegment, "human_message", "", "", "wrong segment")
	appendMain(&segment, "human_message", "", "", "main before")
	mainFocus := appendMain(&segment, "tool_use", "main_probe", "main-use", "main command")
	appendMain(&segment, "tool_result", "main_probe", "main-use", "main output")
	mainApproval, err := approvalStore.CreateApproval(ctx, approval.Call{UserID: owner, RunID: claimed.ActiveRun, TaskID: task.ID, AgentName: "mainagent", ToolName: "main_probe", ToolUseID: "main-use"}, approval.Rule{Name: "fixture", TimeoutSeconds: 60, TimeoutAction: "deny"}, "allow")
	if err != nil {
		t.Fatal(err)
	}
	mainSource, err := service.Execution(ctx, owner, mainApproval.ID)
	if err != nil || mainSource.Seq != mainFocus || mainSource.Session != "main:2" {
		t.Fatalf("main session source: %+v %v", mainSource, err)
	}
	mainPage, err := service.ExecutionContext(ctx, owner, mainApproval.ID, 0, 0, 10)
	if err != nil || len(mainPage.Items) != 3 || mainPage.Items[0].Kind != "user" {
		t.Fatalf("main session context: %+v %v", mainPage, err)
	}
	for _, item := range mainPage.Items {
		if item.Summary == "wrong segment" {
			t.Fatal("main context crossed segment boundary")
		}
	}
	appendActivity(nil, "planner", "text", "", "", "planner before")
	planFocus := appendActivity(nil, "planner", "tool_use", "plan_probe", "plan-use", "plan command")
	appendActivity(nil, "planner", "tool_result", "plan_probe", "plan-use", "plan output")
	planApproval, err := approvalStore.CreateApproval(ctx, approval.Call{UserID: owner, RunID: claimed.ActiveRun, TaskID: task.ID, AgentName: "planner", ToolName: "plan_probe", ToolUseID: "plan-use"}, approval.Rule{Name: "fixture", TimeoutSeconds: 60, TimeoutAction: "deny"}, "allow")
	if err != nil {
		t.Fatal(err)
	}
	planSource, err := service.Execution(ctx, owner, planApproval.ID)
	if err != nil || planSource.Seq != planFocus || planSource.Session != "plan" {
		t.Fatalf("planner source: %+v %v", planSource, err)
	}
	planPage, err := service.ExecutionContext(ctx, owner, planApproval.ID, 0, 0, 10)
	if err != nil || len(planPage.Items) != 3 {
		t.Fatalf("planner context: %+v %v", planPage, err)
	}
	jwt := auth.NewJWTManager("approval-context-fixture", time.Minute, time.Hour)
	token, _, _ := jwt.GenerateAccessToken(owner, "registry-admin", 0)
	foreignToken, _, _ := jwt.GenerateAccessToken(other, "ai-other", 0)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	routes := engine.Group("/v1")
	routes.Use(middleware.AuthMiddleware(jwt, fixtureTokenVersions{}))
	approvalrouter.Register(routes, approvalhandler.NewHandler(service))
	request := func(token, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	path := fmt.Sprintf("/v1/aiApprovals/%d/execution/context?size=2", entry.ID)
	if out := request(token, path); out.Code != http.StatusOK || out.Header().Get("Cache-Control") != "no-store" || !strings.Contains(out.Body.String(), "focus command") {
		t.Fatalf("context HTTP: %d %s", out.Code, out.Body.String())
	}
	if out := request(foreignToken, path); out.Code != http.StatusNotFound {
		t.Fatalf("foreign context HTTP: %d", out.Code)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_task_archive(task_id,state) VALUES($1,'archiving')`, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.ExecutionContext(ctx, owner, entry.ID, 0, 0, 2); !errors.Is(err, approval.ErrExecutionUnavailable) {
		t.Fatalf("archived context visible: %v", err)
	}
	if _, err = facade.ActivityDetail(ctx, owner, task.ID, focus); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("archived activity detail visible: %v", err)
	}
	if _, err = facade.Activities(ctx, owner, task.ID, 0); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("archived task activities visible: %v", err)
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM ai_task_archive WHERE task_id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE target SET deleted_at=clock_timestamp() WHERE id=$1`, target); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Execution(ctx, owner, entry.ID); !errors.Is(err, approval.ErrExecutionUnavailable) {
		t.Fatalf("deleted target execution visible: %v", err)
	}
	if _, err = facade.ActivityDetail(ctx, owner, task.ID, focus); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("deleted target detail visible: %v", err)
	}
	if _, err = facade.Activities(ctx, owner, task.ID, 0); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("deleted target activities visible: %v", err)
	}
}

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/auth"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	aihandler "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/handler"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	airouter "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/router"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyARTEXTaskFindingsPostgres(t *testing.T, ctx context.Context, db *sql.DB, dsn string) {
	t.Helper()
	owner, target, asset := taskFixtureScope(t, ctx, db, "task-findings.example")
	_, outsideTarget, _ := taskFixtureScope(t, ctx, db, "task-findings-outside.example")
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
	source, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "漏洞来源任务", Goal: "漏洞测试", TargetID: target, AssetIDs: []int64{asset}})
	if err != nil {
		t.Fatal(err)
	}
	run := uuid.NewString()
	if _, err = store.ClaimMainChat(ctx, owner, source.ID, run); err != nil {
		t.Fatal(err)
	}
	connector, err := airuntime.NewTaskGraphConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	execution := airuntime.TaskExecution{ID: source.ID, ExplorationID: source.ExplorationID, TenantID: source.TenantID, UserID: owner, RunID: run, TargetID: target, ExecutionMode: "main_chat"}
	connection, err := connector.OpenForExecution(ctx, execution)
	if err != nil {
		t.Fatal(err)
	}
	graph := (&artexdb.DB{DB: connection}).Exploration(source.ExplorationID)
	intent, err := graph.AddIntent(map[string]any{"summary": "检查目标"}, 5, []int64{asset}, "mainagent")
	if err != nil {
		t.Fatal(err)
	}
	var firstID int64
	for i := 0; i < 31; i++ {
		severity := "high"
		if i%2 == 0 {
			severity = "medium"
		}
		result, e := graph.RecordFinding(ctx, artexdb.RecordFindingInput{TaskID: source.ID, IntentID: intent, VulnClass: "fixture", Name: fmt.Sprintf("漏洞 %02d", i), Severity: severity, Summary: fmt.Sprintf("发现 %02d", i), Evidence: fmt.Sprintf("private-evidence-%02d", i), Worker: "mainagent", AssetIDs: []int64{asset}})
		if e != nil {
			t.Fatal(e)
		}
		if i == 0 {
			firstID = result.FindingID
		}
	}
	connection.Close()
	if err = store.ReleaseMainChat(ctx, owner, source.ID, run); err != nil {
		t.Fatal(err)
	}
	child, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "继承任务", Goal: "查看来源", TargetID: target, SourceTaskIDs: []int64{source.ID}})
	if err != nil {
		t.Fatal(err)
	}
	outside, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "其他目标", Goal: "隔离", TargetID: outsideTarget, SourceTaskIDs: []int64{source.ID}})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for page := 0; page < 4; page++ {
		result, e := facade.Findings(ctx, owner, child.ID, aiapp.TaskFindingFilter{Page: page, Size: 10}, false)
		if e != nil || result.Total != 31 {
			t.Fatalf("finding page %d: %+v %v", page, result, e)
		}
		for _, item := range result.Items {
			if seen[item.ID] || !item.Inherited || item.TaskID != source.ID || item.TaskName != "漏洞来源任务" || item.Evidence != "" || len(item.Assets) != 1 {
				t.Fatalf("finding listing: %+v", item)
			}
			seen[item.ID] = true
		}
	}
	if len(seen) != 31 {
		t.Fatalf("finding pagination: %d", len(seen))
	}
	if own, err := facade.Findings(ctx, owner, child.ID, aiapp.TaskFindingFilter{Page: 0, Size: 25, OwnOnly: true}, false); err != nil || own.Total != 0 {
		t.Fatalf("inherited findings appeared as writable options: %+v %v", own, err)
	}
	filtered, err := facade.Findings(ctx, owner, child.ID, aiapp.TaskFindingFilter{Page: 0, Size: 25, Severity: "high", Query: "漏洞 01"}, false)
	if err != nil || filtered.Total != 1 || filtered.Items[0].Name != "漏洞 01" {
		t.Fatalf("finding filters: %+v %v", filtered, err)
	}
	if page, err := facade.Findings(ctx, owner, outside.ID, aiapp.TaskFindingFilter{Page: 0, Size: 25}, false); err != nil || page.Total != 0 {
		t.Fatalf("finding crossed selected target: %+v %v", page, err)
	}
	if _, err = facade.Findings(ctx, other, source.ID, aiapp.TaskFindingFilter{Page: 0, Size: 25}, false); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign finding list: %v", err)
	}
	detail, err := facade.Findings(ctx, owner, child.ID, aiapp.TaskFindingFilter{ID: firstID, Page: 0, Size: 1}, true)
	if err != nil || len(detail.Items) != 1 || !strings.Contains(detail.Items[0].Evidence, "private-evidence-00") {
		t.Fatalf("finding detail: %+v %v", detail, err)
	}
	if err = facade.SetFindingStatus(ctx, owner, child.ID, firstID, "confirmed"); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("inherited finding mutated: %v", err)
	}
	if err = facade.SetFindingStatus(ctx, other, source.ID, firstID, "confirmed"); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign finding mutated: %v", err)
	}
	if err = facade.SetFindingStatus(ctx, owner, source.ID, firstID, "invalid-status"); !errors.Is(err, aiapp.ErrInvalidInput) {
		t.Fatalf("invalid finding status: %v", err)
	}
	if err = facade.SetFindingStatus(ctx, owner, source.ID, firstID, "confirmed"); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = db.QueryRowContext(ctx, `SELECT status FROM tianqiong_ai.findings WHERE id=$1`, firstID).Scan(&status); err != nil || status != "confirmed" {
		t.Fatalf("finding status: %q %v", status, err)
	}
	locked, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.Rollback()
	var lockedID int64
	if err = locked.QueryRowContext(ctx, `SELECT id FROM tianqiong_ai.findings WHERE id=$1 FOR UPDATE`, firstID).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}
	blocked := make(chan error, 1)
	go func() {
		checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		blocked <- facade.SetFindingStatus(checkCtx, owner, source.ID, firstID, "fixed")
	}()
	select {
	case err = <-blocked:
		if !errors.Is(err, aiapp.ErrTaskControlConflict) {
			t.Fatalf("locked finding status: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("finding status waited on conflicting lock")
	}
	_ = locked.Rollback()
	if _, err = db.ExecContext(ctx, `UPDATE tianqiong_ai.findings SET status='fixed' WHERE id=$1`, firstID); err == nil {
		t.Fatal("unfenced finding status accepted")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `SELECT set_config('tianqiong.task_id',$1,TRUE),set_config('tianqiong.owner_user_id',$2,TRUE),set_config('tianqiong.execution_mode','human_finding_status',TRUE),set_config('tianqiong.finding_id',$3,TRUE)`, fmt.Sprint(source.ID), fmt.Sprint(owner), fmt.Sprint(firstID)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE tianqiong_ai.findings SET status='fixed',summary='forged' WHERE id=$1`, firstID); err == nil {
		t.Fatal("human status scope changed other fields")
	}
	_ = tx.Rollback()
	jwt := auth.NewJWTManager("task-findings-fixture", time.Minute, time.Hour)
	ownerToken, _, err := jwt.GenerateAccessToken(owner, "registry-admin", 0)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, _, err := jwt.GenerateAccessToken(other, "ai-other", 0)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/v1")
	group.Use(middleware.AuthMiddleware(jwt, fixtureTokenVersions{}))
	airouter.RegisterAITaskRoutes(group, aihandler.NewTaskHandler(facade))
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	path := fmt.Sprintf("/v1/aiTasks/%d/findings", child.ID)
	if out := request(http.MethodGet, path, ownerToken, ""); out.Code != http.StatusOK || out.Header().Get("Cache-Control") != "no-store" || strings.Contains(out.Body.String(), "private-evidence") {
		t.Fatalf("finding list HTTP: %d %s", out.Code, out.Body.String())
	}
	if out := request(http.MethodGet, fmt.Sprintf("%s/%d", path, firstID), ownerToken, ""); out.Code != http.StatusOK || !strings.Contains(out.Body.String(), "private-evidence-00") {
		t.Fatalf("finding detail HTTP: %d %s", out.Code, out.Body.String())
	}
	if out := request(http.MethodPatch, fmt.Sprintf("%s/%d/status", path, firstID), ownerToken, `{"status":"fixed"}`); out.Code != http.StatusNotFound {
		t.Fatalf("inherited finding status HTTP: %d", out.Code)
	}
	if out := request(http.MethodGet, path, otherToken, ""); out.Code != http.StatusNotFound {
		t.Fatalf("foreign finding HTTP: %d", out.Code)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_task_archive(task_id,state) VALUES($1,'archiving')`, source.ID); err != nil {
		t.Fatal(err)
	}
	if page, err := facade.Findings(ctx, owner, child.ID, aiapp.TaskFindingFilter{Page: 0, Size: 25}, false); err != nil || page.Total != 0 {
		t.Fatalf("archiving source exposed findings: %+v %v", page, err)
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM ai_task_archive WHERE task_id=$1`, source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE target SET deleted_at=clock_timestamp() WHERE id=$1`, target); err != nil {
		t.Fatal(err)
	}
	if out := request(http.MethodGet, path, ownerToken, ""); out.Code != http.StatusNotFound {
		t.Fatalf("deleted target finding HTTP: %d", out.Code)
	}
}

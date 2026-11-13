package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/auth"
	aiapprovalwiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/aiapproval"
	blacklistwiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/blacklist"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	approval "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/application"
	approvalhandler "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/handler"
	approvalrepo "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/repository"
	approvalrouter "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/router"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	registryapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	registryrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
	blacklistrepo "github.com/yyhuni/lunafox/server/internal/modules/blacklist/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyARTEXAssetInterceptPostgres(t *testing.T, ctx context.Context, db *sql.DB, dsn string) {
	t.Helper()
	owner, target, asset := taskFixtureScope(t, ctx, db, "asset-gate.example")
	var other int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	service := approval.NewService(approvalrepo.NewStore(orm))
	if err = service.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	rules, err := service.AssetRules(ctx, owner, 0)
	if err != nil || len(rules) != 4 {
		t.Fatalf("default asset intercept rules: %+v %v", rules, err)
	}
	var removed approval.AssetRule
	for _, rule := range rules {
		if rule.Pattern == ".edu.cn" {
			removed = rule
		}
	}
	if removed.ID == 0 || service.DeleteAssetRule(ctx, owner, 0, removed.ID) != nil {
		t.Fatal("delete default asset rule")
	}
	if err = service.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	rules, err = service.AssetRules(ctx, owner, 0)
	if err != nil || len(rules) != 3 {
		t.Fatalf("deleted default asset rule was reseeded: %+v %v", rules, err)
	}
	if _, err = service.SaveAssetRule(ctx, owner, 0, approval.AssetRule{Enabled: true, Kind: "fuzzy_domain", Pattern: removed.Pattern, Note: removed.Note}, true); err != nil {
		t.Fatal(err)
	}
	store := airepo.NewTaskStore(orm)
	facade := aiapp.NewTaskFacade(store, nil, nil)
	if _, err = facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "非法规则", InterceptRules: []approval.AssetRule{{Enabled: true, Action: "allow", Kind: "cidr", Pattern: "bad"}}}); !errors.Is(err, aiapp.ErrInvalidInput) {
		t.Fatalf("invalid creation rule: %v", err)
	}
	queuedFacade := aiapp.NewTaskFacade(store, func(context.Context, int) (llm.Provider, string, bool, error) {
		return &mentionModelFixture{}, "fixture", true, nil
	}, nil)
	queued, err := queuedFacade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "立即运行的任务规则", StartImmediately: true, InterceptRules: []approval.AssetRule{{Enabled: true, Action: "allow", Kind: "fuzzy_domain", Pattern: "asset-gate.example"}}})
	if err != nil || !queued.Queued {
		t.Fatalf("queued task with rules: %+v %v", queued, err)
	}
	queuedRules, err := service.AssetRules(ctx, owner, queued.ID)
	if err != nil || len(queuedRules) != 1 || queuedRules[0].Pattern != "asset-gate.example" {
		t.Fatalf("queued task was visible without its rules: %+v %v", queuedRules, err)
	}
	if _, err = store.ControlTask(ctx, owner, queued.ID, "pause", false); err != nil {
		t.Fatal(err)
	}
	task, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "资产拦截测试", TargetID: target, AssetIDs: []int64{asset}, InterceptRules: []approval.AssetRule{{Enabled: true, Action: "allow", Kind: "fuzzy_domain", Pattern: "asset-gate.example"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.AssetRules(ctx, other, task.ID); !errors.Is(err, approval.ErrNotFound) {
		t.Fatalf("foreign task asset rules: %v", err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_asset_intercept_rule(tenant_id,task_id,owner_user_id,action,kind,pattern) SELECT tenant_id,id,$1,'allow','fuzzy_domain','forged' FROM ai_task WHERE id=$2`, other, task.ID); err == nil {
		t.Fatal("asset rule accepted a different task owner")
	}
	manager := auth.NewJWTManager("asset-intercept-fixture", time.Minute, time.Hour)
	ownerToken, _, err := manager.GenerateAccessToken(owner, "registry-admin", 0)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, _, err := manager.GenerateAccessToken(other, "ai-other", 0)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/v1")
	group.Use(middleware.AuthMiddleware(manager, fixtureTokenVersions{}))
	approvalrouter.Register(group, approvalhandler.NewHandler(service))
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	taskPath := fmt.Sprintf("/v1/aiTasks/%d/assetInterceptRules", task.ID)
	if response := request(http.MethodGet, taskPath, "", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("asset rules without auth: %d", response.Code)
	}
	if response := request(http.MethodGet, taskPath, otherToken, ""); response.Code != http.StatusNotFound {
		t.Fatalf("asset rules across users: %d", response.Code)
	}
	if response := request(http.MethodPost, taskPath, ownerToken, `{"enabled":true,"action":"allow","kind":"cidr","pattern":"invalid"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid asset rule HTTP: %d %s", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, "/v1/aiAssetInterceptRules", ownerToken, ""); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"`) {
		t.Fatalf("asset rule IDs must be strings: %d %s", response.Code, response.Body.String())
	}
	if _, err = service.SaveAssetRule(ctx, owner, task.ID, approval.AssetRule{Enabled: true, Action: "allow", Kind: "exact_ip", Pattern: "not-an-ip"}, true); !errors.Is(err, approval.ErrInvalidInput) {
		t.Fatalf("invalid IP rule: %v", err)
	}
	taskRules, err := service.AssetRules(ctx, owner, task.ID)
	if err != nil || len(taskRules) != 1 || taskRules[0].Pattern != "asset-gate.example" {
		t.Fatalf("task creation did not persist rules: %+v %v", taskRules, err)
	}
	allow := taskRules[0]
	global, err := service.SaveAssetRule(ctx, owner, 0, approval.AssetRule{Enabled: true, Kind: "exact_domain", Pattern: "asset-gate.example"}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.DeleteAssetRule(context.Background(), owner, 0, global.ID) }()
	if _, err = store.ControlTask(ctx, owner, task.ID, "start", false); err != nil {
		t.Fatal(err)
	}
	run, err := store.ClaimTask(ctx, uuid.NewString())
	if err != nil || run == nil || run.ID != task.ID {
		t.Fatalf("claim asset gate task: %+v %v", run, err)
	}
	run, err = store.StampTaskStart(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.CheckAssetIDs(ctx, owner, task.ID, run.ActiveRun, []int64{asset}, target); err == nil || !strings.Contains(err.Error(), "asset-gate.example") {
		t.Fatalf("global block did not override task allow: %v", err)
	}
	if err = service.CheckAssetIDs(ctx, other, task.ID, run.ActiveRun, []int64{asset}, target); err == nil {
		t.Fatal("foreign task run passed asset gate")
	}
	if err = service.CheckAssetIDs(ctx, owner, task.ID, uuid.NewString(), []int64{asset}, target); !errors.Is(err, approval.ErrRunInactive) {
		t.Fatalf("stale task run passed asset gate: %v", err)
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
	tools := agent.NewToolSet(graph, "planner")
	tools.SetTaskID(task.ID)
	var addIntent tool.CoreTool
	for _, item := range tools.PlannerTools() {
		if item.Name() == "add_intent" {
			addIntent = item
		}
	}
	if addIntent == nil {
		t.Fatal("missing native add_intent")
	}
	toolCtx := agent.WithHostPolicy(ctx, agent.HostPolicy{CheckAssetIDs: func(ctx context.Context, ids []int64) error {
		return service.CheckAssetIDs(ctx, owner, task.ID, run.ActiveRun, ids, target)
	}})
	args := json.RawMessage(fmt.Sprintf(`{"summary":"核验资产","asset_ids":[%d]}`, asset))
	result, err := addIntent.Call(toolCtx, args, nil)
	if err != nil || !result.IsError || !strings.Contains(result.Flatten(), "资产拦截") {
		t.Fatalf("native intent bypassed block: %+v %v", result, err)
	}
	if err = service.ToggleAssetRule(ctx, owner, 0, global.ID, false); err != nil {
		t.Fatal(err)
	}
	var portRow int
	var ipAsset int64
	if err = db.QueryRowContext(ctx, `INSERT INTO host_port_mapping(target_id,host,ip,port) VALUES($1,'api.asset-gate.example','10.8.0.1',22) RETURNING id`, target).Scan(&portRow); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT asset_id FROM asset_host_port_binding WHERE host_port_id=$1`, portRow).Scan(&ipAsset); err != nil {
		t.Fatal(err)
	}
	if err = service.CheckAssetIDs(ctx, owner, task.ID, run.ActiveRun, []int64{ipAsset}, target); err != nil {
		t.Fatalf("bound hostname did not match allow rule: %v", err)
	}
	cidr, err := service.SaveAssetRule(ctx, owner, task.ID, approval.AssetRule{Enabled: true, Action: "block", Kind: "cidr", Pattern: "10.0.0.0/8"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.CheckAssetIDs(ctx, owner, task.ID, run.ActiveRun, []int64{ipAsset}, target); err == nil || !strings.Contains(err.Error(), "10.0.0.0/8") {
		t.Fatalf("CIDR rule did not block bound IP: %v", err)
	}
	if err = service.DeleteAssetRule(ctx, owner, task.ID, cidr.ID); err != nil {
		t.Fatal(err)
	}
	service.SetPlatformBlacklistResolver(func(_ context.Context, targetID int) ([]string, error) {
		if targetID == target {
			return []string{"*.blocked.asset-gate.example", "10.0.0.0/8"}, nil
		}
		return []string{"global-block.example"}, nil
	})
	if err = service.CheckAssetIDs(ctx, owner, task.ID, run.ActiveRun, []int64{ipAsset}, target); err == nil || !strings.Contains(err.Error(), "系统扫描黑名单") {
		t.Fatalf("target scanner blacklist did not block intent: %v", err)
	}
	platformInputs := []registryapp.AssetInput{{Type: "service", URL: "https://x.blocked.asset-gate.example/", TargetID: target}, {Type: "service", URL: "https://global-block.example/"}}
	platformReasons, err := service.CheckAssetInputs(ctx, owner, task.ID, run.ActiveRun, platformInputs)
	if err != nil || len(platformReasons) != 2 || !strings.Contains(platformReasons[0], "系统扫描黑名单") || !strings.Contains(platformReasons[1], "系统扫描黑名单") {
		t.Fatalf("scanner blacklist did not block asset registration: %+v %v", platformReasons, err)
	}
	service.SetPlatformBlacklistResolver(func(context.Context, int) ([]string, error) { return []string{"asset-gate.example"}, nil })
	if err = service.CheckAssetIDs(ctx, owner, task.ID, run.ActiveRun, []int64{asset}, target); err != nil {
		t.Fatalf("exact scanner blacklist unexpectedly blocked a subdomain: %v", err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO blacklist_policy(scope,target_id,patterns) VALUES('target',$1,'["*.blocked.asset-gate.example","10.0.0.0/8"]'::jsonb)`, target); err != nil {
		t.Fatal(err)
	}
	blacklistService, err := blacklistwiring.NewBlacklistPolicyApplicationService(blacklistwiring.NewBlacklistPolicyStoreAdapter(blacklistrepo.NewPolicyRepository(orm)))
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := aiapprovalwiring.NewPlatformBlacklistResolver(blacklistService)
	if err != nil {
		t.Fatal(err)
	}
	service.SetPlatformBlacklistResolver(resolver)
	if err = service.CheckAssetIDs(ctx, owner, task.ID, run.ActiveRun, []int64{ipAsset}, target); err == nil || !strings.Contains(err.Error(), "系统扫描黑名单") {
		t.Fatalf("persisted target scanner blacklist was not applied: %v", err)
	}
	service.SetPlatformBlacklistResolver(nil)
	result, err = addIntent.Call(toolCtx, args, nil)
	if err != nil || result.IsError || !strings.Contains(result.Flatten(), "intent created") {
		t.Fatalf("allowed native intent rejected: %+v %v", result, err)
	}
	inputs := []registryapp.AssetInput{{Type: "service", URL: "https://allowed.asset-gate.example/", TargetID: target}, {Type: "service", URL: "https://agency.gov.cn/", TargetID: target}, {Type: "service", URL: "https://outside.example/", TargetID: target}}
	reasons, err := service.CheckAssetInputs(ctx, owner, task.ID, run.ActiveRun, inputs)
	if err != nil || len(reasons) != 3 || reasons[0] != "" || !strings.Contains(reasons[1], ".gov") || !strings.Contains(reasons[2], "不在任务允许") {
		t.Fatalf("asset gate precedence: %+v %v", reasons, err)
	}
	writer := registryapp.NewRegistrationFacade(registryrepo.NewAssetRegistrationStore(orm, assetRegistrationCreators(orm)), airuntime.SecretBox{})
	writer.SetAIAssetGate(func(ctx context.Context, user int, scope *registryapp.RegistrationTaskScope, request string, inputs []registryapp.AssetInput) ([]string, error) {
		return service.CheckAssetInputs(ctx, user, scope.TaskID, scope.ExecutionID, inputs)
	})
	registered, err := writer.Register(ctx, registryapp.BatchRegisterInput{UserID: owner, Origin: "ai", RequestID: run.ActiveRun + "/gate-fixture", Assets: inputs, Task: &registryapp.RegistrationTaskScope{TaskID: task.ID, ExplorationID: task.ExplorationID, ExecutionID: run.ActiveRun}})
	if err != nil || len(registered.Results) != 1 || registered.Results[0].Index != 0 || len(registered.Errors) != 2 || registered.Errors[0].Index != 1 || registered.Errors[1].Index != 2 {
		t.Fatalf("partial asset gate registration: %+v %v", registered, err)
	}
	if err = service.ToggleAssetRule(ctx, owner, task.ID, allow.ID, false); err != nil {
		t.Fatal(err)
	}
	reasons, err = service.CheckAssetInputs(ctx, owner, task.ID, run.ActiveRun, inputs[2:])
	if err != nil || len(reasons) != 1 || reasons[0] != "" {
		t.Fatalf("disabled allow list remained active: %+v %v", reasons, err)
	}
	if err = store.FinishTask(ctx, run, "done", ""); err != nil {
		t.Fatal(err)
	}
}

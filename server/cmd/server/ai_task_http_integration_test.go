package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/auth"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	aihandler "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/handler"
	airouter "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/router"
)

func verifyAITaskHTTPContract(t *testing.T, db *sql.DB, facade *aiapp.TaskFacade, owner int, taskID, callID int64) {
	t.Helper()
	var other int
	if err := db.QueryRow(`SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	manager := auth.NewJWTManager("fixture-jwt-secret", time.Minute, time.Hour)
	token, _, err := manager.GenerateAccessToken(owner, "registry-admin", 0)
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
	airouter.RegisterAITaskRoutes(group, aihandler.NewTaskHandler(facade))
	request := func(path, authToken string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if authToken != "" {
			req.Header.Set("Authorization", "Bearer "+authToken)
		}
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	post := func(path, authToken, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if authToken != "" {
			req.Header.Set("Authorization", "Bearer "+authToken)
		}
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	graph := "/v1/aiTasks/" + strconv.FormatInt(taskID, 10) + "/graph"
	if r := request(graph, ""); r.Code != http.StatusUnauthorized {
		t.Fatalf("task graph without auth: %d", r.Code)
	}
	if r := request(graph, otherToken); r.Code != http.StatusNotFound {
		t.Fatalf("task graph across users: %d", r.Code)
	}
	if r := request(graph, token); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "discovered.task-runtime.example") {
		t.Fatalf("task graph asset: %d %s", r.Code, r.Body.String())
	}
	assets := "/v1/aiTasks/" + strconv.FormatInt(taskID, 10) + "/assets"
	if r := request(assets, otherToken); r.Code != http.StatusNotFound {
		t.Fatalf("task assets across users: %d", r.Code)
	}
	if r := request(assets, token); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "discovered.task-runtime.example") || !strings.Contains(r.Body.String(), `"factCount":`) {
		t.Fatalf("task assets and confirmed fact counts: %d %s", r.Code, r.Body.String())
	}
	coverageGraph := "/v1/aiTasks/" + strconv.FormatInt(taskID, 10) + "/coverageGraph"
	if r := request(coverageGraph, otherToken); r.Code != http.StatusNotFound {
		t.Fatalf("coverage graph across users: %d", r.Code)
	}
	if r := request(coverageGraph, token); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"nodes"`) {
		t.Fatalf("coverage graph HTTP: %d %s", r.Code, r.Body.String())
	}
	var referencedAsset int64
	if err := db.QueryRow(`SELECT asset_id FROM ai_task_asset WHERE task_id=$1 ORDER BY asset_id LIMIT 1`, taskID).Scan(&referencedAsset); err != nil {
		t.Fatal(err)
	}
	refs := fmt.Sprintf("/v1/aiTasks/%d/assetRefs?asset_id=%d", taskID, referencedAsset)
	if r := request(refs, token); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"facts"`) {
		t.Fatalf("asset refs HTTP: %d %s", r.Code, r.Body.String())
	}
	message := "/v1/aiTasks/" + strconv.FormatInt(taskID, 10) + "/messages"
	if r := post(message, "", `{"message":"请报告进度"}`); r.Code != http.StatusUnauthorized {
		t.Fatalf("MainAgent chat without auth: %d", r.Code)
	}
	if r := post(message, otherToken, `{"message":"请报告进度"}`); r.Code != http.StatusNotFound {
		t.Fatalf("MainAgent chat across users: %d", r.Code)
	}
	if r := post(message, token, `{"message":"请报告进度"}`); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "MainAgent") {
		t.Fatalf("MainAgent chat on completed task: %d %s", r.Code, r.Body.String())
	}
	mainChat := "/v1/aiTasks/" + strconv.FormatInt(taskID, 10) + "/mainChat"
	if r := request(mainChat, ""); r.Code != http.StatusUnauthorized {
		t.Fatalf("main chat status without auth: %d", r.Code)
	}
	if r := request(mainChat, otherToken); r.Code != http.StatusNotFound {
		t.Fatalf("main chat status across users: %d", r.Code)
	}
	if r := post(mainChat, otherToken, `{"message":"后台消息"}`); r.Code != http.StatusNotFound {
		t.Fatalf("main chat send across users: %d", r.Code)
	}
	if r := post(mainChat, token, `{"message":"后台消息","segment":99}`); r.Code != http.StatusNotFound {
		t.Fatalf("main chat unknown segment: %d", r.Code)
	}
	if r := post(mainChat, token, `{"message":"后台消息","segment":0}`); r.Code != http.StatusAccepted {
		t.Fatalf("main chat accepted: %d %s", r.Code, r.Body.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r := request(mainChat, token)
		if r.Code != http.StatusOK {
			t.Fatalf("main chat status: %d %s", r.Code, r.Body.String())
		}
		if strings.Contains(r.Body.String(), `"running":false`) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if r := post(mainChat+"/stop", otherToken, `{}`); r.Code != http.StatusNotFound {
		t.Fatalf("main chat stop across users: %d", r.Code)
	}
	sessions := "/v1/aiTasks/" + strconv.FormatInt(taskID, 10) + "/mainSessions"
	if r := request(sessions, otherToken); r.Code != http.StatusNotFound {
		t.Fatalf("main sessions across users: %d %s", r.Code, r.Body.String())
	}
	if r := request(sessions, token); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"current":1`) {
		t.Fatalf("main sessions: %d %s", r.Code, r.Body.String())
	}
	if r := request(sessions+"/0/activities", otherToken); r.Code != http.StatusNotFound {
		t.Fatalf("main transcript across users: %d", r.Code)
	}
	if r := request(sessions+"/0/activities", token); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"activities"`) {
		t.Fatalf("main transcript HTTP: %d %s", r.Code, r.Body.String())
	}
	if r := request(sessions+"/1/messages", token); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "新会话独立消息") {
		t.Fatalf("segment messages: %d %s", r.Code, r.Body.String())
	}
	if r := request(sessions+"/99/messages", token); r.Code != http.StatusNotFound {
		t.Fatalf("unknown segment messages: %d %s", r.Code, r.Body.String())
	}
	if r := post(sessions, otherToken, `{}`); r.Code != http.StatusNotFound {
		t.Fatalf("create main session across users: %d %s", r.Code, r.Body.String())
	}
	if r := post(sessions, token, `{}`); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"seq":2`) {
		t.Fatalf("create main session: %d %s", r.Code, r.Body.String())
	}
	rerun := "/v1/aiTasks/" + strconv.FormatInt(taskID, 10) + "/intents/999999999/rerun"
	if r := post(rerun, "", `{}`); r.Code != http.StatusUnauthorized {
		t.Fatalf("intent rerun without auth: %d", r.Code)
	}
	if r := post(rerun, otherToken, `{}`); r.Code != http.StatusNotFound {
		t.Fatalf("intent rerun across users: %d", r.Code)
	}
	if r := post(rerun, token, `{}`); r.Code != http.StatusConflict {
		t.Fatalf("intent rerun state conflict: %d %s", r.Code, r.Body.String())
	}
	controlIntent := "/v1/aiTasks/" + strconv.FormatInt(taskID, 10) + "/intents/999999999/control"
	if r := post(controlIntent, "", `{"action":"pause"}`); r.Code != http.StatusUnauthorized {
		t.Fatalf("intent control without auth: %d", r.Code)
	}
	if r := post(controlIntent, otherToken, `{"action":"pause"}`); r.Code != http.StatusNotFound {
		t.Fatalf("intent control across users: %d", r.Code)
	}
	if r := post(controlIntent, token, `{"action":"invalid"}`); r.Code != http.StatusBadRequest {
		t.Fatalf("invalid intent action: %d %s", r.Code, r.Body.String())
	}
	if r := post("/v1/aiTasks", token, `{"displayName":"HTTP 规则创建 fixture","goal":"验证规则","startImmediately":false,"interceptRules":[{"enabled":true,"action":"allow","kind":"cidr","pattern":"invalid"}]}`); r.Code != http.StatusBadRequest {
		t.Fatalf("invalid task creation rule: %d %s", r.Code, r.Body.String())
	}
	if r := post("/v1/aiTasks", token, `{"displayName":"HTTP 规则创建 fixture","goal":"验证规则","startImmediately":false,"interceptRules":[{"enabled":true,"action":"allow","kind":"fuzzy_domain","pattern":"allowed.example"}]}`); r.Code != http.StatusCreated {
		t.Fatalf("task creation with rules: %d %s", r.Code, r.Body.String())
	}
	var createdRules int
	if err := db.QueryRow(`SELECT count(*) FROM ai_asset_intercept_rule r JOIN ai_task t ON t.id=r.task_id WHERE t.name='HTTP 规则创建 fixture' AND r.action='allow' AND r.pattern='allowed.example'`).Scan(&createdRules); err != nil || createdRules != 1 {
		t.Fatalf("HTTP task rules not saved atomically: %d %v", createdRules, err)
	}
	path := "/v1/aiTaskToolExecutions/" + strconv.FormatInt(callID, 10)
	if r := request(path, otherToken); r.Code != http.StatusNotFound {
		t.Fatalf("task tool call across users: %d", r.Code)
	}
	if r := request(path, token); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "insert_assets") {
		t.Fatalf("task tool detail: %d %s", r.Code, r.Body.String())
	}
}

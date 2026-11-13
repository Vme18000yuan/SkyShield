package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/auth"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	settingshandler "github.com/yyhuni/lunafox/server/internal/modules/aisettings/handler"
	settingsrepo "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	settingsrouter "github.com/yyhuni/lunafox/server/internal/modules/aisettings/router"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyAIAgentSettingPostgresContract(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var admin, analyst int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&admin); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&analyst); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	facade := settingsapp.NewAgentSettingFacade(settingsrepo.NewAgentSettingStore(orm))
	defaults, err := facade.List(ctx, admin)
	if err != nil || len(defaults) != 3 || defaults[0].RunSeconds != 1200 {
		t.Fatalf("agent defaults: %+v %v", defaults, err)
	}
	if _, err := facade.List(ctx, analyst); !errors.Is(err, settingsapp.ErrForbidden) {
		t.Fatalf("analyst listed settings: %v", err)
	}
	config := airuntime.DefaultAgentConfig("assistant")
	config.MaxTurns, config.RunSeconds, config.AdditionalPrompt = 18, 240, "fixture-assistant-instruction"
	if err := facade.Save(ctx, analyst, config); !errors.Is(err, settingsapp.ErrForbidden) {
		t.Fatalf("analyst modified settings: %v", err)
	}
	if err := facade.Save(ctx, admin, config); err != nil {
		t.Fatal(err)
	}
	resolved, err := facade.Resolve(ctx, analyst, "assistant")
	if err != nil || resolved != config {
		t.Fatalf("member runtime resolution: %+v %v", resolved, err)
	}
	for _, key := range []string{"planner", "worker"} {
		item := airuntime.DefaultAgentConfig(key)
		item.AdditionalPrompt = "fixture-" + key + "-instruction"
		if err := facade.Save(ctx, admin, item); err != nil {
			t.Fatal(err)
		}
	}
	toolSettings := settingsapp.NewToolSettingFacade(settingsrepo.NewToolSettingStore(orm))
	visibility := settingsapp.ToolVisibility{AgentKey: "planner", ToolKey: "search_platform_assets", Enabled: false}
	if err := toolSettings.Save(ctx, analyst, visibility); !errors.Is(err, settingsapp.ErrForbidden) {
		t.Fatalf("analyst modified tool permissions: %v", err)
	}
	if err := toolSettings.Save(ctx, admin, visibility); err != nil {
		t.Fatal(err)
	}
	planner, err := facade.Resolve(ctx, analyst, "planner")
	if err != nil || planner.SearchAssetsEnabled || !planner.RegisterAssetsEnabled {
		t.Fatalf("planner tool grants: %+v %v", planner, err)
	}
	worker, err := facade.Resolve(ctx, analyst, "worker")
	if err != nil || !worker.SearchAssetsEnabled {
		t.Fatalf("worker grants were modified: %+v %v", worker, err)
	}
	toolList, err := toolSettings.List(ctx, admin)
	if err != nil || len(toolList) != 6 {
		t.Fatalf("tool settings list: %+v %v", toolList, err)
	}
	manager := auth.NewJWTManager("fixture-jwt-secret", time.Minute, time.Hour)
	adminToken, _, err := manager.GenerateAccessToken(admin, "registry-admin", 0)
	if err != nil {
		t.Fatal(err)
	}
	analystToken, _, err := manager.GenerateAccessToken(analyst, "ai-other", 0)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/v1")
	group.Use(middleware.AuthMiddleware(manager, fixtureTokenVersions{}))
	settingsrouter.RegisterAIAgentSettingRoutes(group, settingshandler.NewAgentSettingHandler(facade))
	settingsrouter.RegisterAIToolSettingRoutes(group, settingshandler.NewToolSettingHandler(toolSettings))
	request := func(method, path, token string, payload any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(payload)
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	if r := request(http.MethodGet, "/v1/aiAgentSettings", "", nil); r.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous settings: %d", r.Code)
	}
	if r := request(http.MethodGet, "/v1/aiAgentSettings", analystToken, nil); r.Code != http.StatusForbidden {
		t.Fatalf("analyst settings: %d", r.Code)
	}
	if r := request(http.MethodGet, "/v1/aiAgentSettings", adminToken, nil); r.Code != http.StatusOK {
		t.Fatalf("admin settings: %d %s", r.Code, r.Body.String())
	}
	if r := request(http.MethodPut, "/v1/aiAgentSettings/planner", adminToken, map[string]any{"enabled": true, "maxTurns": 12, "runSeconds": -1}); r.Code != http.StatusBadRequest {
		t.Fatalf("negative planner timeout accepted: %d %s", r.Code, r.Body.String())
	}
	if r := request(http.MethodPut, "/v1/aiToolSettings/planner/search_platform_assets", analystToken, map[string]any{"enabled": true}); r.Code != http.StatusForbidden {
		t.Fatalf("analyst changed tool grant: %d", r.Code)
	}
	if r := request(http.MethodGet, "/v1/aiToolSettings", adminToken, nil); r.Code != http.StatusOK {
		t.Fatalf("admin tool settings: %d", r.Code)
	}
}

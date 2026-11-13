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
	"github.com/yyhuni/lunafox/server/internal/auth"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	settingshandler "github.com/yyhuni/lunafox/server/internal/modules/aisettings/handler"
	settingsrepo "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	settingsrouter "github.com/yyhuni/lunafox/server/internal/modules/aisettings/router"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyAISkillPostgresContract(t *testing.T, ctx context.Context, db *sql.DB) {
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
	facade := settingsapp.NewSkillFacade(settingsrepo.NewSkillStore(orm))
	item := settingsapp.SkillRecord{Name: "fixture-skill", Description: "用于本地测试", Instructions: "fixture-skill-instruction", Enabled: true, Agents: []string{"assistant", "worker"}}
	if _, err := facade.Save(ctx, analyst, "", item); !errors.Is(err, settingsapp.ErrForbidden) {
		t.Fatalf("analyst created skill: %v", err)
	}
	saved, err := facade.Save(ctx, admin, "", item)
	if err != nil {
		t.Fatal(err)
	}
	items, err := facade.List(ctx, admin)
	if err != nil || len(items) != 1 || len(items[0].Agents) != 2 {
		t.Fatalf("skill list: %+v %v", items, err)
	}
	bound, err := facade.Resolve(ctx, analyst, "assistant")
	if err != nil || len(bound) != 1 || bound[0].Instructions != "fixture-skill-instruction" {
		t.Fatalf("assistant skill resolution: %+v %v", bound, err)
	}
	unbound, err := facade.Resolve(ctx, analyst, "planner")
	if err != nil || len(unbound) != 0 {
		t.Fatalf("unbound skill leaked: %+v %v", unbound, err)
	}
	if _, err := facade.Save(ctx, admin, "", item); !errors.Is(err, settingsapp.ErrProfileConflict) {
		t.Fatalf("duplicate skill accepted: %v", err)
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
	settingsrouter.RegisterAISkillRoutes(group, settingshandler.NewSkillHandler(facade))
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
	if r := request(http.MethodGet, "/v1/aiSkills", analystToken, nil); r.Code != http.StatusForbidden {
		t.Fatalf("analyst skill settings: %d", r.Code)
	}
	if r := request(http.MethodGet, "/v1/aiSkills", adminToken, nil); r.Code != http.StatusOK {
		t.Fatalf("admin skill settings: %d %s", r.Code, r.Body.String())
	}
	if r := request(http.MethodDelete, "/v1/aiSkills/"+saved.ID, analystToken, nil); r.Code != http.StatusForbidden {
		t.Fatalf("analyst deleted skill: %d", r.Code)
	}
}

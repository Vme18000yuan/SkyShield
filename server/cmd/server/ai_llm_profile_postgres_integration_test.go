package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/auth"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	settingshandler "github.com/yyhuni/lunafox/server/internal/modules/aisettings/handler"
	settingsinfra "github.com/yyhuni/lunafox/server/internal/modules/aisettings/infrastructure"
	settingsrepo "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	settingsrouter "github.com/yyhuni/lunafox/server/internal/modules/aisettings/router"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyAILLMProfilePostgresContract(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	t.Setenv("TIANQIONG_AI_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("z", 32))))
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
	store := settingsrepo.NewLLMProfileStore(orm)
	box := airuntime.SecretBox{}
	facade := settingsapp.NewLLMProfileFacade(store, box)
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer fixture.Close()
	input := settingsapp.ProfileInput{Name: "模型配置", Provider: "openai", Model: "fixture", BaseURL: fixture.URL, APIKey: "fixture-secret", IsDefault: true}
	if _, err := facade.Save(ctx, analyst, "", input); !errors.Is(err, settingsapp.ErrForbidden) {
		t.Fatalf("analyst was allowed to edit model settings: %v", err)
	}
	profile, err := facade.Save(ctx, admin, "", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(profile.EncryptedKey) != 0 || profile.ID == "" || profile.CreatedAt.IsZero() {
		t.Fatalf("profile response: %+v", profile)
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
	settingsrouter.RegisterAISettingsRoutes(group, settingshandler.NewLLMProfileHandler(facade))
	request := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/v1/aiLlmProfiles", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	if response := request(adminToken); response.Code != http.StatusOK || strings.Contains(response.Body.String(), "fixture-secret") || !strings.Contains(response.Body.String(), "模型配置") {
		t.Fatalf("admin profile response: %d %s", response.Code, response.Body.String())
	}
	if response := request(analystToken); response.Code != http.StatusForbidden {
		t.Fatalf("analyst profile response: %d", response.Code)
	}
	var encrypted []byte
	if err := db.QueryRowContext(ctx, `SELECT encrypted_api_key FROM ai_llm_profile WHERE id=$1`, profile.ID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte("fixture-secret")) {
		t.Fatal("API key stored in plaintext")
	}
	input.APIKey = ""
	input.Name = "模型配置更新"
	if _, err := facade.Save(ctx, admin, profile.ID, input); err != nil {
		t.Fatal(err)
	}
	var retained []byte
	if err := db.QueryRowContext(ctx, `SELECT encrypted_api_key FROM ai_llm_profile WHERE id=$1`, profile.ID).Scan(&retained); err != nil || !bytes.Equal(encrypted, retained) {
		t.Fatalf("empty update did not retain encrypted key: %v", err)
	}
	if _, err := facade.List(ctx, analyst); !errors.Is(err, settingsapp.ErrForbidden) {
		t.Fatalf("analyst listed model settings: %v", err)
	}
	if _, _, err := facade.Test(ctx, analyst, profile.ID); !errors.Is(err, settingsapp.ErrForbidden) {
		t.Fatalf("analyst tested model settings: %v", err)
	}
	_, reply, err := facade.Test(ctx, admin, profile.ID)
	if err != nil || reply != "OK" {
		t.Fatalf("model connectivity: reply %q, err %v", reply, err)
	}
	provider, model, configured, err := settingsinfra.NewLLMResolver(store, box).Resolve(ctx, analyst)
	if err != nil || !configured || provider == nil || model != "fixture" {
		t.Fatalf("resolve provider: %v %q %v %v", provider, model, configured, err)
	}
	if err := facade.Delete(ctx, admin, profile.ID); !errors.Is(err, settingsapp.ErrProfileActive) {
		t.Fatalf("active profile deletion: %v", err)
	}
	input.IsDefault = false
	if _, err := facade.Save(ctx, admin, profile.ID, input); err != nil {
		t.Fatal(err)
	}
	if err := facade.Delete(ctx, admin, profile.ID); err != nil {
		t.Fatal(err)
	}
}

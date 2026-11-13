package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/aitraffic"
	"github.com/yyhuni/lunafox/server/internal/auth"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	aihandler "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/handler"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	airouter "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/router"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyARTEXConversationTrafficPostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var owner, other int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := airepo.NewConversationStore(orm)
	conversation := uuid.NewString()
	if _, err = store.CreateConversation(ctx, owner, conversation, "对话流量测试"); err != nil {
		t.Fatal(err)
	}
	manager, err := aitraffic.NewManager(t.TempDir(), func(*http.Request) (*url.URL, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	lease, err := manager.Acquire(ctx, aitraffic.Scope{TenantID: assets.DefaultTenantID, UserID: owner, ConversationID: conversation})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "missing-traffic-fixture")
			return
		}
		_, _ = io.WriteString(w, "conversation-traffic-fixture")
	}))
	defer target.Close()
	proxy, _ := url.Parse(lease.ProxyURL())
	transport := &http.Transport{Proxy: http.ProxyURL(proxy)}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, err := client.Get(target.URL + "/conversation")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	client.CloseIdleConnections()
	records, err := lease.Recorder().Search("127.0.0.1", 0, 10)
	if err != nil || len(records) != 1 {
		t.Fatalf("conversation capture: %+v %v", records, err)
	}
	traffic := aiapp.NewConversationTrafficFacade(store, manager)
	page, err := traffic.List(ctx, owner, conversation, aiapp.TaskTrafficFilter{Page: 0, Size: 25})
	if err != nil || page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("conversation traffic: %+v %v", page, err)
	}
	if _, _, err = traffic.Exchange(ctx, other, conversation, records[0].ID); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("conversation traffic crossed account: %v", err)
	}
	otherConversation := uuid.NewString()
	if _, err = store.CreateConversation(ctx, owner, otherConversation, "另一对话"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = traffic.Exchange(ctx, owner, otherConversation, records[0].ID); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("traffic crossed conversation scope: %v", err)
	}
	missingResponse, err := client.Post(target.URL+"/missing", "text/plain", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	missingResponse.Body.Close()
	client.CloseIdleConnections()
	min, max := int64(3), int64(100)
	filtered, err := traffic.List(ctx, owner, conversation, aiapp.TaskTrafficFilter{Page: 0, Size: 25, Method: "POST", Path: "/missing", Status: "4xx", Body: "missing-traffic-fixture", RespMin: &min, RespMax: &max, Sort: "status", Order: "asc"})
	if err != nil || filtered.Total != 1 || len(filtered.Items) != 1 || filtered.Items[0].Status != 404 {
		t.Fatalf("advanced traffic filters: %+v %v", filtered, err)
	}
	if _, err = traffic.List(ctx, owner, conversation, aiapp.TaskTrafficFilter{Page: 0, Size: 25, RespMin: &max, RespMax: &min}); !errors.Is(err, aiapp.ErrInvalidInput) {
		t.Fatalf("invalid traffic size range: %v", err)
	}
	jwt := auth.NewJWTManager("conversation-traffic-fixture", time.Minute, time.Hour)
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
	handler := aihandler.NewConversationHandler(nil)
	handler.SetTraffic(traffic)
	airouter.RegisterAIWorkbenchRoutes(group, handler)
	path := fmt.Sprintf("/v1/aiConversations/%s/traffic", conversation)
	get := func(path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	if out := get(path, ""); out.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous conversation traffic: %d", out.Code)
	}
	if out := get(path, otherToken); out.Code != http.StatusNotFound {
		t.Fatalf("foreign conversation traffic: %d", out.Code)
	}
	if out := get(path, ownerToken); out.Code != http.StatusOK || out.Header().Get("Cache-Control") != "no-store" || !strings.Contains(out.Body.String(), "/conversation") {
		t.Fatalf("conversation traffic HTTP: %d %s", out.Code, out.Body.String())
	}
	if out := get(path+"/exchange?id="+url.QueryEscape(records[0].ID), ownerToken); out.Code != http.StatusOK || !strings.Contains(out.Body.String(), "conversation-traffic-fixture") {
		t.Fatalf("conversation exchange HTTP: %d %s", out.Code, out.Body.String())
	}
	if out := get(path+"?method=POST&path=%2Fmissing&status=4xx&body=missing-traffic-fixture&respMin=3&respMax=100&sort=status&order=asc", ownerToken); out.Code != http.StatusOK || !strings.Contains(out.Body.String(), "/missing") || strings.Contains(out.Body.String(), "/conversation") {
		t.Fatalf("advanced traffic filters HTTP: %d %s", out.Code, out.Body.String())
	}
	if out := get(path+"?sort=invalid", ownerToken); out.Code != http.StatusBadRequest {
		t.Fatalf("invalid traffic sort: %d", out.Code)
	}
	if _, err = store.DeleteOwnedConversations(ctx, owner, []string{conversation}); err != nil {
		t.Fatal(err)
	}
	if out := get(path, ownerToken); out.Code != http.StatusNotFound {
		t.Fatalf("deleted conversation exposed traffic: %d", out.Code)
	}
}

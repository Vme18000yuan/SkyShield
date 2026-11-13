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
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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

type revokingTrafficCatalog struct {
	delegate aiapp.TrafficScopeCatalog
	calls    atomic.Int32
}

func (c *revokingTrafficCatalog) VisibleTrafficScopes(ctx context.Context, user int, scopes []aitraffic.Scope) ([]aiapp.TrafficScope, error) {
	if c.calls.Add(1) > 1 {
		return []aiapp.TrafficScope{}, nil
	}
	return c.delegate.VisibleTrafficScopes(ctx, user, scopes)
}

func verifyARTEXGlobalTrafficPostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	owner, target1, _ := taskFixtureScope(t, ctx, db, "global-traffic-a.example")
	_, target2, _ := taskFixtureScope(t, ctx, db, "global-traffic-b.example")
	var other int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	taskStore := airepo.NewTaskStore(orm)
	tasks := aiapp.NewTaskFacade(taskStore, nil, nil)
	task1, err := tasks.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "任务甲", Goal: "流量甲", TargetID: target1})
	if err != nil {
		t.Fatal(err)
	}
	task2, err := tasks.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "任务乙", Goal: "流量乙", TargetID: target2})
	if err != nil {
		t.Fatal(err)
	}
	conversations := airepo.NewConversationStore(orm)
	conversation, foreignConversation := uuid.NewString(), uuid.NewString()
	if _, err = conversations.CreateConversation(ctx, owner, conversation, "对话甲"); err != nil {
		t.Fatal(err)
	}
	if _, err = conversations.CreateConversation(ctx, other, foreignConversation, "其他账号对话"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	manager, err := aitraffic.NewManager(root, func(*http.Request) (*url.URL, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("status") == "404" {
			w.WriteHeader(404)
		}
		_, _ = io.WriteString(w, "global-traffic-fixture")
	}))
	defer target.Close()
	record := func(scope aitraffic.Scope, prefix string, count int) {
		t.Helper()
		lease, e := manager.Acquire(ctx, scope)
		if e != nil {
			t.Fatal(e)
		}
		defer lease.Close()
		proxy, e := url.Parse(lease.ProxyURL())
		if e != nil {
			t.Fatal(e)
		}
		transport := &http.Transport{Proxy: http.ProxyURL(proxy)}
		client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
		defer client.CloseIdleConnections()
		for i := 0; i < count; i++ {
			code := "200"
			if i%2 == 1 {
				code = "404"
			}
			response, e := client.Get(fmt.Sprintf("%s/%s-%d?status=%s", target.URL, prefix, i, code))
			if e != nil {
				t.Fatal(e)
			}
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
	}
	scope1 := aitraffic.Scope{TenantID: assets.DefaultTenantID, UserID: owner, TaskID: task1.ID}
	scope2 := aitraffic.Scope{TenantID: assets.DefaultTenantID, UserID: owner, TaskID: task2.ID}
	chatScope := aitraffic.Scope{TenantID: assets.DefaultTenantID, UserID: owner, ConversationID: conversation}
	foreignScope := aitraffic.Scope{TenantID: assets.DefaultTenantID, UserID: other, ConversationID: foreignConversation}
	record(scope1, "task-a", 30)
	record(scope2, "task-b", 3)
	record(chatScope, "chat", 3)
	record(foreignScope, "foreign", 1)
	// An arbitrary symlink under the scope directory must never become a source.
	trafficDir := filepath.Join(root, assets.DefaultTenantID, fmt.Sprintf("user-%d", owner), "traffic")
	if err = os.Symlink(filepath.Join(trafficDir, fmt.Sprintf("task-%d", task1.ID)), filepath.Join(trafficDir, "task-999999")); err != nil {
		t.Fatal(err)
	}
	taskView := aiapp.NewTaskTrafficFacade(taskStore, manager)
	chatView := aiapp.NewConversationTrafficFacade(conversations, manager)
	global := aiapp.NewGlobalTrafficFacade(manager, taskStore, taskView, chatView)
	seen := map[string]bool{}
	lastStatus := 0
	for page := 0; page < 4; page++ {
		result, e := global.List(ctx, owner, aiapp.TaskTrafficFilter{Page: page, Size: 10, Sort: "status", Order: "asc"})
		if e != nil || result.Total != 36 {
			t.Fatalf("global traffic page %d: %+v %v", page, result, e)
		}
		for _, item := range result.Items {
			if item.Status < lastStatus {
				t.Fatalf("global traffic sort: %+v", item)
			}
			lastStatus = item.Status
			key := item.SourceType + ":" + item.SourceID + ":" + item.ID
			if seen[key] || strings.Contains(item.URL, "foreign") {
				t.Fatalf("duplicate or foreign traffic: %+v", item)
			}
			seen[key] = true
		}
	}
	if len(seen) != 36 {
		t.Fatalf("global pagination missed records: %d", len(seen))
	}
	hosts, err := global.Hosts(ctx, owner)
	if err != nil || len(hosts) != 1 || hosts[0].Host != "127.0.0.1" || hosts[0].Count != 36 {
		t.Fatalf("global host counts: %+v %v", hosts, err)
	}
	revoking := aiapp.NewGlobalTrafficFacade(manager, &revokingTrafficCatalog{delegate: taskStore}, taskView, chatView)
	if _, err = revoking.List(ctx, owner, aiapp.TaskTrafficFilter{Page: 0, Size: 10}); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("revoked traffic scope leaked during aggregation: %v", err)
	}
	revokingHosts := aiapp.NewGlobalTrafficFacade(manager, &revokingTrafficCatalog{delegate: taskStore}, taskView, chatView)
	if _, err = revokingHosts.Hosts(ctx, owner); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("revoked traffic scope leaked host counts: %v", err)
	}
	foreign, err := global.List(ctx, other, aiapp.TaskTrafficFilter{Page: 0, Size: 25})
	if err != nil || foreign.Total != 1 || len(foreign.Items) != 1 || foreign.Items[0].SourceName != "其他账号对话" {
		t.Fatalf("global account isolation: %+v %v", foreign, err)
	}
	jwt := auth.NewJWTManager("global-traffic-fixture", time.Minute, time.Hour)
	ownerToken, _, err := jwt.GenerateAccessToken(owner, "registry-admin", 0)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/v1")
	group.Use(middleware.AuthMiddleware(jwt, fixtureTokenVersions{}))
	handler := aihandler.NewTaskHandler(tasks)
	handler.SetGlobalTraffic(global)
	airouter.RegisterAITaskRoutes(group, handler)
	req := httptest.NewRequest(http.MethodGet, "/v1/aiTraffic?page=0&size=10&status=404", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken)
	out := httptest.NewRecorder()
	engine.ServeHTTP(out, req)
	if out.Code != http.StatusOK || out.Header().Get("Cache-Control") != "no-store" || !strings.Contains(out.Body.String(), "任务甲") {
		t.Fatalf("global traffic HTTP: %d %s", out.Code, out.Body.String())
	}
	if _, err = db.ExecContext(ctx, `UPDATE target SET deleted_at=clock_timestamp() WHERE id=$1`, target1); err != nil {
		t.Fatal(err)
	}
	remaining, err := global.List(ctx, owner, aiapp.TaskTrafficFilter{Page: 0, Size: 25})
	if err != nil || remaining.Total != 6 {
		t.Fatalf("deleted target remained in global traffic: %+v %v", remaining, err)
	}
	hosts, err = global.Hosts(ctx, owner)
	if err != nil || len(hosts) != 1 || hosts[0].Count != 6 {
		t.Fatalf("deleted target remained in host counts: %+v %v", hosts, err)
	}
	if _, err = conversations.DeleteOwnedConversations(ctx, owner, []string{conversation}); err != nil {
		t.Fatal(err)
	}
	remaining, err = global.List(ctx, owner, aiapp.TaskTrafficFilter{Page: 0, Size: 25})
	if err != nil || remaining.Total != 3 {
		t.Fatalf("deleted conversation remained in global traffic: %+v %v", remaining, err)
	}
}

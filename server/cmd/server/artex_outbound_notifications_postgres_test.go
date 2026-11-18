package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/notify"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/ainotification"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/auth"
	serverdatabase "github.com/yyhuni/lunafox/server/internal/database"
	"github.com/yyhuni/lunafox/server/internal/httpoutbound"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestARTEXOutboundNotificationsIsolatedPostgres(t *testing.T) {
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
		t.Fatal("isolated test database is not empty")
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
	owner, target, asset := taskFixtureScope(t, ctx, db, "outbound.example")
	var other int
	if err = db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TIANQIONG_AI_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{42}, 32)))
	var mu sync.Mutex
	var bodies []string
	var fail bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Host != "8.8.8.8" {
			t.Errorf("unexpected destination %q", r.URL.Host)
		}
		if r.Header.Get("Authorization") != "Bearer fixture-secret" {
			t.Error("missing webhook credential")
		}
		bodies = append(bodies, string(raw))
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer proxy.Close()
	if err = httpoutbound.DefaultProxyController.Set(true, proxy.URL); err != nil {
		t.Fatal(err)
	}
	defer httpoutbound.DefaultProxyController.UseEnvironment()
	notify.SetHTTPProxyResolver(httpoutbound.DefaultProxyController.Proxy)
	defer notify.SetHTTPProxyResolver(nil)
	store := ainotification.NewStore(orm, airuntime.SecretBox{})
	worker := ainotification.NewWorker(store)
	name, kind, mode := "fixture", "webhook", "realtime"
	enabled, rate := true, 0
	channel, err := store.SaveChannel(ctx, owner, 0, ainotification.ChannelInput{Name: &name, Kind: &kind, Mode: &mode, Enabled: &enabled, RatePerMin: &rate, Config: map[string]any{"url": "http://8.8.8.8/hook", "method": "POST", "headers": map[string]any{"Authorization": "Bearer fixture-secret"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !notify.IsMasked(channel.Config["url"].(string)) || !notify.IsMasked(channel.Config["headers"].(string)) {
		t.Fatalf("channel secrets not masked: %+v", channel.Config)
	}
	var cipher []byte
	if err = db.QueryRowContext(ctx, `SELECT config_cipher FROM ai_notification_channels WHERE id=$1`, channel.ID).Scan(&cipher); err != nil || bytes.Contains(cipher, []byte("fixture-secret")) || bytes.Contains(cipher, []byte("8.8.8.8")) {
		t.Fatalf("channel credentials not encrypted: %v", err)
	}
	if _, err = store.SaveChannel(ctx, other, channel.ID, ainotification.ChannelInput{Version: &channel.Version}); !errors.Is(err, ainotification.ErrNotFound) {
		t.Fatalf("foreign channel update: %v", err)
	}
	if _, err = store.SaveChannel(ctx, owner, channel.ID, ainotification.ChannelInput{Version: &channel.Version, Config: map[string]any{"url": "http://1.1.1.1/other", "headers": notify.MaskedPrefix}}); !errors.Is(err, ainotification.ErrInvalid) {
		t.Fatalf("destination change reused old credential: %v", err)
	}
	stale := channel.Version - 1
	if _, err = store.SaveChannel(ctx, owner, channel.ID, ainotification.ChannelInput{Version: &stale}); !errors.Is(err, ainotification.ErrConflict) {
		t.Fatalf("stale channel update: %v", err)
	}
	tasks := airepo.NewTaskStore(orm)
	facade := aiapp.NewTaskFacade(tasks, nil, nil)
	task, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "通知测试", Goal: "验证", TargetID: target, AssetIDs: []int64{asset}})
	if err != nil {
		t.Fatal(err)
	}
	run := uuid.NewString()
	if _, err = tasks.ClaimMainChat(ctx, owner, task.ID, run); err != nil {
		t.Fatal(err)
	}
	connector, err := airuntime.NewTaskGraphConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := connector.OpenForExecution(ctx, airuntime.TaskExecution{ID: task.ID, ExplorationID: task.ExplorationID, TenantID: task.TenantID, UserID: owner, RunID: run, TargetID: target, ExecutionMode: "main_chat"})
	if err != nil {
		t.Fatal(err)
	}
	graph := (&artexdb.DB{DB: conn}).Exploration(task.ExplorationID)
	intent, err := graph.AddIntent(map[string]any{"summary": "check"}, 5, []int64{asset}, "mainagent")
	if err != nil {
		t.Fatal(err)
	}
	finding, err := graph.RecordFinding(ctx, artexdb.RecordFindingInput{TaskID: task.ID, IntentID: intent, VulnClass: "fixture", Name: "finding one", Severity: "high", Summary: "visible summary", Evidence: "private evidence", Worker: "mainagent", AssetIDs: []int64{asset}})
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if err = tasks.ReleaseMainChat(ctx, owner, task.ID, run); err != nil {
		t.Fatal(err)
	}
	if err = worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(bodies) != 1 || !strings.Contains(bodies[0], "finding one") || !strings.Contains(bodies[0], "visible summary") || !strings.Contains(bodies[0], "outbound.example") || strings.Contains(bodies[0], "private evidence") {
		t.Fatalf("first outbound body: %+v", bodies)
	}
	mu.Unlock()
	page, err := store.Deliveries(ctx, owner, 0, 25)
	if err != nil || page.Total != 1 || page.Items[0].State != "sent" {
		t.Fatalf("delivery history: %+v %v", page, err)
	}
	if foreign, e := store.Deliveries(ctx, other, 0, 25); e == nil && foreign.Total != 0 {
		t.Fatalf("foreign delivery history: %+v", foreign)
	}
	if err = facade.SetFindingStatus(ctx, owner, task.ID, finding.FindingID, "confirmed"); err != nil {
		t.Fatal(err)
	}
	if err = worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(bodies) != 1 {
		t.Fatalf("status change bypassed filter: %d", len(bodies))
	}
	mu.Unlock()
	filter := notify.Filter{OnStatusChange: true}
	channel, err = store.SaveChannel(ctx, owner, channel.ID, ainotification.ChannelInput{Version: &channel.Version, Filter: &filter})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	fail = true
	mu.Unlock()
	if err = facade.SetFindingStatus(ctx, owner, task.ID, finding.FindingID, "fixed"); err != nil {
		t.Fatal(err)
	}
	if err = worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	page, err = store.Deliveries(ctx, owner, 0, 25)
	if err != nil || page.Total != 2 || page.Items[0].State != "pending" || page.Items[0].Attempts != 1 {
		t.Fatalf("retryable delivery: %+v %v", page, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_notification_deliveries SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE id=$1`, page.Items[0].ID); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	fail = false
	mu.Unlock()
	if err = worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	page, err = store.Deliveries(ctx, owner, 0, 25)
	if err != nil || page.Items[0].State != "sent" || page.Items[0].Attempts != 2 {
		t.Fatalf("delivery retry: %+v %v", page, err)
	}
	stats, err := store.Statistics(ctx, owner)
	if err != nil || stats.Channels != 1 || stats.EnabledChannels != 1 || stats.SentToday != 2 {
		t.Fatalf("notification statistics: %+v %v", stats, err)
	}
	base, interval, pause := "https://portal.example", 1, false
	if err = store.PatchSettings(ctx, owner, ainotification.SettingsPatch{Enabled: &pause, PublicBaseURL: &base, DigestIntervalMin: &interval}); err != nil {
		t.Fatal(err)
	}
	if err = facade.SetFindingStatus(ctx, owner, task.ID, finding.FindingID, "ignored"); err != nil {
		t.Fatal(err)
	}
	if err = worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	pausedCount := len(bodies)
	mu.Unlock()
	if pausedCount != 3 {
		t.Fatalf("paused notifications sent: %d", pausedCount)
	}
	resume := true
	if err = store.PatchSettings(ctx, owner, ainotification.SettingsPatch{Enabled: &resume}); err != nil {
		t.Fatal(err)
	}
	if err = worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(bodies) != 4 || !strings.Contains(bodies[3], "https://portal.example/ai/tasks/") {
		t.Fatalf("resumed notification missing detail link: %+v", bodies)
	}
	mu.Unlock()
	digestName, digestMode := "digest", "digest"
	digest, err := store.SaveChannel(ctx, owner, 0, ainotification.ChannelInput{Name: &digestName, Kind: &kind, Mode: &digestMode, Enabled: &enabled, RatePerMin: &rate, Filter: &filter, Config: map[string]any{"url": "http://8.8.8.8/hook", "method": "POST", "headers": map[string]any{"Authorization": "Bearer fixture-secret"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = facade.SetFindingStatus(ctx, owner, task.ID, finding.FindingID, "pending"); err != nil {
		t.Fatal(err)
	}
	if err = worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(bodies) != 5 {
		t.Fatalf("digest was sent before its interval: %d", len(bodies))
	}
	mu.Unlock()
	if _, err = db.ExecContext(ctx, `UPDATE ai_notification_deliveries SET created_at=clock_timestamp()-interval '2 minutes' WHERE channel_id=$1 AND state='pending'`, digest.ID); err != nil {
		t.Fatal(err)
	}
	if err = worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(bodies) != 6 || !strings.Contains(bodies[5], `"batch": true`) && !strings.Contains(bodies[5], `"batch":true`) {
		t.Fatalf("digest not sent as a batch: %+v", bodies)
	}
	mu.Unlock()
	disable := false
	if _, err = store.SaveChannel(ctx, owner, digest.ID, ainotification.ChannelInput{Version: &digest.Version, Enabled: &disable}); err != nil {
		t.Fatal(err)
	}
	if err = facade.SetFindingStatus(ctx, owner, task.ID, finding.FindingID, "confirmed"); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- ainotification.NewWorker(store).RunOnce(ctx) }()
	}
	for i := 0; i < 2; i++ {
		if err = <-results; err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	if len(bodies) != 7 {
		t.Fatalf("concurrent workers duplicated a delivery: %d", len(bodies))
	}
	mu.Unlock()
	jwt := auth.NewJWTManager("notification-fixture", time.Minute, time.Hour)
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
	routes := engine.Group("/v1")
	routes.Use(middleware.AuthMiddleware(jwt, fixtureTokenVersions{}))
	ainotification.NewHandler(store).Register(routes)
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	if out := request(http.MethodGet, "/v1/aiNotification/meta", ownerToken, ""); out.Code != http.StatusOK || !strings.Contains(out.Body.String(), `"channels":2`) || out.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("notification metadata HTTP: %d %s", out.Code, out.Body.String())
	}
	if out := request(http.MethodGet, "/v1/aiNotification/channels", ownerToken, ""); out.Code != http.StatusOK || strings.Contains(out.Body.String(), "fixture-secret") || strings.Contains(out.Body.String(), "8.8.8.8") || !strings.Contains(out.Body.String(), "__masked__") {
		t.Fatalf("notification credentials exposed: %d %s", out.Code, out.Body.String())
	}
	path := fmt.Sprintf("/v1/aiNotification/channels/%d", channel.ID)
	if out := request(http.MethodPatch, path, otherToken, fmt.Sprintf(`{"version":%d,"enabled":false}`, channel.Version)); out.Code != http.StatusNotFound {
		t.Fatalf("foreign channel HTTP: %d", out.Code)
	}
	if out := request(http.MethodGet, fmt.Sprintf("/v1/aiNotification/deliveries?channel_id=%d&state=sent&page=0&size=25", channel.ID), ownerToken, ""); out.Code != http.StatusOK || !strings.Contains(out.Body.String(), "finding one") {
		t.Fatalf("filtered delivery HTTP: %d %s", out.Code, out.Body.String())
	}
	if out := request(http.MethodPost, path+"/test", ownerToken, ""); out.Code != http.StatusOK {
		t.Fatalf("test delivery HTTP: %d %s", out.Code, out.Body.String())
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_task_archive(task_id,state) VALUES($1,'archiving')`, task.ID); err != nil {
		t.Fatal(err)
	}
	if hidden, e := store.Deliveries(ctx, owner, 0, 25); e != nil || hidden.Total != 0 {
		t.Fatalf("archived task history visible: %+v %v", hidden, e)
	}
	verifyRegistryPush(t, ctx, db, store, owner, other, target)
}

// The native registry has no task identifier. A repeated chat report must not
// become another notification or disclose the chat to other channel owners.
func verifyRegistryPush(t *testing.T, ctx context.Context, db *sql.DB, store *ainotification.Store, owner, other, target int) {
	t.Helper()
	const tenant = "00000000-0000-0000-0000-000000000001"
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO ai_notification_channels(tenant_id,owner_user_id,name,kind,enabled,enabled_at,mode,config_cipher)
  VALUES ($1,$2,'other account','webhook',TRUE,clock_timestamp(),'realtime',decode('00','hex'))`, tenant, other)
	var vid int
	if err := db.QueryRowContext(ctx, `INSERT INTO vulnerability(target_id,url,vuln_type,severity,source) VALUES($1,'https://fixture.invalid/scan','fixture','high','nuclei') RETURNING id`, target).Scan(&vid); err != nil {
		t.Fatal(err)
	}
	chat, run := uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO ai_conversation(id,tenant_id,owner_user_id,title) VALUES($1,$2,$3,'push fixture')`, chat, tenant, owner)
	exec(`INSERT INTO ai_run(id,tenant_id,conversation_id,owner_user_id,status,lease_owner,lease_until) VALUES($1,$2,$3,$4,'completed',$5,clock_timestamp())`, run, tenant, chat, owner, uuid.NewString())
	for _, call := range []string{"first", "duplicate"} {
		exec(`INSERT INTO ai_conversation_vulnerability_report(tenant_id,owner_user_id,conversation_id,run_id,tool_use_id,input_hash,vulnerability_id,report,evidence)
   VALUES($1,$2,$3,$4,$5,repeat('0',64),$6,'{"name":"chat only","summary":"public summary"}','[{"text":"private evidence"}]')`, tenant, owner, chat, run, call, vid)
	}
	for i := 0; i < 10; i++ {
		processed, err := store.FanOutOne(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !processed {
			break
		}
	}
	for user, count := range map[int]int64{owner: 2, other: 1} {
		page, err := store.Deliveries(ctx, user, 0, 20)
		if err != nil || page.Total != count {
			t.Fatalf("user %d expected %d registered notifications: %+v %v", user, count, page, err)
		}
		for _, item := range page.Items {
			if item.VulnerabilityID != int64(vid) || item.TaskID != 0 || item.Title == "chat only" && user != owner {
				t.Fatalf("wrong source or audience: %+v", item)
			}
		}
	}
	// An info-gather risk needs both the registry row and the matching import
	// receipt. Neither its raw-output IDs nor another user's channel is enough.
	gather := uuid.NewString()
	exec(`INSERT INTO ai_info_gather_run(id,tenant_id,owner_user_id,execution_id,tool_use_id,target,target_kind,mode,status)
 VALUES($1,$2,$3,$4,'gather','fixture.invalid','HOST','fast','completed')`, gather, tenant, owner, uuid.NewString())
	exec(`INSERT INTO ai_info_gather_record(run_id,kind,record_id,payload) VALUES($1,'vulnerability',repeat('a',64),'{}')`, gather)
	exec(`INSERT INTO vulnerability(target_id,url,vuln_type,severity,source,raw_output) VALUES($1,'https://fixture.invalid/gather','fixture gather','info','info-gath',jsonb_build_object('runId',$2::text,'recordId',repeat('a',64)))`, target, gather)
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM ai_notification_events WHERE gather_run_id=$1`, gather).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unbound gather notification: %d %v", count, err)
	}
	exec(`UPDATE ai_info_gather_record SET status='imported',target_id=$2 WHERE run_id=$1`, gather, target)
	if _, err := store.FanOutOne(ctx); err != nil {
		t.Fatal(err)
	}
	ownPage, err := store.Deliveries(ctx, owner, 0, 20)
	if err != nil || ownPage.Total != 3 {
		t.Fatalf("owner gather notification: %+v %v", ownPage, err)
	}
	foreign, err := store.Deliveries(ctx, other, 0, 20)
	if err != nil || foreign.Total != 1 {
		t.Fatalf("gather notification disclosed: %+v %v", foreign, err)
	}
	exec(`DELETE FROM vulnerability WHERE id=$1`, vid)
	pageAfterDelete, err := store.Deliveries(ctx, owner, 0, 20)
	if err != nil || pageAfterDelete.Total != 1 {
		t.Fatalf("registry deletion must remove its scan and chat deliveries: %+v %v", pageAfterDelete, err)
	}
	exec(`UPDATE target SET deleted_at=clock_timestamp() WHERE id=$1`, target)
	page, err := store.Deliveries(ctx, owner, 0, 20)
	if err != nil || page.Total != 0 {
		t.Fatalf("deleted target still visible: %+v %v", page, err)
	}
}

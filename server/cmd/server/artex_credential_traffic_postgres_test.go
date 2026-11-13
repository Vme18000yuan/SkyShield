package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/aitraffic"
	"github.com/yyhuni/lunafox/server/internal/auth"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	settingsrepo "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	aihandler "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/handler"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	airouter "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/router"
	registryapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	registryrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyARTEXCredentialTrafficPostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	t.Setenv("TIANQIONG_AI_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{39}, 32)))
	t.Setenv("TIANQIONG_AI_ENCRYPTION_KEY_FILE", "")
	owner, assetTarget, assetID := taskFixtureScope(t, ctx, db, "credential-traffic.example")
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = settingsrepo.NewAgentCatalogStore(orm).SeedDefaultAgents(ctx); err != nil {
		t.Fatal(err)
	}
	settings := settingsapp.NewRuntimeSettingFacade(settingsrepo.NewToolCatalogStore(orm), airuntime.SecretBox{}, nil)
	if _, err = settings.Patch(ctx, owner, map[string]any{"task_intelligence_enabled": true, "credential_agent_enabled": true, "credential_traffic_binding": false}); err != nil {
		t.Fatal(err)
	}
	store := airepo.NewTaskStore(orm)
	tasks := aiapp.NewTaskFacade(store, nil, nil)
	task, err := tasks.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "凭据流量核对", AssetIDs: []int64{assetID}})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	manager, err := aitraffic.NewManager(root, func(*http.Request) (*url.URL, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	lease, err := manager.Acquire(ctx, aitraffic.Scope{TenantID: task.TenantID, UserID: owner, TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	var hits atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, "accepted")
	}))
	defer target.Close()
	proxy, err := url.Parse(lease.ProxyURL())
	if err != nil {
		t.Fatal(err)
	}
	cert, err := os.ReadFile(lease.CABundle())
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(cert) {
		t.Fatal("capture CA")
	}
	transport := &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, err := client.Post(target.URL+"/login", "application/json", strings.NewReader(`{"username":"alice","password":"s3cret-pass"}`))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	client.CloseIdleConnections()
	rows, err := lease.Recorder().Search("127.0.0.1", 0, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("credential capture: %+v %v", rows, err)
	}
	run := uuid.NewString()
	if _, err = store.ClaimMainChat(ctx, owner, task.ID, run); err != nil {
		t.Fatal(err)
	}
	intelligence := aiapp.NewIntelligenceFacade(airepo.NewTaskIntelligenceStore(orm), airuntime.SecretBox{})
	intelligence.SetAssetReader(registryapp.NewFacade(registryrepo.NewAssetRegistryStore(orm)))
	taskCtx := airuntime.WithTaskExecution(ctx, airuntime.TaskExecution{ID: task.ID, ExplorationID: task.ExplorationID, TenantID: task.TenantID, UserID: owner, RunID: run, ExecutionMode: "main_chat"})
	intelID, err := intelligence.Record(taskCtx, task.ID, 0, "mainagent", artexdb.IntelligenceInput{Kind: "credential", Title: "登录凭据", Content: `{"username":"alice","password":"s3cret-pass"}`, Evidence: "验证 " + target.URL + "/login", Confidence: "observed", AssetIDs: []int64{assetID}})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ReleaseMainChat(ctx, owner, task.ID, run); err != nil {
		t.Fatal(err)
	}
	jobs := airepo.NewCredentialJobStore(orm)
	processor := aiapp.NewCredentialProcessor(jobs, airuntime.SecretBox{}, nil)
	processor.SetTrafficMatcher(aiapp.NewScopedCredentialTrafficMatcher(jobs, manager))
	workerCtx, cancel := context.WithCancel(ctx)
	processor.Start(workerCtx)
	defer func() { cancel(); <-processor.Done() }()
	wait := func(state string) *artexdb.CredentialAnalysis {
		t.Helper()
		deadline := time.Now().Add(12 * time.Second)
		for time.Now().Before(deadline) {
			page, e := intelligence.List(ctx, owner, task.ID, aiapp.IntelligenceFilter{ID: intelID, Limit: 1})
			if e != nil {
				t.Fatal(e)
			}
			if len(page.Items) == 1 && page.Items[0].Credentials != nil && page.Items[0].Credentials.State == "ready" && page.Items[0].Credentials.TrafficState == state {
				return page.Items[0].Credentials
			}
			time.Sleep(30 * time.Millisecond)
		}
		t.Fatalf("credential traffic did not reach %s", state)
		return nil
	}
	off := wait("off")
	if len(off.Traffic) != 0 {
		t.Fatalf("traffic bound while disabled: %+v", off)
	}
	if _, err = settings.Patch(ctx, owner, map[string]any{"credential_traffic_binding": true}); err != nil {
		t.Fatal(err)
	}
	ready := wait("ready")
	if len(ready.Traffic) != 1 || ready.Traffic[0].ID != rows[0].ID || ready.Traffic[0].Side != "request" || !strings.Contains(ready.Traffic[0].Excerpt, "s3cret-pass") || hits.Load() != 1 {
		t.Fatalf("credential matching: %+v hits=%d", ready, hits.Load())
	}
	details := aiapp.NewCredentialTrafficDetailFacade(intelligence, store, manager)
	assetPage, err := intelligence.ListAsset(ctx, owner, assetID, aiapp.IntelligenceFilter{Limit: 25})
	if err != nil || assetPage.Total != 1 || assetPage.Items[0].ID != intelID || assetPage.Items[0].TaskName != task.Name {
		t.Fatalf("asset intelligence: %+v %v", assetPage, err)
	}
	assetDetail, err := details.DetailAsset(ctx, owner, assetID, intelID, rows[0].ID)
	if err != nil || assetDetail.Unavailable || !strings.Contains(assetDetail.Request, "s3cret-pass") {
		t.Fatalf("asset credential traffic: %+v %v", assetDetail, err)
	}
	detail, err := details.Detail(ctx, owner, task.ID, intelID, rows[0].ID)
	if err != nil || detail.Unavailable || !strings.Contains(detail.Request, "s3cret-pass") {
		t.Fatalf("credential traffic detail: %+v %v", detail, err)
	}
	if _, err = details.Detail(ctx, owner, task.ID, intelID, "not-linked"); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("unlinked credential traffic: %v", err)
	}
	child, err := tasks.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "继承凭据", SourceTaskIDs: []int64{task.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if inherited, err := details.Detail(ctx, owner, child.ID, intelID, rows[0].ID); err != nil || inherited.Unavailable {
		t.Fatalf("inherited credential traffic: %+v %v", inherited, err)
	}
	_, otherTarget, _ := taskFixtureScope(t, ctx, db, "credential-other-target.example")
	limited, err := tasks.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "目标隔离", TargetID: otherTarget, SourceTaskIDs: []int64{task.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = details.Detail(ctx, owner, limited.ID, intelID, rows[0].ID); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("cross-target inherited credential traffic: %v", err)
	}
	var ciphertext []byte
	var metadata string
	if err = db.QueryRowContext(ctx, `SELECT sealed_analysis,metadata::text FROM ai_credential_job WHERE intelligence_id=$1`, intelID).Scan(&ciphertext, &metadata); err != nil || bytes.Contains(ciphertext, []byte("s3cret-pass")) || strings.Contains(metadata, "s3cret-pass") {
		t.Fatalf("credential traffic excerpt not sealed: %v", err)
	}
	var other int
	if err = db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if _, err = intelligence.List(ctx, other, task.ID, aiapp.IntelligenceFilter{ID: intelID}); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("credential traffic cross-account: %v", err)
	}
	if _, err = details.Detail(ctx, other, task.ID, intelID, rows[0].ID); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("credential traffic detail cross-account: %v", err)
	}
	foreignPage, err := intelligence.ListAsset(ctx, other, assetID, aiapp.IntelligenceFilter{Limit: 25})
	if err != nil || foreignPage.Total != 0 {
		t.Fatalf("asset intelligence leaked across accounts: %+v %v", foreignPage, err)
	}
	if _, err = details.DetailAsset(ctx, other, assetID, intelID, rows[0].ID); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("asset credential traffic cross-account: %v", err)
	}
	// An asset can remain visible through a second target after its original
	// association is removed. Historical intelligence from a task limited to
	// the first target must then stop appearing under that asset.
	_, scopeTarget, scopeAsset := taskFixtureScope(t, ctx, db, "asset-intelligence-scope.example")
	var secondTarget int
	if err = db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES('asset-intelligence-second.example','domain') RETURNING id`).Scan(&secondTarget); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO website(target_id,url) VALUES($1,'https://api.asset-intelligence-scope.example/')`, secondTarget); err != nil {
		t.Fatal(err)
	}
	scopedTask, err := tasks.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "资产目标隔离", TargetID: scopeTarget, AssetIDs: []int64{scopeAsset}})
	if err != nil {
		t.Fatal(err)
	}
	scopedRun := uuid.NewString()
	if _, err = store.ClaimMainChat(ctx, owner, scopedTask.ID, scopedRun); err != nil {
		t.Fatal(err)
	}
	scopedCtx := airuntime.WithTaskExecution(ctx, airuntime.TaskExecution{ID: scopedTask.ID, ExplorationID: scopedTask.ExplorationID, TenantID: scopedTask.TenantID, UserID: owner, RunID: scopedRun, TargetID: scopeTarget, ExecutionMode: "main_chat"})
	if _, err = intelligence.Record(scopedCtx, scopedTask.ID, 0, "mainagent", artexdb.IntelligenceInput{Kind: "lead", Title: "限定目标线索", Content: "scoped", Evidence: "fixture", Confidence: "observed", AssetIDs: []int64{scopeAsset}}); err != nil {
		t.Fatal(err)
	}
	if err = store.ReleaseMainChat(ctx, owner, scopedTask.ID, scopedRun); err != nil {
		t.Fatal(err)
	}
	if page, e := intelligence.ListAsset(ctx, owner, scopeAsset, aiapp.IntelligenceFilter{}); e != nil || page.Total != 1 {
		t.Fatalf("scoped asset intelligence before removal: %+v %v", page, e)
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM website WHERE target_id=$1`, scopeTarget); err != nil {
		t.Fatal(err)
	}
	if page, e := intelligence.ListAsset(ctx, owner, scopeAsset, aiapp.IntelligenceFilter{}); e != nil || page.Total != 0 {
		t.Fatalf("intelligence escaped removed target association: %+v %v", page, e)
	}
	jwt := auth.NewJWTManager("credential-traffic-test", time.Minute, time.Hour)
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
	handler := aihandler.NewTaskHandler(tasks, intelligence)
	handler.SetCredentialTraffic(details)
	airouter.RegisterAITaskRoutes(group, handler)
	path := fmt.Sprintf("/v1/aiTasks/%d/intelligence/%d/traffic/%s", task.ID, intelID, rows[0].ID)
	get := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	if out := get(""); out.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous credential traffic: %d", out.Code)
	}
	if out := get(otherToken); out.Code != http.StatusNotFound {
		t.Fatalf("foreign credential traffic HTTP: %d", out.Code)
	}
	if out := get(ownerToken); out.Code != http.StatusOK || out.Header().Get("Cache-Control") != "no-store" || !strings.Contains(out.Body.String(), "s3cret-pass") {
		t.Fatalf("credential traffic HTTP: %d %s", out.Code, out.Body.String())
	}
	assetPath := fmt.Sprintf("/v1/aiAssets/%d/intelligence", assetID)
	assetGet := func(path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	if out := assetGet(assetPath, ownerToken); out.Code != http.StatusOK || !strings.Contains(out.Body.String(), "登录凭据") || out.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("asset intelligence HTTP: %d %s", out.Code, out.Body.String())
	}
	if out := assetGet(assetPath, otherToken); out.Code != http.StatusOK || strings.Contains(out.Body.String(), "登录凭据") {
		t.Fatalf("foreign asset intelligence HTTP: %d %s", out.Code, out.Body.String())
	}
	if out := assetGet(fmt.Sprintf("%s/%d/traffic/%s", assetPath, intelID, rows[0].ID), ownerToken); out.Code != http.StatusOK || !strings.Contains(out.Body.String(), "s3cret-pass") {
		t.Fatalf("asset credential traffic HTTP: %d %s", out.Code, out.Body.String())
	}
	if _, err = lease.Recorder().DeleteHost("127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	missing, err := details.Detail(ctx, owner, task.ID, intelID, rows[0].ID)
	if err != nil || !missing.Unavailable || !strings.Contains(missing.Reference.Excerpt, "s3cret-pass") {
		t.Fatalf("deleted credential traffic fallback: %+v %v", missing, err)
	}
	if out := get(ownerToken); out.Code != http.StatusOK || !strings.Contains(out.Body.String(), `"unavailable":true`) {
		t.Fatalf("deleted credential traffic HTTP: %d %s", out.Code, out.Body.String())
	}
	if err = processor.Retry(ctx, owner, task.ID, intelID); err != nil {
		t.Fatal(err)
	}
	preserved := wait("ready")
	if len(preserved.Traffic) != 1 || preserved.Traffic[0].ID != rows[0].ID {
		t.Fatalf("previous credential evidence lost: %+v", preserved)
	}
	cancel()
	<-processor.Done()
	if err = processor.Retry(ctx, owner, task.ID, intelID); err != nil {
		t.Fatal(err)
	}
	job, err := jobs.Claim(ctx)
	if err != nil || job == nil || job.IntelligenceID != intelID {
		t.Fatalf("credential lease: %+v %v", job, err)
	}
	assetHosts, err := jobs.TrafficAssets(ctx, job)
	if err != nil || len(assetHosts) != 1 {
		t.Fatalf("credential scoped assets: %+v %v", assetHosts, err)
	}
	on, epoch, err := jobs.TrafficState(ctx, job)
	if err != nil || !on {
		t.Fatalf("credential binding epoch: %v %d %v", on, epoch, err)
	}
	if _, err = settings.Patch(ctx, owner, map[string]any{"credential_traffic_binding": false}); err != nil {
		t.Fatal(err)
	}
	if _, err = settings.Patch(ctx, owner, map[string]any{"credential_traffic_binding": true}); err != nil {
		t.Fatal(err)
	}
	epochMetadata, _ := json.Marshal(map[string]any{"state": "ready", "traffic_state": "ready", "traffic_epoch": epoch})
	if err = jobs.Finish(ctx, job, "ready", ciphertext, epochMetadata); !errors.Is(err, aiapp.ErrCredentialBindingStateChanged) {
		t.Fatalf("off/on allowed stale credential evidence: %v", err)
	}
	if err = jobs.Finish(ctx, job, "queued", nil, nil); err != nil {
		t.Fatal(err)
	}
	var kept []byte
	if err = db.QueryRowContext(ctx, `SELECT sealed_analysis FROM ai_credential_job WHERE intelligence_id=$1`, intelID).Scan(&kept); err != nil || len(kept) == 0 {
		t.Fatalf("requeue lost previous evidence: %v", err)
	}
	if _, err = settings.Patch(ctx, owner, map[string]any{"task_intelligence_enabled": false}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE target SET deleted_at=clock_timestamp() WHERE id=$1`, assetTarget); err != nil {
		t.Fatal(err)
	}
	if _, err = intelligence.ListAsset(ctx, owner, assetID, aiapp.IntelligenceFilter{Limit: 25}); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("deleted asset exposed intelligence: %v", err)
	}
}

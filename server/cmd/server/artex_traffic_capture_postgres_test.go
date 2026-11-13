package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/aitraffic"
	"github.com/yyhuni/lunafox/server/internal/auth"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	aihandler "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/handler"
	aiinfra "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/infrastructure"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	airouter "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/router"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type trafficWorkflowProvider struct {
	count atomic.Int32
	host  string
}

func (p *trafficWorkflowProvider) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		step := p.count.Add(1)
		var name, input string
		if step == 1 {
			name = "fixture_http"
			input = `{}`
		} else if step == 2 {
			name = "traffic_search"
			input = fmt.Sprintf(`{"host":%q}`, p.host)
		}
		if name != "" {
			found := false
			for _, item := range req.Tools {
				if item.Name == name {
					found = true
				}
			}
			if !found {
				yield(llm.StreamEvent{}, fmt.Errorf("tool %s unavailable", name))
				return
			}
		}
		events := []llm.StreamEvent{{Type: llm.SEMessageStart}}
		if name != "" {
			events = append(events, llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: fmt.Sprintf("capture-%d", step), ToolName: name}, llm.StreamEvent{Type: llm.SEToolInputJSON, Text: input}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"})
		} else {
			events = append(events, llm.StreamEvent{Type: llm.SETextDelta, Text: "录制测试完成"}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"})
		}
		events = append(events, llm.StreamEvent{Type: llm.SEMessageStop})
		for _, event := range events {
			if !yield(event, nil) {
				return
			}
		}
	}
}
func (p *trafficWorkflowProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, req) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func verifyARTEXTrafficCapturePostgres(t *testing.T, ctx context.Context, db *sql.DB, dsn string) {
	t.Helper()
	owner, targetID, _ := taskFixtureScope(t, ctx, db, "traffic-fixture.example")
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := airepo.NewTaskStore(orm)
	var hits atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/large" {
			_, _ = io.WriteString(w, strings.Repeat("large-fixture-", 24000))
			return
		}
		_, _ = w.Write([]byte("recorded-fixture-body"))
	}))
	defer target.Close()
	parsed, err := url.Parse(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	manager, err := aitraffic.NewManager(root, func(*http.Request) (*url.URL, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	connector, err := airuntime.NewTaskGraphConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	exec, _ := json.Marshal(map[string]any{"url": target.URL + "/evidence", "method": "GET"})
	custom := airuntime.BuildCustomTool(artexdb.Tool{Key: "fixture_http", Kind: "http", Exec: exec, Schema: json.RawMessage(`{"type":"object","properties":{}}`)}, nil)
	runner := airuntime.NewTaskRunner(connector, root, func(int) []tool.CoreTool { return []tool.CoreTool{custom} })
	runner.SetTrafficResolver(aitraffic.TaskResolver(db, manager))
	var model llm.Provider = &trafficWorkflowProvider{host: parsed.Host}
	resolve := func(context.Context, int) (llm.Provider, string, bool, error) { return model, "fixture", true, nil }
	runner.SetRoleProviderResolver(func(context.Context, int, string) (llm.Provider, string, bool, error) {
		return model, "fixture", true, nil
	})
	controller := aiapp.NewTaskController(store, runner, resolve)
	facade := aiapp.NewTaskFacade(store, resolve, controller)
	task, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "验证流量录制", TargetID: targetID})
	if err != nil {
		t.Fatal(err)
	}
	// Preserve the pre-existing runtime options for the remaining integration tests.
	var old string
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_runtime_setting(tenant_id,options) VALUES($1::uuid,'{}'::jsonb) ON CONFLICT(tenant_id) DO NOTHING`, task.TenantID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT options::text FROM ai_runtime_setting LIMIT 1`).Scan(&old); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.ExecContext(context.Background(), `UPDATE ai_runtime_setting SET options=$1::jsonb WHERE tenant_id=$2::uuid`, old, task.TenantID)
	}()
	if _, err = db.ExecContext(ctx, `UPDATE ai_runtime_setting SET options=jsonb_set(options,'{traffic_capture}','true'::jsonb) WHERE tenant_id=$1::uuid`, task.TenantID); err != nil {
		t.Fatal(err)
	}
	if reply, err := facade.Chat(ctx, owner, task.ID, "记录测试请求"); err != nil || reply != "录制测试完成" {
		t.Fatalf("capture workflow: %q %v", reply, err)
	}
	var output, search string
	if err = db.QueryRowContext(ctx, `SELECT COALESCE(detail,'') FROM tianqiong_ai.activity WHERE exploration_id=$1 AND worker='mainagent' AND kind='tool_result' AND tool='fixture_http' ORDER BY id DESC LIMIT 1`, task.ExplorationID).Scan(&output); err != nil || !strings.Contains(output, "recorded-fixture-body") {
		t.Fatalf("HTTP tool output: %q %v", output, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT COALESCE(detail,'') FROM tianqiong_ai.activity WHERE exploration_id=$1 AND worker='mainagent' AND kind='tool_result' AND tool='traffic_search' ORDER BY id DESC LIMIT 1`, task.ExplorationID).Scan(&search); err != nil || !strings.Contains(search, "/evidence") || hits.Load() != 1 {
		t.Fatalf("traffic search output: %q hits=%d %v", search, hits.Load(), err)
	}
	saved, err := manager.Acquire(ctx, aitraffic.Scope{TenantID: task.TenantID, UserID: owner, TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	records, err := saved.Recorder().Search(parsed.Hostname(), 0, 10)
	if err != nil || len(records) != 1 {
		t.Fatalf("persistent capture: %+v %v", records, err)
	}
	trafficUI := aiapp.NewTaskTrafficFacade(store, manager)
	page, err := trafficUI.List(ctx, owner, task.ID, aiapp.TaskTrafficFilter{Page: 0, Size: 25, Host: "127.0.0.1"})
	if err != nil || page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("authorized traffic page: %+v %v", page, err)
	}
	requestText, responseText, err := trafficUI.Exchange(ctx, owner, task.ID, records[0].ID)
	if err != nil || !strings.Contains(requestText, "/evidence") || !strings.Contains(responseText, "recorded-fixture-body") {
		t.Fatalf("traffic detail: %q %q %v", requestText, responseText, err)
	}
	var other int
	if err = db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if _, err = trafficUI.List(ctx, other, task.ID, aiapp.TaskTrafficFilter{Page: 0, Size: 25}); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign traffic page: %v", err)
	}
	if _, _, err = trafficUI.Exchange(ctx, other, task.ID, records[0].ID); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign traffic detail: %v", err)
	}
	if _, _, err = trafficUI.Blob(ctx, owner, task.ID, strings.Repeat("g", 64), 0, 8192); !errors.Is(err, aiapp.ErrInvalidInput) {
		t.Fatalf("invalid blob hash: %v", err)
	}
	jwt := auth.NewJWTManager("traffic-fixture-secret", time.Minute, time.Hour)
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
	handler := aihandler.NewTaskHandler(facade)
	handler.SetTraffic(trafficUI)
	airouter.RegisterAITaskRoutes(group, handler)
	get := func(path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, req)
		return response
	}
	path := fmt.Sprintf("/v1/aiTasks/%d/traffic", task.ID)
	if response := get(path, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("traffic without auth: %d", response.Code)
	}
	if response := get(path, otherToken); response.Code != http.StatusNotFound {
		t.Fatalf("traffic across accounts: %d", response.Code)
	}
	if response := get(path, ownerToken); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "/evidence") || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("traffic HTTP: %d %s", response.Code, response.Body.String())
	}
	if response := get(path+"/exchange?id="+url.QueryEscape(records[0].ID), ownerToken); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "recorded-fixture-body") {
		t.Fatalf("traffic exchange HTTP: %d %s", response.Code, response.Body.String())
	}
	// Large captured bodies remain in the scoped blob store and can be read in
	// bounded chunks without exposing another account's records.
	proxyURL, err := url.Parse(saved.ProxyURL())
	if err != nil {
		t.Fatal(err)
	}
	cert, err := os.ReadFile(saved.CABundle())
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(cert) {
		t.Fatal("capture CA")
	}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	largeResponse, err := client.Get(target.URL + "/large")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, largeResponse.Body)
	largeResponse.Body.Close()
	client.CloseIdleConnections()
	largeRows, err := saved.Recorder().Search(parsed.Hostname(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var largeID string
	for _, row := range largeRows {
		if strings.Contains(row.URL, "/large") {
			largeID = row.ID
		}
	}
	if largeID == "" {
		t.Fatalf("large exchange missing: %+v", largeRows)
	}
	_, rawLarge, err := trafficUI.Exchange(ctx, owner, task.ID, largeID)
	if err != nil {
		t.Fatal(err)
	}
	matches := regexp.MustCompile(`@blob sha256:([a-f0-9]{64})`).FindStringSubmatch(rawLarge)
	if len(matches) != 2 {
		t.Fatalf("large response missing blob reference: %s", rawLarge)
	}
	chunk, total, err := trafficUI.Blob(ctx, owner, task.ID, matches[1], 0, 8192)
	if err != nil || len(chunk) != 8192 || total <= int64(len(chunk)) || !strings.HasPrefix(string(chunk), "large-fixture-") {
		t.Fatalf("large response chunk: %d/%d %v", len(chunk), total, err)
	}
	if _, _, err = trafficUI.Blob(ctx, other, task.ID, matches[1], 0, 8192); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign traffic blob: %v", err)
	}

	// Store an immutable evidence snapshot for a task finding.
	setupRun := uuid.NewString()
	if _, err = store.ClaimMainChat(ctx, owner, task.ID, setupRun); err != nil {
		t.Fatal(err)
	}
	execIdentity := airuntime.TaskExecution{ID: task.ID, ExplorationID: task.ExplorationID, TenantID: task.TenantID, UserID: owner, RunID: setupRun, ExecutionMode: "main_chat"}
	conn, err := connector.OpenForExecution(ctx, execIdentity)
	if err != nil {
		t.Fatal(err)
	}
	var nodeID, findingID int64
	if err = conn.QueryRowContext(ctx, `INSERT INTO tianqiong_ai.exploration_nodes(exploration_id,kind,payload,state,origin) VALUES($1,'finding','{"summary":"recording fixture"}','confirmed','human') RETURNING id`, task.ExplorationID).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRowContext(ctx, `INSERT INTO tianqiong_ai.findings(task_id,node_id,vulnclass,name,severity,summary) VALUES($1,$2,'fixture','fixture','low','fixture') RETURNING id`, task.ID, nodeID).Scan(&findingID); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if err = store.ReleaseMainChat(ctx, owner, task.ID, setupRun); err != nil {
		t.Fatal(err)
	}
	evidenceFacade := aiapp.NewFindingEvidenceFacade(store, manager, connector, root)
	refs := []artexdb.TrafficRef{{TrafficID: records[0].ID, Role: "proof", Note: "对应验证响应"}}
	handler.SetFindingEvidence(evidenceFacade)
	bound, err := evidenceFacade.Bind(ctx, owner, task.ID, findingID, refs)
	if err != nil || bound.Version != 1 || len(bound.Bindings) != 1 || bound.Bindings[0].Role != "proof" {
		t.Fatalf("bind evidence: %+v %v", bound, err)
	}
	evidencePath := fmt.Sprintf("/v1/aiTasks/%d/findings/%d/traffic", task.ID, findingID)
	if response := get(evidencePath, otherToken); response.Code != http.StatusNotFound {
		t.Fatalf("foreign evidence HTTP: %d", response.Code)
	}
	if response := get(evidencePath, ownerToken); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "对应验证响应") {
		t.Fatalf("evidence list HTTP: %d %s", response.Code, response.Body.String())
	}
	payload, _ := json.Marshal(map[string]any{"traffic_refs": refs})
	req := httptest.NewRequest(http.MethodPost, evidencePath, strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "Bearer "+ownerToken)
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, req)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"version":1`) {
		t.Fatalf("idempotent evidence HTTP: %d %s", response.Code, response.Body.String())
	}
	patchPath := fmt.Sprintf("%s/%d", evidencePath, bound.Bindings[0].ID)
	patch := httptest.NewRequest(http.MethodPatch, patchPath, strings.NewReader(`{"version":0,"role":"baseline"}`))
	patch.Header.Set("Authorization", "Bearer "+ownerToken)
	patch.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, patch)
	if response.Code != http.StatusConflict {
		t.Fatalf("stale evidence HTTP: %d %s", response.Code, response.Body.String())
	}
	if response := get(patchPath+"/body?side=response", ownerToken); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "HTTP 200") {
		t.Fatalf("evidence body HTTP: %d %s", response.Code, response.Body.String())
	}
	if response := get(patchPath+"/body?side=response", otherToken); response.Code != http.StatusNotFound {
		t.Fatalf("foreign evidence body HTTP: %d", response.Code)
	}
	again, err := evidenceFacade.Bind(ctx, owner, task.ID, findingID, refs)
	if err != nil || again.Version != 1 || len(again.Bindings) != 1 {
		t.Fatalf("idempotent evidence bind: %+v %v", again, err)
	}
	if _, err = evidenceFacade.Bind(ctx, other, task.ID, findingID, refs); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign evidence bind: %v", err)
	}
	if _, err = evidenceFacade.Bind(ctx, owner, task.ID, findingID, []artexdb.TrafficRef{{TrafficID: "missing", Role: "proof"}}); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("missing traffic bind: %v", err)
	}
	snapshot, err := store.ReadFindingTraffic(ctx, owner, findingID)
	if err != nil || snapshot.Version != 1 || len(snapshot.Bindings) != 1 {
		t.Fatalf("evidence persistence: %+v %v", snapshot, err)
	}
	listed, err := evidenceFacade.List(ctx, owner, task.ID, findingID)
	if err != nil || listed.Version != 1 || len(listed.Bindings) != 1 {
		t.Fatalf("evidence list: %+v %v", listed, err)
	}
	bindingID := listed.Bindings[0].ID
	body, err := evidenceFacade.Body(ctx, owner, task.ID, findingID, bindingID, "response", 0, 8192)
	if err != nil || !strings.Contains(string(body.Data), "recorded-fixture-body") || !strings.Contains(body.Head, "200") {
		t.Fatalf("immutable evidence body: %+v %v", body, err)
	}
	if _, err = evidenceFacade.Body(ctx, other, task.ID, findingID, bindingID, "response", 0, 8192); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign evidence body: %v", err)
	}
	role, note := "verification", "重新确认"
	if _, err = evidenceFacade.Edit(ctx, owner, task.ID, findingID, bindingID, 0, &role, &note, false, nil); !errors.Is(err, aiapp.ErrEvidenceVersionConflict) {
		t.Fatalf("stale evidence edit: %v", err)
	}
	edited, err := evidenceFacade.Edit(ctx, owner, task.ID, findingID, bindingID, 1, &role, &note, false, nil)
	if err != nil || edited.Version != 2 || edited.Bindings[0].Role != "verification" || edited.Bindings[0].Note != note {
		t.Fatalf("edit evidence: %+v %v", edited, err)
	}
	removed, err := evidenceFacade.Edit(ctx, owner, task.ID, findingID, bindingID, 2, nil, nil, true, nil)
	if err != nil || removed.Version != 3 || len(removed.Bindings) != 0 {
		t.Fatalf("remove evidence: %+v %v", removed, err)
	}
	bound, err = evidenceFacade.Bind(ctx, owner, task.ID, findingID, refs)
	if err != nil || bound.Version != 4 || len(bound.Bindings) != 1 {
		t.Fatalf("rebind evidence: %+v %v", bound, err)
	}
	largeRef := artexdb.TrafficRef{TrafficID: largeID, Role: "supporting", Note: "大正文"}
	bound, err = evidenceFacade.Bind(ctx, owner, task.ID, findingID, []artexdb.TrafficRef{largeRef})
	if err != nil || bound.Version != 5 || len(bound.Bindings) != 2 {
		t.Fatalf("second evidence: %+v %v", bound, err)
	}
	firstBinding, secondBinding := bound.Bindings[0].ID, bound.Bindings[1].ID
	ordered, err := evidenceFacade.Edit(ctx, owner, task.ID, findingID, 0, 5, nil, nil, false, []int64{secondBinding, firstBinding})
	if err != nil || ordered.Version != 6 || ordered.Bindings[0].ID != secondBinding {
		t.Fatalf("evidence reorder: %+v %v", ordered, err)
	}
	largeBody, err := evidenceFacade.Body(ctx, owner, task.ID, findingID, secondBinding, "response", 8192, 8192)
	if err != nil || len(largeBody.Data) != 8192 || largeBody.Total <= 8192 {
		t.Fatalf("evidence body pagination: %+v %v", largeBody, err)
	}
	bound, err = evidenceFacade.Edit(ctx, owner, task.ID, findingID, secondBinding, 6, nil, nil, true, nil)
	if err != nil || bound.Version != 7 || len(bound.Bindings) != 1 {
		t.Fatalf("remove second evidence: %+v %v", bound, err)
	}
	// A real MainAgent report_finding call creates the finding and its evidence
	// atomically when automatic binding is enabled.
	if _, err = db.ExecContext(ctx, `UPDATE ai_runtime_setting SET options=jsonb_set(options,'{agent_traffic_binding}','true'::jsonb) WHERE tenant_id=$1::uuid`, task.TenantID); err != nil {
		t.Fatal(err)
	}
	enabledBinding := func(ctx context.Context, user int) bool {
		var enabled bool
		err := db.QueryRowContext(ctx, `SELECT COALESCE(options->>'agent_traffic_binding','false')='true' FROM ai_runtime_setting WHERE tenant_id=$1::uuid`, task.TenantID).Scan(&enabled)
		return err == nil && enabled && user == owner
	}
	runner.SetFindingTrafficHooks(enabledBinding, evidenceFacade.RecordFinding)
	autoInput, _ := json.Marshal(map[string]any{"vulnclass": "fixture-auto", "name": "自动关联发现", "severity": "low", "summary": "模型验证记录", "traffic_refs": refs})
	model = &independentMainProvider{tool: "report_finding", input: string(autoInput)}
	if _, err = facade.Chat(ctx, owner, task.ID, "登记自动关联发现"); err != nil {
		t.Fatalf("automatic finding chat: %v", err)
	}
	var autoFinding, autoNode int64
	if err = db.QueryRowContext(ctx, `SELECT id,node_id FROM tianqiong_ai.findings WHERE task_id=$1 AND name='自动关联发现'`, task.ID).Scan(&autoFinding, &autoNode); err != nil {
		t.Fatalf("automatic finding not persisted: %v", err)
	}
	autoEvidence, err := store.ReadFindingTraffic(ctx, owner, autoFinding)
	if err != nil || autoEvidence.Version != 1 || len(autoEvidence.Bindings) != 1 {
		t.Fatalf("automatic finding evidence: %+v %v", autoEvidence, err)
	}
	badInput, _ := json.Marshal(map[string]any{"vulnclass": "fixture-auto", "name": "不得保存缺失证据", "severity": "low", "summary": "失败测试", "traffic_refs": []artexdb.TrafficRef{{TrafficID: "missing", Role: "proof"}}})
	model = &independentMainProvider{tool: "report_finding", input: string(badInput)}
	if _, err = facade.Chat(ctx, owner, task.ID, "登记无效证据"); err != nil {
		t.Fatalf("invalid evidence chat: %v", err)
	}
	var invalidCount int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.findings WHERE task_id=$1 AND name='不得保存缺失证据'`, task.ID).Scan(&invalidCount); err != nil || invalidCount != 0 {
		t.Fatalf("invalid evidence partially created finding: %d %v", invalidCount, err)
	}
	guardRun := uuid.NewString()
	if _, err = store.ClaimMainChat(ctx, owner, task.ID, guardRun); err != nil {
		t.Fatal(err)
	}
	guardTask := airuntime.TaskExecution{ID: task.ID, ExplorationID: task.ExplorationID, TenantID: task.TenantID, UserID: owner, RunID: guardRun, ExecutionMode: "main_chat"}
	if _, err = db.ExecContext(ctx, `UPDATE ai_runtime_setting SET options=jsonb_set(options,'{agent_traffic_binding}','false'::jsonb) WHERE tenant_id=$1::uuid`, task.TenantID); err != nil {
		t.Fatal(err)
	}
	_, err = evidenceFacade.RecordFinding(airuntime.WithTaskExecution(ctx, guardTask), artexdb.RecordFindingInput{TaskID: task.ID, ExplorationID: task.ExplorationID, VulnClass: "fixture", Name: "关闭后不得写入", Severity: "low", Summary: "fixture"}, refs)
	if !errors.Is(err, aiapp.ErrTaskControlConflict) {
		t.Fatalf("disabled automatic binding: %v", err)
	}
	if err = store.ReleaseMainChat(ctx, owner, task.ID, guardRun); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_runtime_setting SET options=jsonb_set(options,'{agent_traffic_binding}','true'::jsonb) WHERE tenant_id=$1::uuid`, task.TenantID); err != nil {
		t.Fatal(err)
	}

	// A reporter triggered by this task reuses its recorded evidence, while an
	// unrelated conversation gets a separate account-owned capture scope.
	convStore := airepo.NewConversationStore(orm)
	conversationID, conversationRun, instanceID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = convStore.CreateAgentConversation(ctx, owner, conversationID, "reporter fixture", "reporter"); err != nil {
		t.Fatal(err)
	}
	if err = convStore.BeginRun(ctx, owner, conversationID, conversationRun, instanceID, "report"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_agent_trigger_job(tenant_id,agent_key,owner_user_id,source_task_id,event_key,title,message,status,conversation_id,run_id) VALUES($1::uuid,'reporter',$2,$3,$4,'fixture','fixture','running',$5::uuid,$6::uuid)`, task.TenantID, owner, task.ID, "traffic-fixture-"+conversationRun, conversationID, conversationRun); err != nil {
		t.Fatal(err)
	}
	chatRun := airuntime.ChatRun{TenantID: task.TenantID, UserID: owner, SessionID: conversationID, ExecutionID: conversationRun, AgentKey: "reporter"}
	foreignTask, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "另一任务"})
	if err != nil {
		t.Fatal(err)
	}
	foreignRun := uuid.NewString()
	if _, err = store.ClaimMainChat(ctx, owner, foreignTask.ID, foreignRun); err != nil {
		t.Fatal(err)
	}
	foreignConn, err := connector.OpenForExecution(ctx, airuntime.TaskExecution{ID: foreignTask.ID, ExplorationID: foreignTask.ExplorationID, TenantID: foreignTask.TenantID, UserID: owner, RunID: foreignRun, ExecutionMode: "main_chat"})
	if err != nil {
		t.Fatal(err)
	}
	var foreignNode, foreignFinding int64
	if err = foreignConn.QueryRowContext(ctx, `INSERT INTO tianqiong_ai.exploration_nodes(exploration_id,kind,payload,state,origin) VALUES($1,'finding','{"summary":"foreign"}','confirmed','human') RETURNING id`, foreignTask.ExplorationID).Scan(&foreignNode); err != nil {
		t.Fatal(err)
	}
	if err = foreignConn.QueryRowContext(ctx, `INSERT INTO tianqiong_ai.findings(task_id,node_id,vulnclass,name,severity,summary) VALUES($1,$2,'fixture','foreign','low','foreign') RETURNING id`, foreignTask.ID, foreignNode).Scan(&foreignFinding); err != nil {
		t.Fatal(err)
	}
	foreignConn.Close()
	if err = store.ReleaseMainChat(ctx, owner, foreignTask.ID, foreignRun); err != nil {
		t.Fatal(err)
	}
	if _, err = evidenceFacade.BindReporter(ctx, owner, foreignTask.ID, foreignFinding, conversationRun, refs); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("reporter bound another task: %v", err)
	}
	reporter, err := aitraffic.ConversationResolver(db, manager)(ctx, chatRun)
	if err != nil || reporter == nil || reporter.Lease.ProxyURL() != saved.ProxyURL() {
		t.Fatalf("reporter did not reuse task traffic: %+v %v", reporter, err)
	}
	var searchTool tool.CoreTool
	for _, item := range reporter.Lease.Tools() {
		if item.Name() == "traffic_search" {
			searchTool = item
			break
		}
	}
	request, _ := json.Marshal(map[string]any{"host": parsed.Host})
	found, err := searchTool.Call(ctx, request, &tool.ToolContext{})
	if err != nil || found.IsError || !strings.Contains(found.Flatten(), "/evidence") {
		t.Fatalf("reporter task traffic search: %+v %v", found, err)
	}
	// Remove the previous manual link, then let the real reporter tool rebind it.
	if _, err = evidenceFacade.Edit(ctx, owner, task.ID, findingID, bound.Bindings[0].ID, 7, nil, nil, true, nil); err != nil {
		t.Fatal(err)
	}
	facade.SetFindingEvidence(evidenceFacade)
	reporterTools := aiinfra.FindingTrafficTools(facade, owner)
	reporterInput, _ := json.Marshal(map[string]any{"finding_id": fmt.Sprint(findingID), "traffic_refs": refs})
	reporterModel := &independentMainProvider{tool: "bind_finding_traffic", input: string(reporterInput)}
	chatRunner, err := airuntime.NewChatRunner(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = chatRunner.Run(ctx, airuntime.ChatRun{TenantID: task.TenantID, UserID: owner, SessionID: conversationID, ExecutionID: conversationRun, AgentKey: "reporter", Message: "关联验证证据", Provider: reporterModel, Model: "fixture", Tools: reporterTools, TrafficResolver: aitraffic.ConversationResolver(db, manager), FindingTrafficEnabled: func(ctx context.Context) bool { return enabledBinding(ctx, owner) }}, nil)
	if err != nil {
		t.Fatalf("reporter binding run: %v", err)
	}
	reporterEvidence, err := store.ReadFindingTraffic(ctx, owner, findingID)
	if err != nil || reporterEvidence.Version != 9 || len(reporterEvidence.Bindings) != 1 {
		t.Fatalf("reporter binding result: %+v %v", reporterEvidence, err)
	}
	reporter.Lease.Close()
	saved.Close()
	if err = convStore.FinishRun(ctx, owner, conversationID, conversationRun, instanceID, "completed"); err != nil {
		t.Fatal(err)
	}
	if _, err = reporter.Enabled(ctx); err == nil {
		t.Fatal("completed reporter retained traffic access")
	}

	run := uuid.NewString()
	if _, err = store.ClaimMainChat(ctx, owner, task.ID, run); err != nil {
		t.Fatal(err)
	}
	identity := airuntime.TaskExecution{ID: task.ID, ExplorationID: task.ExplorationID, TenantID: task.TenantID, UserID: owner, RunID: run, ExecutionMode: "main_chat"}
	access, err := aitraffic.TaskResolver(db, manager)(ctx, identity)
	if err != nil || access == nil {
		t.Fatalf("live capture lease: %+v %v", access, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_runtime_setting SET options=jsonb_set(options,'{traffic_capture}','false'::jsonb) WHERE tenant_id=$1::uuid`, task.TenantID); err != nil {
		t.Fatal(err)
	}
	if enabled, err := access.Enabled(ctx); err != nil || enabled {
		t.Fatalf("live disable: %v %v", enabled, err)
	}
	if _, err = store.StopMainChat(ctx, owner, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = access.Enabled(ctx); err == nil {
		t.Fatal("stopped run retained traffic access")
	}
	access.Lease.Close()
	if err = store.ReleaseMainChat(ctx, owner, task.ID, run); err != nil {
		t.Fatal(err)
	}
	if _, err = aitraffic.TaskResolver(db, manager)(ctx, identity); err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("stale run traffic admission: %v", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE target SET deleted_at=clock_timestamp() WHERE id=$1`, targetID); err != nil {
		t.Fatal(err)
	}
	if _, err = trafficUI.List(ctx, owner, task.ID, aiapp.TaskTrafficFilter{Page: 0, Size: 25}); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("deleted target exposed traffic: %v", err)
	}
}

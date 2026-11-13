package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/auth"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	settingsrepo "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	aihandler "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/handler"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	airouter "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/router"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	assetrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type intelligenceModelFixture struct {
	mentionModelFixture
	calls       atomic.Int64
	assetID     int64
	ready       chan struct{}
	continueRun chan struct{}
	result      chan string
	schema      chan bool
}

type credentialModelFixture struct {
	mode    atomic.Int32
	calls   atomic.Int32
	started chan struct{}
}

func (p *credentialModelFixture) Stream(ctx context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		message, stop, _, err := p.Complete(ctx, request)
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}
		for _, event := range []llm.StreamEvent{{Type: llm.SEMessageStart}, {Type: llm.SETextDelta, Text: message.Text()}, {Type: llm.SEMessageDelta, StopReason: stop}, {Type: llm.SEMessageStop}} {
			if !yield(event, nil) {
				return
			}
		}
	}
}
func (p *credentialModelFixture) Complete(ctx context.Context, request llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	p.calls.Add(1)
	if p.mode.Load() == 0 {
		select {
		case p.started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return llm.Message{}, "", llm.Usage{}, ctx.Err()
	}
	value := `{"accounts":[{"username":"alice","password":"fabricated"}]}`
	if p.mode.Load() == 2 {
		value = `{"accounts":[{"username":"alice","password":"p@ss"}]}`
	}
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock(value)}}, "end_turn", llm.Usage{InputTokens: 11, OutputTokens: 7}, nil
}

func (p *intelligenceModelFixture) Stream(ctx context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	worker := false
	schemaOK := false
	recordTool := false
	for _, item := range request.Tools {
		if item.Name == "record_fact" {
			worker = true
			props, _ := item.InputSchema["properties"].(map[string]any)
			_, schemaOK = props["intelligence"]
		}
		if item.Name == "record_intelligence" {
			recordTool = true
		}
	}
	if !worker {
		return p.mentionModelFixture.Stream(ctx, request)
	}
	select {
	case p.schema <- schemaOK && recordTool:
	default:
	}
	call := p.calls.Add(1)
	for _, message := range request.Messages {
		for _, block := range message.Content {
			if block.Type == llm.BlockToolResult && block.ToolUseID == "intel-after-disable" {
				raw, _ := json.Marshal(block.Content)
				select {
				case p.result <- string(raw):
				default:
				}
			}
		}
	}
	if call <= 4 {
		if call == 4 {
			close(p.ready)
			select {
			case <-p.continueRun:
			case <-ctx.Done():
				return func(yield func(llm.StreamEvent, error) bool) { yield(llm.StreamEvent{}, ctx.Err()) }
			}
		}
		id, name, input := "intel-lead", "record_intelligence", fmt.Sprintf(`{"kind":"lead","title":"线索","content":"fixture-lead","evidence":"fixture-observation","confidence":"observed","asset_ids":[%d]}`, p.assetID)
		if call == 2 {
			id, name, input = "intel-fact", "record_fact", fmt.Sprintf(`{"summary":"发现敏感数据","asset_ids":[%d],"intelligence":[{"kind":"sensitive_data","title":"数据样本","content":"fixture-sensitive","evidence":"fixture-file","confidence":"observed"}]}`, p.assetID)
		}
		if call == 3 {
			id, name, input = "intel-asset", "insert_assets", `{"assets":[{"type":"service","url":"https://intel-auto.example/","auth":[{"username":"fixture-user","password":"fixture-secret"}]}]}`
		}
		if call == 4 {
			id, name, input = "intel-after-disable", "record_intelligence", `{"kind":"lead","title":"不应保存","content":"disabled-content","evidence":"fixture","confidence":"observed"}`
		}
		return func(yield func(llm.StreamEvent, error) bool) {
			for _, event := range []llm.StreamEvent{{Type: llm.SEMessageStart}, {Type: llm.SEToolUseStart, ToolID: id, ToolName: name}, {Type: llm.SEToolInputJSON, Text: input}, {Type: llm.SEMessageDelta, StopReason: "tool_use"}, {Type: llm.SEMessageStop}} {
				if !yield(event, nil) {
					return
				}
			}
		}
	}
	return p.mentionModelFixture.Stream(ctx, request)
}
func (p *intelligenceModelFixture) Complete(ctx context.Context, request llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, request) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func verifyARTEXTaskIntelligencePostgres(t *testing.T, ctx context.Context, db *sql.DB, dsn string) {
	t.Helper()
	t.Setenv("TIANQIONG_AI_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{42}, 32)))
	t.Setenv("TIANQIONG_AI_ENCRYPTION_KEY_FILE", "")
	owner, target, asset := taskFixtureScope(t, ctx, db, "intelligence-fixture.example")
	var other int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := airepo.NewTaskStore(orm)
	tasks := aiapp.NewTaskFacade(store, nil, nil)
	intelligence := aiapp.NewIntelligenceFacade(airepo.NewTaskIntelligenceStore(orm), airuntime.SecretBox{})
	settings := settingsapp.NewRuntimeSettingFacade(settingsrepo.NewToolCatalogStore(orm), airuntime.SecretBox{}, nil)
	if _, err = settings.Patch(ctx, owner, map[string]any{"task_intelligence_enabled": false}); err != nil {
		t.Fatal(err)
	}
	if _, err = settings.Patch(ctx, owner, map[string]any{"credential_agent_enabled": true}); !errors.Is(err, settingsapp.ErrInvalidProfile) {
		t.Fatalf("credential agent enabled without intelligence: %v", err)
	}
	if _, err = settings.Patch(ctx, owner, map[string]any{"task_intelligence_enabled": true, "credential_agent_enabled": true, "credential_traffic_binding": true}); err != nil {
		t.Fatal(err)
	}
	var settingsWG sync.WaitGroup
	settingsWG.Add(2)
	go func() {
		defer settingsWG.Done()
		_, _ = settings.Patch(ctx, owner, map[string]any{"task_intelligence_enabled": false})
	}()
	go func() {
		defer settingsWG.Done()
		_, _ = settings.Patch(ctx, owner, map[string]any{"credential_agent_enabled": true})
	}()
	settingsWG.Wait()
	options, err := settings.Get(ctx, owner)
	if err != nil || options["task_intelligence_enabled"] != false || options["credential_agent_enabled"] != false || options["credential_traffic_binding"] != false {
		t.Fatalf("concurrent intelligence settings invariant: %+v %v", options, err)
	}
	task, err := tasks.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "验证情报", AssetIDs: []int64{asset}, WorkerCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ControlTask(ctx, owner, task.ID, "start", false); err != nil {
		t.Fatal(err)
	}
	var run *aiapp.Task
	for attempts := 0; attempts < 100; attempts++ {
		run, err = store.ClaimTask(ctx, uuid.NewString())
		if err != nil || run == nil {
			t.Fatalf("claim intelligence task: %+v %v", run, err)
		}
		if run.ID == task.ID {
			break
		}
		if err = store.FinishTask(ctx, run, "paused", "fixture cleanup"); err != nil {
			t.Fatal(err)
		}
		run = nil
	}
	if run == nil || run.ID != task.ID {
		t.Fatal("intelligence task not claimed")
	}
	run, err = store.StampTaskStart(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	execution := taskExecution(run)
	runCtx := airuntime.WithTaskExecution(ctx, execution)
	input := artexdb.IntelligenceInput{Kind: "credential", Title: "账号", Content: `{"username":"u","password":"fixture-direct-secret"}`, Evidence: "fixture direct evidence", Confidence: "observed", AssetIDs: []int64{asset}}
	if _, err = intelligence.Record(runCtx, task.ID, 0, "worker", input); !errors.Is(err, aiapp.ErrTaskControlConflict) {
		t.Fatalf("disabled intelligence wrote data: %v", err)
	}
	if _, err = settings.Patch(ctx, owner, map[string]any{"task_intelligence_enabled": true}); err != nil {
		t.Fatal(err)
	}
	id, err := intelligence.Record(runCtx, task.ID, 0, "worker", input)
	if err != nil {
		t.Fatal(err)
	}
	var plainContent, plainEvidence, fingerprint string
	var sealed []byte
	if err = db.QueryRowContext(ctx, `SELECT content,evidence,fingerprint,sealed_payload FROM tianqiong_ai.task_intelligence WHERE id=$1`, id).Scan(&plainContent, &plainEvidence, &fingerprint, &sealed); err != nil {
		t.Fatal(err)
	}
	if plainContent != "" || plainEvidence != "" || strings.Contains(string(sealed), "fixture-direct-secret") || !strings.HasPrefix(fingerprint, "hmac-sha256:") {
		t.Fatal("intelligence sensitive content was not sealed")
	}
	if err = intelligence.UpdateStatus(ctx, owner, task.ID, id, "verified"); err != nil {
		t.Fatal(err)
	}
	input.Content = `{"password":"fixture-direct-secret","username":"u"}`
	again, err := intelligence.Record(runCtx, task.ID, 0, "worker", input)
	if err != nil || again != id {
		t.Fatalf("canonical dedup: %d %d %v", id, again, err)
	}
	page, err := intelligence.List(ctx, owner, task.ID, aiapp.IntelligenceFilter{Query: "fixture-direct-secret"})
	if err != nil || page.Total != 1 || page.Items[0].Status != "verified" {
		t.Fatalf("intelligence read or triage lost: %+v %v", page, err)
	}
	if _, err = intelligence.List(ctx, other, task.ID, aiapp.IntelligenceFilter{}); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign intelligence read: %v", err)
	}
	if err = intelligence.UpdateStatus(ctx, other, task.ID, id, "invalid"); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign intelligence edit: %v", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE tianqiong_ai.task_intelligence SET status='invalid' WHERE id=$1`, id); err == nil {
		t.Fatal("unfenced intelligence update accepted")
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
	taskHandler := aihandler.NewTaskHandler(tasks, intelligence)
	airouter.RegisterAITaskRoutes(group, taskHandler)
	request := func(method, path, authToken, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if authToken != "" {
			req.Header.Set("Authorization", "Bearer "+authToken)
		}
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	base := fmt.Sprintf("/v1/aiTasks/%d/intelligence", task.ID)
	if out := request(http.MethodGet, base, "", ""); out.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous intelligence: %d", out.Code)
	}
	if out := request(http.MethodGet, base, otherToken, ""); out.Code != http.StatusNotFound {
		t.Fatalf("foreign intelligence HTTP: %d", out.Code)
	}
	if out := request(http.MethodGet, base, token, ""); out.Code != http.StatusOK || out.Header().Get("Cache-Control") != "no-store" || !strings.Contains(out.Body.String(), "fixture-direct-secret") {
		t.Fatalf("intelligence HTTP response: %d %s", out.Code, out.Body.String())
	}
	if out := request(http.MethodPatch, fmt.Sprintf("%s/%d", base, id), token, `{"status":"invalid"}`); out.Code != http.StatusOK {
		t.Fatalf("intelligence status HTTP: %d %s", out.Code, out.Body.String())
	}
	if err = intelligence.UpdateStatus(ctx, owner, task.ID, id, "verified"); err != nil {
		t.Fatal(err)
	}
	connector, err := airuntime.NewTaskGraphConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := connector.OpenForExecution(ctx, execution)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	graph := (&artexdb.DB{DB: conn}).Exploration(task.ExplorationID)
	if _, err = graph.AddGoal(map[string]any{"text": "验证情报记录"}, "human"); err != nil {
		t.Fatal(err)
	}
	intent, err := graph.AddIntent(map[string]any{"summary": "情报 Worker"}, 9, []int64{asset}, "planner")
	if err != nil {
		t.Fatal(err)
	}
	registryRead := assetapp.NewFacade(assetrepo.NewAssetRegistryStore(orm))
	registryWrite := assetapp.NewRegistrationFacade(assetrepo.NewAssetRegistrationStore(orm, assetapp.LegacyAssetCreators{}), airuntime.SecretBox{})
	runner := airuntime.NewTaskRunner(connector, t.TempDir(), airuntime.AssetToolResolver(registryRead, registryWrite))
	runner.SetIntelligenceHooks(intelligence.Enabled, intelligence.Record)
	catalog := settingsapp.NewToolCatalogFacade(settingsrepo.NewToolCatalogStore(orm), nil, nil, nil)
	catalog.SetSeeds(settingsapp.NativeToolSeeds(nil))
	if err = catalog.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	runner.SetNativeTools(false, catalog.Resolve)
	model := &intelligenceModelFixture{assetID: asset, ready: make(chan struct{}), continueRun: make(chan struct{}), result: make(chan string, 1), schema: make(chan bool, 1)}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); _, _ = runner.RunTask(workCtx, execution, model, "fixture") }()
	select {
	case has := <-model.schema:
		if !has {
			t.Fatal("intelligence tool or fact schema unavailable")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("intelligence worker did not start")
	}
	select {
	case <-model.ready:
	case <-time.After(8 * time.Second):
		t.Fatal("intelligence worker did not record fixtures")
	}
	page, err = intelligence.List(ctx, owner, task.ID, aiapp.IntelligenceFilter{})
	if err != nil || page.Total != 4 || page.Counts["credential"] != 2 || page.Counts["lead"] != 1 || page.Counts["sensitive_data"] != 1 {
		t.Fatalf("runtime intelligence records: %+v %v", page, err)
	}
	var linked int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.task_intelligence WHERE task_id=$1 AND node_id=$2`, task.ID, intent).Scan(&linked); err != nil || linked != 2 {
		t.Fatalf("Worker provenance missing: %d %v", linked, err)
	}
	child, err := tasks.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "继承情报", SourceTaskIDs: []int64{task.ID}})
	if err != nil {
		t.Fatal(err)
	}
	inherited, err := intelligence.List(ctx, owner, child.ID, aiapp.IntelligenceFilter{})
	if err != nil || inherited.Total != 4 || !inherited.Items[0].Inherited {
		t.Fatalf("inherited intelligence: %+v %v", inherited, err)
	}
	if err = intelligence.UpdateStatus(ctx, owner, child.ID, id, "invalid"); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("inherited intelligence was writable: %v", err)
	}
	limited, err := tasks.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "目标限制", TargetID: target, SourceTaskIDs: []int64{task.ID}})
	if err != nil {
		t.Fatal(err)
	}
	limitedPage, err := intelligence.List(ctx, owner, limited.ID, aiapp.IntelligenceFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range limitedPage.Items {
		if strings.Contains(item.Content, "fixture-secret") {
			t.Fatal("out-of-target intelligence was inherited")
		}
	}
	_, _, outsideAsset := taskFixtureScope(t, ctx, db, "intelligence-outside.example")
	if _, err = store.ControlTask(ctx, owner, limited.ID, "start", false); err != nil {
		t.Fatal(err)
	}
	var limitedRun *aiapp.Task
	for attempts := 0; attempts < 100; attempts++ {
		limitedRun, err = store.ClaimTask(ctx, uuid.NewString())
		if err != nil || limitedRun == nil {
			t.Fatalf("claim limited intelligence task: %+v %v", limitedRun, err)
		}
		if limitedRun.ID == limited.ID {
			break
		}
		if err = store.FinishTask(ctx, limitedRun, "paused", "fixture cleanup"); err != nil {
			t.Fatal(err)
		}
		limitedRun = nil
	}
	if limitedRun == nil || limitedRun.ID != limited.ID {
		t.Fatal("limited intelligence task not claimed")
	}
	limitedRun, err = store.StampTaskStart(ctx, limitedRun)
	if err != nil {
		t.Fatal(err)
	}
	badInput := artexdb.IntelligenceInput{Kind: "lead", Title: "越界", Content: "outside", Evidence: "fixture", Confidence: "observed", AssetIDs: []int64{outsideAsset}}
	if _, err = intelligence.Record(airuntime.WithTaskExecution(ctx, taskExecution(limitedRun)), limited.ID, 0, "worker", badInput); !errors.Is(err, aiapp.ErrRunLeaseLost) {
		t.Fatalf("out-of-target intelligence recorded: %v", err)
	}
	if err = store.FinishTask(ctx, limitedRun, "paused", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(ctx, `INSERT INTO tianqiong_ai.task_intelligence(task_id,kind,title,content,evidence,confidence,status,asset_ids,worker,fingerprint) VALUES($1,'lead','历史情报','legacy-content','legacy-evidence','observed','pending','[]','worker','legacy-fixture')`, task.ID); err != nil {
		t.Fatal(err)
	}
	legacy, err := intelligence.List(ctx, owner, task.ID, aiapp.IntelligenceFilter{Query: "legacy-content"})
	if err != nil || legacy.Total != 1 || legacy.Items[0].Evidence != "legacy-evidence" {
		t.Fatalf("legacy intelligence compatibility: %+v %v", legacy, err)
	}
	if _, err = settings.Patch(ctx, owner, map[string]any{"task_intelligence_enabled": false}); err != nil {
		t.Fatal(err)
	}
	if historical, err := intelligence.List(ctx, owner, task.ID, aiapp.IntelligenceFilter{}); err != nil || historical.Enabled || historical.Total != 5 {
		t.Fatalf("disabled historical intelligence: %+v %v", historical, err)
	}
	close(model.continueRun)
	select {
	case got := <-model.result:
		if !strings.Contains(got, "关闭") {
			t.Fatalf("disabled live write: %s", got)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("disabled intelligence result missing")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("intelligence worker did not stop")
	}
	if err = store.FinishTask(ctx, run, "paused", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = intelligence.Record(runCtx, task.ID, 0, "worker", input); !errors.Is(err, aiapp.ErrRunLeaseLost) {
		t.Fatalf("expired run wrote intelligence: %v", err)
	}
	if err = settingsrepo.NewAgentCatalogStore(orm).SeedDefaultAgents(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_custom_agent SET system_prompt='用户自定义凭据提示',enabled=FALSE WHERE tenant_id=$1 AND agent_key='credential_processor'`, task.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM ai_runtime_seed WHERE tenant_id=$1 AND seed_key='credential_processor_seed_v1'`, task.TenantID); err != nil {
		t.Fatal(err)
	}
	if err = settingsrepo.NewAgentCatalogStore(orm).SeedDefaultAgents(ctx); err != nil {
		t.Fatal(err)
	}
	var customPrompt string
	var customEnabled bool
	if err = db.QueryRowContext(ctx, `SELECT system_prompt,enabled FROM ai_custom_agent WHERE tenant_id=$1 AND agent_key='credential_processor'`, task.TenantID).Scan(&customPrompt, &customEnabled); err != nil || customPrompt != "用户自定义凭据提示" || customEnabled {
		t.Fatalf("credential seed overwrote existing settings: %q %t %v", customPrompt, customEnabled, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_custom_agent SET system_prompt=$2,enabled=TRUE WHERE tenant_id=$1 AND agent_key='credential_processor'`, task.TenantID, agent.CredentialProcessorPrompt); err != nil {
		t.Fatal(err)
	}
	if _, err = settings.Patch(ctx, owner, map[string]any{"task_intelligence_enabled": true, "credential_agent_enabled": true}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ControlTask(ctx, owner, task.ID, "resume", false); err != nil {
		t.Fatal(err)
	}
	var secondRun *aiapp.Task
	for attempts := 0; attempts < 100; attempts++ {
		secondRun, err = store.ClaimTask(ctx, uuid.NewString())
		if err != nil || secondRun == nil {
			t.Fatalf("claim credential source: %+v %v", secondRun, err)
		}
		if secondRun.ID == task.ID {
			break
		}
		if err = store.FinishTask(ctx, secondRun, "paused", "fixture cleanup"); err != nil {
			t.Fatal(err)
		}
		secondRun = nil
	}
	if secondRun == nil || secondRun.ID != task.ID {
		t.Fatal("credential source task not claimed")
	}
	secondRun, err = store.StampTaskStart(ctx, secondRun)
	if err != nil {
		t.Fatal(err)
	}
	unstructured := artexdb.IntelligenceInput{Kind: "credential", Title: "非结构化账号", Content: "账号 alice 密码 p@ss", Evidence: "fixture source", Confidence: "observed", AssetIDs: []int64{asset}}
	unstructuredID, err := intelligence.Record(airuntime.WithTaskExecution(ctx, taskExecution(secondRun)), task.ID, 0, "worker", unstructured)
	if err != nil {
		t.Fatal(err)
	}
	secondConn, err := connector.OpenForExecution(ctx, taskExecution(secondRun))
	if err != nil {
		t.Fatal(err)
	}
	var legacyCredential int64
	if err = secondConn.QueryRowContext(ctx, `INSERT INTO tianqiong_ai.task_intelligence(task_id,kind,title,content,evidence,confidence,status,asset_ids,worker,fingerprint,credential_analysis) VALUES($1,'credential','历史凭据','legacy-account','legacy-source','observed','unverified','[]','worker','legacy-credential','{"state":"ready","method":"rules","accounts":[{"username":"legacy","password":"legacy-pass"}],"traffic":[],"traffic_state":"off"}') RETURNING id`, task.ID).Scan(&legacyCredential); err != nil {
		t.Fatal(err)
	}
	secondConn.Close()
	if err = store.FinishTask(ctx, secondRun, "paused", ""); err != nil {
		t.Fatal(err)
	}
	credentialModel := &credentialModelFixture{started: make(chan struct{}, 1)}
	jobStore := airepo.NewCredentialJobStore(orm)
	processor := aiapp.NewCredentialProcessor(jobStore, airuntime.SecretBox{}, func(_ context.Context, user int) (llm.Provider, string, string, bool, error) {
		if user != owner {
			return nil, "", "", false, errors.New("wrong owner")
		}
		metered := airuntime.MeterLLMProvider(credentialModel, settingsrepo.NewLLMUsageStore(orm), "", "", "credential-fixture")
		return airuntime.BindLLMUsageUser(metered, user), "credential-fixture", "", true, nil
	})
	taskHandler.SetCredentialProcessor(processor)
	processorCtx, cancelProcessor := context.WithCancel(ctx)
	processor.Start(processorCtx)
	defer func() { cancelProcessor(); <-processor.Done() }()
	select {
	case <-credentialModel.started:
	case <-time.After(8 * time.Second):
		t.Fatal("credential model did not begin")
	}
	var legacyJobs int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_credential_job WHERE intelligence_id=$1`, legacyCredential).Scan(&legacyJobs); err != nil || legacyJobs != 0 {
		t.Fatalf("legacy completed credential was reprocessed: %d %v", legacyJobs, err)
	}
	legacyResult, err := intelligence.List(ctx, owner, task.ID, aiapp.IntelligenceFilter{ID: legacyCredential, Limit: 1})
	if err != nil || len(legacyResult.Items) != 1 || legacyResult.Items[0].Credentials == nil || len(legacyResult.Items[0].Credentials.Accounts) != 1 {
		t.Fatalf("legacy credential analysis lost: %+v %v", legacyResult, err)
	}
	structured, err := intelligence.List(ctx, owner, task.ID, aiapp.IntelligenceFilter{ID: id, Limit: 1})
	if err != nil || len(structured.Items) != 1 || structured.Items[0].Credentials == nil || structured.Items[0].Credentials.State != "ready" || structured.Items[0].Credentials.Method != "rules" || len(structured.Items[0].Credentials.Accounts) != 1 {
		t.Fatalf("structured credential rules: %+v %v", structured, err)
	}
	if credentialModel.calls.Load() != 1 {
		t.Fatalf("structured credentials invoked model: %d", credentialModel.calls.Load())
	}
	if _, err = settings.Patch(ctx, owner, map[string]any{"credential_agent_enabled": false}); err != nil {
		t.Fatal(err)
	}
	waitCredential := func(want string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			var state string
			err := db.QueryRowContext(ctx, `SELECT status FROM ai_credential_job WHERE intelligence_id=$1`, unstructuredID).Scan(&state)
			if err == nil && state == want {
				return
			}
			time.Sleep(30 * time.Millisecond)
		}
		t.Fatalf("credential job did not become %s", want)
	}
	waitCredential("queued")
	retryPath := fmt.Sprintf("%s/%d/credentials/retry", base, unstructuredID)
	if out := request(http.MethodPost, retryPath, token, `{}`); out.Code != http.StatusConflict {
		t.Fatalf("disabled credential retry HTTP: %d %s", out.Code, out.Body.String())
	}
	var sealedAnalysis []byte
	if err = db.QueryRowContext(ctx, `SELECT sealed_analysis FROM ai_credential_job WHERE intelligence_id=$1`, unstructuredID).Scan(&sealedAnalysis); err != nil || len(sealedAnalysis) != 0 {
		t.Fatalf("disabled processor published a result: %v", err)
	}
	credentialModel.mode.Store(1)
	if _, err = settings.Patch(ctx, owner, map[string]any{"credential_agent_enabled": true}); err != nil {
		t.Fatal(err)
	}
	waitCredential("error")
	failed, err := intelligence.List(ctx, owner, task.ID, aiapp.IntelligenceFilter{ID: unstructuredID, Limit: 1})
	if err != nil || len(failed.Items) != 1 || failed.Items[0].Credentials == nil || failed.Items[0].Credentials.State != "error" || strings.Contains(fmt.Sprint(failed.Items[0].Credentials.Accounts), "fabricated") {
		t.Fatalf("ungrounded credential output accepted: %+v %v", failed, err)
	}
	if err = processor.Retry(ctx, other, task.ID, unstructuredID); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign credential retry: %v", err)
	}
	if out := request(http.MethodPost, retryPath, otherToken, `{}`); out.Code != http.StatusNotFound {
		t.Fatalf("foreign credential retry HTTP: %d", out.Code)
	}
	credentialModel.mode.Store(2)
	if out := request(http.MethodPost, retryPath, token, `{}`); out.Code != http.StatusAccepted {
		t.Fatalf("credential retry HTTP: %d %s", out.Code, out.Body.String())
	}
	waitCredential("ready")
	ready, err := intelligence.List(ctx, owner, task.ID, aiapp.IntelligenceFilter{ID: unstructuredID, Limit: 1})
	if err != nil || len(ready.Items) != 1 || ready.Items[0].Credentials == nil || len(ready.Items[0].Credentials.Accounts) != 1 || ready.Items[0].Credentials.Accounts[0].Password != "p@ss" || ready.Items[0].Credentials.InputTokens != 11 {
		t.Fatalf("credential model result: %+v %v", ready, err)
	}
	var metadata string
	if err = db.QueryRowContext(ctx, `SELECT sealed_analysis,metadata::text FROM ai_credential_job WHERE intelligence_id=$1`, unstructuredID).Scan(&sealedAnalysis, &metadata); err != nil || bytes.Contains(sealedAnalysis, []byte("p@ss")) || strings.Contains(metadata, "p@ss") {
		t.Fatalf("credential analysis not sealed: %v", err)
	}
	var credentialUsage int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_llm_usage WHERE owner_user_id=$1 AND task_id=$2 AND exploration_id=$3 AND worker='credential_processor' AND input_tokens=11 AND output_tokens=7`, owner, task.ID, task.ExplorationID).Scan(&credentialUsage); err != nil || credentialUsage < 2 {
		t.Fatalf("credential model usage missing: %d %v", credentialUsage, err)
	}
	cancelProcessor()
	<-processor.Done()
	if err = processor.Retry(ctx, owner, task.ID, unstructuredID); err != nil {
		t.Fatal(err)
	}
	type claimResult struct {
		job *aiapp.CredentialJob
		err error
	}
	claimResults := make(chan claimResult, 2)
	for i := 0; i < 2; i++ {
		go func() { job, err := jobStore.Claim(ctx); claimResults <- claimResult{job, err} }()
	}
	var job *aiapp.CredentialJob
	for i := 0; i < 2; i++ {
		result := <-claimResults
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.job != nil {
			if job != nil {
				t.Fatal("two processors claimed one credential")
			}
			job = result.job
		}
	}
	if job == nil || job.IntelligenceID != unstructuredID {
		t.Fatalf("claim retried credential: %+v", job)
	}
	if err = processor.Retry(ctx, owner, task.ID, unstructuredID); !errors.Is(err, aiapp.ErrTaskControlConflict) {
		t.Fatalf("retry processing credential: %v", err)
	}
	staleJob := *job
	staleJob.Lease = uuid.NewString()
	if err = jobStore.Finish(ctx, &staleJob, "queued", nil, nil); !errors.Is(err, aiapp.ErrRunLeaseLost) {
		t.Fatalf("stale credential lease completed job: %v", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_credential_job SET status='ready',lease_id=NULL,lease_until=NULL WHERE intelligence_id=$1`, unstructuredID); err == nil {
		t.Fatal("unfenced credential completion accepted")
	}
	if err = jobStore.Finish(ctx, job, "queued", nil, nil); err != nil {
		t.Fatal(err)
	}
	job, err = jobStore.Claim(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim before archive: %+v %v", job, err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_task_archive(task_id,state) VALUES($1,'archiving')`, task.ID); err != nil {
		t.Fatal(err)
	}
	if enabled, err := jobStore.Enabled(ctx, job); err != nil || enabled {
		t.Fatalf("archive did not stop credential job: %t %v", enabled, err)
	}
	if err = jobStore.Finish(ctx, job, "queued", nil, nil); err != nil {
		t.Fatalf("archive requeue: %v", err)
	}
	if next, err := jobStore.Claim(ctx); err != nil || next != nil {
		t.Fatalf("claimed credential during archive: %+v %v", next, err)
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM ai_task_archive WHERE task_id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
}

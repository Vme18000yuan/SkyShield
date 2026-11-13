package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	aiinfra "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/infrastructure"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	assetrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type scopePlannerModel struct {
	mentionModelFixture
	calls     atomic.Int64
	tools     chan string
	result    chan string
	assets    chan string
	outside   chan string
	company   chan string
	tech      chan string
	port      chan string
	outsideID int64
	companyID int
}

type targetAssetModel struct {
	mentionModelFixture
	calls   atomic.Int64
	assetID int64
	result  chan string
}

func (p *targetAssetModel) Stream(ctx context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	planner := false
	for _, item := range request.Tools {
		planner = planner || item.Name == "add_intent"
	}
	if planner {
		if p.calls.Add(1) == 1 {
			return func(yield func(llm.StreamEvent, error) bool) {
				for _, event := range []llm.StreamEvent{{Type: llm.SEMessageStart}, {Type: llm.SEToolUseStart, ToolID: "target-metadata", ToolName: "list_assets"}, {Type: llm.SEToolInputJSON, Text: fmt.Sprintf(`{"id":%d}`, p.assetID)}, {Type: llm.SEMessageDelta, StopReason: "tool_use"}, {Type: llm.SEMessageStop}} {
					if !yield(event, nil) {
						return
					}
				}
			}
		}
		for _, message := range request.Messages {
			for _, block := range message.Content {
				if block.Type == llm.BlockToolResult && block.ToolUseID == "target-metadata" {
					raw, _ := json.Marshal(block.Content)
					select {
					case p.result <- string(raw):
					default:
					}
				}
			}
		}
	}
	return p.mentionModelFixture.Stream(ctx, request)
}

func (p *targetAssetModel) Complete(ctx context.Context, request llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, request) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func (p *scopePlannerModel) Stream(ctx context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	var planner, scope, coverage, assets bool
	for _, item := range request.Tools {
		planner = planner || item.Name == "add_intent"
		scope = scope || item.Name == "add_task_scope"
		coverage = coverage || item.Name == "list_untested_assets"
		assets = assets || item.Name == "list_assets"
	}
	if planner {
		select {
		case p.tools <- fmt.Sprintf("scope=%t coverage=%t assets=%t", scope, coverage, assets):
		default:
		}
		call := p.calls.Add(1)
		if call == 1 {
			return func(yield func(llm.StreamEvent, error) bool) {
				for _, event := range []llm.StreamEvent{{Type: llm.SEMessageStart}, {Type: llm.SEToolUseStart, ToolID: "scope-model", ToolName: "add_task_scope"}, {Type: llm.SEToolInputJSON, Text: `{"kind":"keyword","value":"model scope fixture","reason":"模型登记"}`}, {Type: llm.SEMessageDelta, StopReason: "tool_use"}, {Type: llm.SEMessageStop}} {
					if !yield(event, nil) {
						return
					}
				}
			}
		}
		for _, message := range request.Messages {
			for _, block := range message.Content {
				if block.Type == llm.BlockToolResult && block.ToolUseID == "scope-model" {
					raw, _ := json.Marshal(block.Content)
					select {
					case p.result <- string(raw):
					default:
					}
				}
				if block.Type == llm.BlockToolResult && block.ToolUseID == "scope-assets" {
					raw, _ := json.Marshal(block.Content)
					select {
					case p.assets <- string(raw):
					default:
					}
				}
				if block.Type == llm.BlockToolResult && block.ToolUseID == "scope-outside" {
					raw, _ := json.Marshal(block.Content)
					select {
					case p.outside <- string(raw):
					default:
					}
				}
				if block.Type == llm.BlockToolResult && block.ToolUseID == "scope-company" {
					raw, _ := json.Marshal(block.Content)
					select {
					case p.company <- string(raw):
					default:
					}
				}
				if block.Type == llm.BlockToolResult && block.ToolUseID == "scope-tech" {
					raw, _ := json.Marshal(block.Content)
					select {
					case p.tech <- string(raw):
					default:
					}
				}
				if block.Type == llm.BlockToolResult && block.ToolUseID == "scope-port" {
					raw, _ := json.Marshal(block.Content)
					select {
					case p.port <- string(raw):
					default:
					}
				}
			}
		}
		if call == 2 {
			return func(yield func(llm.StreamEvent, error) bool) {
				events := []llm.StreamEvent{{Type: llm.SEMessageStart}}
				for _, query := range []struct{ id, input string }{{"scope-assets", `{"dsl":"domain=parity-scope.example","limit":100}`}, {"scope-outside", fmt.Sprintf(`{"id":%d}`, p.outsideID)}, {"scope-company", fmt.Sprintf(`{"dsl":"company_id==%d","limit":100}`, p.companyID)}, {"scope-tech", `{"dsl":"status_code>=400 AND technology=nginx","limit":100}`}, {"scope-port", `{"dsl":"port==22","limit":100}`}} {
					events = append(events, llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: query.id, ToolName: "list_assets"}, llm.StreamEvent{Type: llm.SEToolInputJSON, Text: query.input})
				}
				events = append(events, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"}, llm.StreamEvent{Type: llm.SEMessageStop})
				for _, event := range events {
					if !yield(event, nil) {
						return
					}
				}
			}
		}
	}
	return p.mentionModelFixture.Stream(ctx, request)
}

func (p *scopePlannerModel) Complete(ctx context.Context, request llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, request) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func verifyARTEXTaskScopePostgres(t *testing.T, ctx context.Context, db *sql.DB, dsn string) {
	t.Helper()
	owner, target, targetAsset := taskFixtureScope(t, ctx, db, "parity-scope.example")
	_, otherTarget, otherAsset := taskFixtureScope(t, ctx, db, "company-only.example")
	_, limitedTarget, limitedAsset := taskFixtureScope(t, ctx, db, "limited-scope.example")
	var otherUser, org, secondOrg int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&otherUser); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO organization(name) VALUES('Scope Fixture Organization') RETURNING id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO organization(name) VALUES('Scope Second Organization') RETURNING id`).Scan(&secondOrg); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO organization_target(organization_id,target_id) VALUES($1,$2),($1,$3)`, org, target, otherTarget); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO organization_target(organization_id,target_id) VALUES($1,$2)`, secondOrg, otherTarget); err != nil {
		t.Fatal(err)
	}
	var overlap int
	if err := db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES('api.parity-scope.example','domain') RETURNING id`).Scan(&overlap); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO organization_target(organization_id,target_id) VALUES($1,$2)`, secondOrg, overlap); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE website SET title='target A evidence',status_code=200 WHERE target_id=$1 AND url='https://api.parity-scope.example/'`, target); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO website(target_id,url,title,status_code) VALUES($1,'https://api.parity-scope.example/','target B private evidence',403)`, overlap); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO website(target_id,url,title,status_code,tech) VALUES($1,'https://tech.parity-scope.example/','Search Fixture',403,ARRAY['nginx'])`, target); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO host_port_mapping(target_id,host,ip,port) VALUES($1,'scan.parity-scope.example','198.51.100.45',22),($1,'scan.parity-scope.example','198.51.100.45',1883)`, target); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := airepo.NewTaskStore(orm)
	facade := aiapp.NewTaskFacade(store, nil, nil)
	registry := assetapp.NewRegistrationFacade(assetrepo.NewAssetRegistrationStore(orm, assetapp.LegacyAssetCreators{}), nil)
	registered, err := registry.Register(ctx, assetapp.BatchRegisterInput{UserID: owner, Origin: "manual", Assets: []assetapp.AssetInput{
		{Type: "subdomain", Domain: "www.parity-scope.example"},
		{Type: "service", URL: "https://www.parity-scope.example/path"},
		{Type: "ip", IP: "192.0.2.17"},
		{Type: "ip", IP: "192.0.3.17"},
		{Type: "app", AppName: "scope-icp-fixture", AppICP: "京 ICP 12345 号"},
	}})
	if err != nil || len(registered.Errors) != 0 || len(registered.Results) != 5 {
		t.Fatalf("register scope fixtures: %+v %v", registered, err)
	}
	assetID := func(index int) int64 {
		t.Helper()
		id, err := strconv.ParseInt(registered.Results[index].ID, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	subdomain, service, ip, outsideIP, app := assetID(0), assetID(1), assetID(2), assetID(3), assetID(4)
	sharedRegistrations, err := registry.Register(ctx, assetapp.BatchRegisterInput{UserID: owner, Origin: "manual", Assets: []assetapp.AssetInput{
		{Type: "app", AppName: "cross-target-app", TargetID: target, BoundDomains: []string{"only.parity-scope.example"}},
		{Type: "app", AppName: "cross-target-app", TargetID: otherTarget, BoundDomains: []string{"only.company-only.example"}},
	}})
	if err != nil || len(sharedRegistrations.Results) != 2 || len(sharedRegistrations.Errors) != 0 || sharedRegistrations.Results[0].ID != sharedRegistrations.Results[1].ID {
		t.Fatalf("shared asset registrations: %+v %v", sharedRegistrations, err)
	}
	sharedID, err := strconv.ParseInt(sharedRegistrations.Results[0].ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	foreignScope, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "不同目标范围"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = facade.AddScope(ctx, owner, foreignScope.ID, aiapp.TaskScopeInput{Kind: "subdomain", Value: "only.company-only.example"}); err != nil {
		t.Fatal(err)
	}
	strictScope, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "限定证据来源", TargetID: target, AssetIDs: []int64{targetAsset}, SourceTaskIDs: []int64{foreignScope.ID}})
	if err != nil {
		t.Fatal(err)
	}
	var crossed bool
	if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tianqiong_ai.task_scope_assets($1,$2) WHERE asset_id=$3)`, strictScope.ID, owner, sharedID).Scan(&crossed); err != nil || crossed {
		t.Fatalf("other target evidence widened scope: %t %v", crossed, err)
	}
	strictGraphView, err := facade.CoverageGraph(ctx, owner, strictScope.ID)
	if err != nil {
		t.Fatal(err)
	}
	var strictAssetFound bool
	for _, node := range strictGraphView.Nodes {
		if node.AssetID == targetAsset {
			strictAssetFound = node.PageTitle == "target A evidence"
		}
		if node.CompanyID == int64(secondOrg) {
			t.Fatal("other target organization appeared in coverage graph")
		}
	}
	if !strictAssetFound {
		t.Fatalf("coverage graph used wrong target evidence: %+v", strictGraphView.Nodes)
	}
	source, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "范围来源", Goal: "范围测试"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := facade.AddScope(ctx, owner, source.ID, aiapp.TaskScopeInput{Kind: "root_domain", Value: "https://WWW.Parity-Scope.Example/path", Reason: "授权"})
	if err != nil || root.Domain != "parity-scope.example" || root.Source != "manual" {
		t.Fatalf("normalize root: %+v %v", root, err)
	}
	duplicate, err := facade.AddScope(ctx, owner, source.ID, aiapp.TaskScopeInput{Kind: "root_domain", Value: "parity-scope.example"})
	if err != nil || duplicate.ID != root.ID || duplicate.Reason != "授权" {
		t.Fatalf("scope idempotence: %+v %v", duplicate, err)
	}
	if _, err = facade.AddScope(ctx, owner, source.ID, aiapp.TaskScopeInput{Kind: "cidr", Value: "192.0.2.19/24"}); err != nil {
		t.Fatal(err)
	}
	if _, err = facade.AddScope(ctx, owner, source.ID, aiapp.TaskScopeInput{Kind: "icp", Value: "京 ICP 12345 号"}); err != nil {
		t.Fatal(err)
	}
	company, err := facade.AddScope(ctx, owner, source.ID, aiapp.TaskScopeInput{Kind: "company", Value: "Scope Fixture Organization"})
	if err != nil || company.CompanyName != "Scope Fixture Organization" || company.OrganizationID == nil || *company.OrganizationID != org {
		t.Fatalf("company name: %+v %v", company, err)
	}
	if _, err = facade.AddScope(ctx, otherUser, source.ID, aiapp.TaskScopeInput{Kind: "subdomain", Value: "evil.example"}); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign scope write: %v", err)
	}
	if _, err = facade.AddScope(ctx, owner, source.ID, aiapp.TaskScopeInput{Kind: "cidr", Value: "192.0.2.1/99"}); !errors.Is(err, aiapp.ErrInvalidInput) {
		t.Fatalf("invalid scope: %v", err)
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM ai_task_scope WHERE id=$1`, root.ID); err == nil {
		t.Fatal("database accepted unfenced scope deletion")
	}
	child, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "继承范围", SourceTaskIDs: []int64{source.ID}})
	if err != nil {
		t.Fatal(err)
	}
	scopes, err := facade.Scopes(ctx, owner, child.ID)
	if err != nil || len(scopes) != 4 || !scopes[0].Inherited || scopes[0].SourceTaskID != source.ID {
		t.Fatalf("source scopes: %+v %v", scopes, err)
	}
	if err = facade.DeleteScope(ctx, owner, child.ID, root.ID); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("deleted inherited scope: %v", err)
	}
	if _, err = facade.Coverage(ctx, otherUser, child.ID); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign coverage: %v", err)
	}
	checkIDs := func(taskID int64, expected, excluded []int64) {
		t.Helper()
		rows, err := db.QueryContext(ctx, `SELECT asset_id FROM tianqiong_ai.task_scope_assets($1,$2)`, taskID, owner)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		found := map[int64]bool{}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			found[id] = true
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		for _, id := range expected {
			if !found[id] {
				t.Fatalf("task %d missing asset %d in %v", taskID, id, found)
			}
		}
		for _, id := range excluded {
			if found[id] {
				t.Fatalf("task %d included outside asset %d", taskID, id)
			}
		}
	}
	checkIDs(child.ID, []int64{targetAsset, otherAsset, subdomain, service, ip, app}, []int64{outsideIP, limitedAsset})
	listed, err := facade.Assets(ctx, owner, child.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	listedIDs := map[int64]bool{}
	for _, item := range listed {
		listedIDs[item.ID] = true
	}
	if !listedIDs[subdomain] || !listedIDs[otherAsset] || listedIDs[outsideIP] {
		t.Fatalf("scope asset page: %+v", listed)
	}
	coverage, err := facade.Coverage(ctx, owner, child.ID)
	if err != nil || !coverage.Enabled || coverage.ScopeRows != 4 || coverage.Denominator < 6 || coverage.Tested != 0 {
		t.Fatalf("coverage: %+v %v", coverage, err)
	}
	limited, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "限定目标", TargetID: limitedTarget, AssetIDs: []int64{limitedAsset}, SourceTaskIDs: []int64{source.ID}})
	if err != nil {
		t.Fatal(err)
	}
	checkIDs(limited.ID, []int64{limitedAsset}, []int64{targetAsset, otherAsset, subdomain, service, ip, app})
	listed, err = facade.Assets(ctx, owner, limited.ID, 0)
	if err != nil || len(listed) != 1 || listed[0].ID != limitedAsset {
		t.Fatalf("selected target asset page: %+v %v", listed, err)
	}
	disabled := false
	withoutCoverage, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "关闭覆盖度", CoverageEnabled: &disabled})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = facade.AddScope(ctx, owner, withoutCoverage.ID, aiapp.TaskScopeInput{Kind: "subdomain", Value: "www.parity-scope.example"}); err != nil {
		t.Fatal(err)
	}
	cov, err := facade.Coverage(ctx, owner, withoutCoverage.ID)
	if err != nil || cov.Enabled || cov.Denominator != 0 {
		t.Fatalf("disabled coverage: %+v %v", cov, err)
	}

	if _, err = store.ControlTask(ctx, owner, source.ID, "start", false); err != nil {
		t.Fatal(err)
	}
	var run *aiapp.Task
	for attempts := 0; attempts < 100; attempts++ {
		run, err = store.ClaimTask(ctx, uuid.NewString())
		if err != nil || run == nil {
			t.Fatalf("claim scope task: %+v %v", run, err)
		}
		if run.ID == source.ID {
			break
		}
		if err = store.FinishTask(ctx, run, "paused", "fixture cleanup"); err != nil {
			t.Fatal(err)
		}
		run = nil
	}
	if run == nil || run.ID != source.ID {
		t.Fatal("scope task was not claimed")
	}
	run, err = store.StampTaskStart(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = facade.AddAgentScope(ctx, owner, source.ID, uuid.NewString(), aiapp.TaskScopeInput{Kind: "ip", Value: "203.0.113.1"}); !errors.Is(err, aiapp.ErrRunLeaseLost) {
		t.Fatalf("stale scope run: %v", err)
	}
	agentScope, err := facade.AddAgentScope(ctx, owner, source.ID, run.ActiveRun, aiapp.TaskScopeInput{Kind: "ip", Value: "[2001:db8::1]:443", Reason: "Agent 发现"})
	if err != nil || agentScope.Source != "agent" || agentScope.Net != "2001:db8::1/128" {
		t.Fatalf("agent scope: %+v %v", agentScope, err)
	}
	connector, err := airuntime.NewTaskGraphConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := connector.OpenForExecution(ctx, taskExecution(run))
	if err != nil {
		t.Fatal(err)
	}
	graph := (&artexdb.DB{DB: conn}).Exploration(source.ExplorationID)
	if _, err = graph.AddNode("fact", map[string]any{"summary": "已确认"}, 0, "confirmed", "worker", []int64{subdomain}); err != nil {
		t.Fatal(err)
	}
	batch := assetapp.BatchRegisterInput{UserID: owner, Origin: "ai", RequestID: "scope-auto-" + uuid.NewString(), Task: &assetapp.RegistrationTaskScope{TaskID: source.ID, ExplorationID: source.ExplorationID, ExecutionID: run.ActiveRun}, Assets: []assetapp.AssetInput{{Type: "service", URL: "https://auto.parity-scope.example/"}}}
	for i := 0; i < 2; i++ {
		result, err := registry.Register(ctx, batch)
		if err != nil || len(result.Errors) != 0 || len(result.Results) != 1 {
			t.Fatalf("automatic scope registration: %+v %v", result, err)
		}
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_task_scope WHERE task_id=$1 AND kind='subdomain' AND domain='auto.parity-scope.example' AND source='auto'`, source.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("auto scope not idempotent: %d %v", count, err)
	}
	coverage, err = facade.Coverage(ctx, owner, child.ID)
	if err != nil || coverage.Tested != 1 || coverage.Pct == nil || coverage.ScopeRows != 6 {
		t.Fatalf("fact coverage: %+v %v", coverage, err)
	}
	coverageGraph, err := facade.CoverageGraph(ctx, owner, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	assetKey := func(id int64) string { return fmt.Sprintf("a:%d", id) }
	graphNodes := map[string]aiapp.CoverageGraphNode{}
	for _, node := range coverageGraph.Nodes {
		graphNodes[node.Key] = node
	}
	if !graphNodes[assetKey(subdomain)].Tested || graphNodes[assetKey(service)].Kind != "service" || graphNodes[fmt.Sprintf("c:%d", org)].Label != "Scope Fixture Organization" {
		t.Fatalf("coverage graph nodes: %+v", coverageGraph.Nodes)
	}
	var serviceLinked bool
	for _, edge := range coverageGraph.Edges {
		if edge.Src == assetKey(service) && edge.Dst == assetKey(subdomain) {
			serviceLinked = true
		}
	}
	if !serviceLinked {
		t.Fatalf("coverage graph missing service parent: %+v", coverageGraph.Edges)
	}
	refs, err := facade.AssetRefs(ctx, owner, child.ID, subdomain)
	if err != nil || len(refs.Facts) != 1 || !refs.Facts[0].Inherited || refs.Facts[0].SourceTaskID != source.ID {
		t.Fatalf("coverage graph source refs: %+v %v", refs, err)
	}
	if _, err = facade.CoverageGraph(ctx, otherUser, child.ID); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign coverage graph: %v", err)
	}
	if _, err = facade.AssetRefs(ctx, owner, limited.ID, subdomain); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("out-of-target graph refs: %v", err)
	}
	if _, err = graph.AddGoal(map[string]any{"text": "模型登记范围"}, "human"); err != nil {
		t.Fatal(err)
	}
	model := &scopePlannerModel{tools: make(chan string, 1), result: make(chan string, 1), assets: make(chan string, 1), outside: make(chan string, 1), company: make(chan string, 1), tech: make(chan string, 1), port: make(chan string, 1), outsideID: outsideIP, companyID: secondOrg}
	execution := taskExecution(run)
	execution.CoverageEnabled = &disabled
	runner := airuntime.NewTaskRunner(connector, t.TempDir(), func(user int) []tool.CoreTool { return aiinfra.TaskScopeTools(facade, user) })
	runCtx, cancelRun := context.WithCancel(ctx)
	completed := make(chan struct{})
	type scopeOutcome struct {
		result airuntime.TaskOutcome
		err    error
	}
	outcomes := make(chan scopeOutcome, 1)
	go func() {
		defer close(completed)
		result, err := runner.RunTask(runCtx, execution, model, "fixture")
		outcomes <- scopeOutcome{result, err}
	}()
	failScope := func(message string) {
		t.Helper()
		select {
		case out := <-outcomes:
			t.Fatalf("%s; calls=%d outcome=%+v", message, model.calls.Load(), out)
		default:
			t.Fatalf("%s; calls=%d runner still active", message, model.calls.Load())
		}
	}
	select {
	case got := <-model.tools:
		if got != "scope=true coverage=false assets=true" {
			cancelRun()
			t.Fatalf("model-facing disabled coverage tools: %s", got)
		}
	case <-time.After(8 * time.Second):
		cancelRun()
		t.Fatal("planner did not receive scope tool")
	}
	select {
	case got := <-model.result:
		if !strings.Contains(got, "model scope fixture") || strings.Contains(got, "errors") {
			cancelRun()
			t.Fatalf("model scope result: %s", got)
		}
	case <-time.After(8 * time.Second):
		cancelRun()
		t.Fatal("planner did not execute scope tool")
	}
	select {
	case got := <-model.assets:
		if !strings.Contains(got, "www.parity-scope.example") || strings.Contains(got, "192.0.3.17") {
			cancelRun()
			t.Fatalf("DSL scoped search: %s", got)
		}
	case <-time.After(8 * time.Second):
		cancelRun()
		t.Fatal("planner did not execute DSL asset search")
	}
	select {
	case got := <-model.outside:
		if strings.Contains(got, "192.0.3.17") || !strings.Contains(got, `count`) {
			cancelRun()
			t.Fatalf("direct asset lookup escaped scope: %s", got)
		}
	case <-time.After(8 * time.Second):
		cancelRun()
		t.Fatal("planner did not execute direct asset lookup")
	}
	select {
	case got := <-model.company:
		if !strings.Contains(got, "api.company-only.example") {
			cancelRun()
			t.Fatalf("multi-organization DSL: %s", got)
		}
	case <-time.After(8 * time.Second):
		cancelRun()
		t.Fatal("planner did not search by organization")
	}
	select {
	case got := <-model.tech:
		if !strings.Contains(got, "tech.parity-scope.example") {
			cancelRun()
			t.Fatalf("numeric and technology DSL: %s", got)
		}
	case <-time.After(8 * time.Second):
		cancelRun()
		t.Fatal("planner did not search by technology")
	}
	select {
	case got := <-model.port:
		if !strings.Contains(got, "198.51.100.45") || !strings.Contains(got, "1883") {
			cancelRun()
			t.Fatalf("scanned host port search: %s", got)
		}
	case <-time.After(8 * time.Second):
		cancelRun()
		failScope("planner did not search scanned ports")
	}
	cancelRun()
	select {
	case <-completed:
	case <-time.After(8 * time.Second):
		t.Fatal("scope planner did not stop")
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_task_scope WHERE task_id=$1 AND kind='keyword' AND value='model scope fixture' AND source='agent'`, source.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("model scope not persisted: %d %v", count, err)
	}
	if err = store.FinishTask(ctx, run, "paused", ""); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	result, err := registry.Register(ctx, batch)
	if err != nil || len(result.Errors) != 1 {
		t.Fatalf("stale registration retry: %+v %v", result, err)
	}
	if _, err = facade.AddAgentScope(ctx, owner, source.ID, run.ActiveRun, aiapp.TaskScopeInput{Kind: "ip", Value: "203.0.113.1"}); !errors.Is(err, aiapp.ErrRunLeaseLost) {
		t.Fatalf("finished scope run: %v", err)
	}
	if _, err = store.ControlTask(ctx, owner, strictScope.ID, "start", false); err != nil {
		t.Fatal(err)
	}
	var strictRun *aiapp.Task
	for attempts := 0; attempts < 100; attempts++ {
		strictRun, err = store.ClaimTask(ctx, uuid.NewString())
		if err != nil || strictRun == nil {
			t.Fatalf("claim strict search: %+v %v", strictRun, err)
		}
		if strictRun.ID == strictScope.ID {
			break
		}
		if err = store.FinishTask(ctx, strictRun, "paused", "fixture cleanup"); err != nil {
			t.Fatal(err)
		}
		strictRun = nil
	}
	if strictRun == nil || strictRun.ID != strictScope.ID {
		t.Fatal("strict search task not claimed")
	}
	strictRun, err = store.StampTaskStart(ctx, strictRun)
	if err != nil {
		t.Fatal(err)
	}
	strictConn, err := connector.OpenForExecution(ctx, taskExecution(strictRun))
	if err != nil {
		t.Fatal(err)
	}
	strictGraph := (&artexdb.DB{DB: strictConn}).Exploration(strictScope.ExplorationID)
	if _, err = strictGraph.AddGoal(map[string]any{"text": "查询本目标证据"}, "human"); err != nil {
		t.Fatal(err)
	}
	strictModel := &targetAssetModel{assetID: targetAsset, result: make(chan string, 1)}
	strictCtx, cancelStrict := context.WithCancel(ctx)
	strictDone := make(chan struct{})
	strictWorkspace := t.TempDir()
	go func() {
		defer close(strictDone)
		_, _ = airuntime.NewTaskRunner(connector, strictWorkspace, nil).RunTask(strictCtx, taskExecution(strictRun), strictModel, "fixture")
	}()
	select {
	case got := <-strictModel.result:
		if !strings.Contains(got, "target A evidence") || strings.Contains(got, "target B private evidence") {
			cancelStrict()
			t.Fatalf("cross-target metadata exposed: %s", got)
		}
		var blocks []struct {
			Text string `json:"text"`
		}
		var data struct {
			Assets []struct {
				CompanyIDs []int `json:"company_ids"`
			} `json:"assets"`
		}
		if err = json.Unmarshal([]byte(got), &blocks); err != nil || len(blocks) != 1 {
			cancelStrict()
			t.Fatalf("asset tool output: %s %v", got, err)
		}
		if err = json.Unmarshal([]byte(blocks[0].Text), &data); err != nil || len(data.Assets) != 1 {
			cancelStrict()
			t.Fatalf("asset JSON: %s %v", blocks[0].Text, err)
		}
		for _, id := range data.Assets[0].CompanyIDs {
			if id == secondOrg {
				cancelStrict()
				t.Fatalf("cross-target organization exposed: %s", got)
			}
		}
	case <-time.After(8 * time.Second):
		cancelStrict()
		t.Fatal("target-scoped asset search did not run")
	}
	cancelStrict()
	select {
	case <-strictDone:
	case <-time.After(8 * time.Second):
		t.Fatal("strict search runner did not stop")
	}
	if err = store.FinishTask(ctx, strictRun, "paused", ""); err != nil {
		t.Fatal(err)
	}
	strictConn.Close()
	if err = facade.DeleteScope(ctx, owner, source.ID, root.ID); err != nil {
		t.Fatalf("delete own scope: %v", err)
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM organization WHERE id=$1`, org); err != nil {
		t.Fatalf("organization scope cascade: %v", err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_task_scope WHERE id=$1`, company.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("scope cascade: %d %v", count, err)
	}
}

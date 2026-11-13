package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	serverdatabase "github.com/yyhuni/lunafox/server/internal/database"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	settingsrepo "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	aiinfra "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/infrastructure"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestARTEXTaskInheritanceIsolatedPostgres(t *testing.T) {
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
	verifyARTEXTaskInheritancePostgres(t, ctx, db, dsn)
	verifyARTEXSeedWorkflowPostgres(t, ctx, db, dsn)
	verifyARTEXTaskScopePostgres(t, ctx, db, dsn)
	verifyARTEXIntentDeletePostgres(t, ctx, db, dsn)
	verifyARTEXMainSessionsPostgres(t, ctx, db, dsn)
	verifyARTEXIndependentMainChatPostgres(t, ctx, db, dsn)
	verifyARTEXTrafficCapturePostgres(t, ctx, db, dsn)
	verifyARTEXConversationTrafficPostgres(t, ctx, db)
	verifyARTEXGlobalTrafficPostgres(t, ctx, db)
	verifyARTEXGlobalAssetDSLPostgres(t, ctx, db)
	verifyARTEXCredentialTrafficPostgres(t, ctx, db)
	verifyARTEXTaskIntelligencePostgres(t, ctx, db, dsn)
	verifyARTEXCoverageGraphScalePostgres(t, ctx, db)
	verifyARTEXLLMUsagePostgres(t, ctx, db)
	verifyARTEXTaskFindingsPostgres(t, ctx, db, dsn)
	verifyARTEXSpawnSchemaUpgrade(t, ctx, db)
}

func verifyARTEXSpawnSchemaUpgrade(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	// Exercise the 000055 upgrade even when newer migrations are present.
	for {
		var version int
		if err := db.QueryRowContext(ctx, `SELECT version FROM schema_migrations`).Scan(&version); err != nil {
			t.Fatal(err)
		}
		if version <= 54 {
			break
		}
		if err := serverdatabase.MigrateDown(db); err != nil {
			t.Fatal(err)
		}
	}
	oldSchema := `{"type":"object","properties":{"description":{"type":"string"},"goal":{"type":"string"},"timeout_seconds":{"type":"integer","minimum":30,"maximum":604800,"description":"用户说明","default":42}},"required":["description","goal"]}`
	if _, err := db.ExecContext(ctx, `INSERT INTO ai_tool_catalog(tenant_id,key,system,description,schema,agents,enabled,kind) VALUES('00000000-0000-0000-0000-000000000001','spawn_task',TRUE,'用户自定义说明',$1::jsonb,'["auto"]'::jsonb,FALSE,'builtin')`, oldSchema); err != nil {
		t.Fatal(err)
	}
	if err := serverdatabase.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	var description string
	var enabled bool
	if err := db.QueryRowContext(ctx, `SELECT schema,description,enabled FROM ai_tool_catalog WHERE key='spawn_task'`).Scan(&raw, &description, &enabled); err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok || props["source_task_ids"] == nil || props["seed_first_intent"] == nil || props["llm_profile_id"] == nil || props["plan_heartbeat_seconds"] == nil {
		t.Fatalf("spawn schema was not upgraded: %s", raw)
	}
	timeout, ok := props["timeout_seconds"].(map[string]any)
	if !ok || timeout["minimum"] != float64(0) || timeout["maximum"] != float64(2147483647) || timeout["description"] != "用户说明" || timeout["default"] != float64(42) || description != "用户自定义说明" || enabled {
		t.Fatalf("spawn schema upgrade lost user configuration: %s %q %t", raw, description, enabled)
	}
	if _, err := db.ExecContext(ctx, `UPDATE ai_tool_catalog SET enabled=TRUE WHERE key='spawn_task'`); err != nil {
		t.Fatal(err)
	}
	var user int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	facade := aiapp.NewTaskFacade(airepo.NewTaskStore(orm), nil, nil)
	tools := aiinfra.AutoTaskWriteTools(facade, user)
	catalog := settingsapp.NewToolCatalogFacade(settingsrepo.NewToolCatalogStore(orm), nil, nil, nil)
	resolved, err := catalog.Resolve(ctx, user, "auto", tools)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, item := range resolved {
		if item.Name() == "spawn_task" {
			properties, _ := item.InputSchema()["properties"].(map[string]any)
			found = properties["source_task_ids"] != nil && properties["seed_first_intent"] != nil
		}
	}
	if !found {
		t.Fatal("resolved Auto tool did not expose upgraded parameters")
	}
}

type inheritanceModelFixture struct {
	mentionModelFixture
	workerCalls atomic.Int64
	planner     chan string
	untested    chan string
}

func (p *inheritanceModelFixture) Stream(ctx context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	role := ""
	for _, item := range request.Tools {
		if item.Name == "add_intent" {
			role = "planner"
			break
		}
		if item.Name == "record_fact" {
			role = "worker"
		}
	}
	if role == "planner" {
		var input string
		for _, message := range request.Messages {
			input += message.Text()
		}
		select {
		case p.planner <- input:
		default:
		}
	}
	if role == "worker" && p.workerCalls.Add(1) == 1 {
		return func(yield func(llm.StreamEvent, error) bool) {
			for _, event := range []llm.StreamEvent{{Type: llm.SEMessageStart}, {Type: llm.SEToolUseStart, ToolID: "source-untested", ToolName: "list_untested_assets"}, {Type: llm.SEToolInputJSON, Text: `{"type":"service"}`}, {Type: llm.SEMessageDelta, StopReason: "tool_use"}, {Type: llm.SEMessageStop}} {
				if !yield(event, nil) {
					return
				}
			}
		}
	}
	if role == "worker" {
		for _, message := range request.Messages {
			for _, block := range message.Content {
				if block.Type == llm.BlockToolResult && block.ToolUseID == "source-untested" {
					raw, _ := json.Marshal(block.Content)
					select {
					case p.untested <- string(raw):
					default:
					}
				}
			}
		}
	}
	return p.mentionModelFixture.Stream(ctx, request)
}
func (p *inheritanceModelFixture) Complete(ctx context.Context, request llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, request) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func verifyARTEXTaskInheritancePostgres(t *testing.T, ctx context.Context, db *sql.DB, dsn string) {
	t.Helper()
	owner, target, asset := taskFixtureScope(t, ctx, db, "inheritance.example")
	var other, website int
	var untestedAsset int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO website(target_id,url) VALUES($1,'https://untested.inheritance.example/') RETURNING id`, target).Scan(&website); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT asset_id FROM asset_website_binding WHERE website_id=$1`, website).Scan(&untestedAsset); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := airepo.NewTaskStore(orm)
	facade := aiapp.NewTaskFacade(store, nil, nil)
	connector, err := airuntime.NewTaskGraphConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	start := func(task *aiapp.Task) (*aiapp.Task, *sql.DB, *artexdb.ExplorationStore) {
		t.Helper()
		if _, err := store.ControlTask(ctx, owner, task.ID, "start", false); err != nil {
			t.Fatal(err)
		}
		var run *aiapp.Task
		var err error
		for attempts := 0; attempts < 100; attempts++ {
			run, err = store.ClaimTask(ctx, uuid.NewString())
			if err != nil || run == nil {
				t.Fatalf("claim inheritance task: %+v %v", run, err)
			}
			if run.ID == task.ID {
				break
			}
			// The shared migration suite may leave earlier fixture tasks queued.
			// Settle only those isolated test rows before claiming this fixture.
			if err = store.FinishTask(ctx, run, "paused", "fixture cleanup"); err != nil {
				t.Fatal(err)
			}
			run = nil
		}
		if run == nil || run.ID != task.ID {
			t.Fatal("inheritance fixture was not claimed")
		}
		run, err = store.StampTaskStart(ctx, run)
		if err != nil {
			t.Fatal(err)
		}
		conn, err := connector.OpenForExecution(ctx, taskExecution(run))
		if err != nil {
			t.Fatal(err)
		}
		return run, conn, (&artexdb.DB{DB: conn}).Exploration(task.ExplorationID)
	}
	grand, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "间接来源", Goal: "旧任务"})
	if err != nil {
		t.Fatal(err)
	}
	grandRun, grandConn, grandGraph := start(grand)
	grandFact, err := grandGraph.AddNode("fact", map[string]any{"summary": "不可递归继承的事实"}, 0, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.FinishTask(ctx, grandRun, "done", ""); err != nil {
		t.Fatal(err)
	}
	grandConn.Close()
	source, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "直接来源", Goal: "已有结果", TargetID: target, AssetIDs: []int64{asset, untestedAsset}, SourceTaskIDs: []int64{grand.ID}})
	if err != nil {
		t.Fatal(err)
	}
	sourceRun, sourceConn, sourceGraph := start(source)
	defer sourceConn.Close()
	fact, err := sourceGraph.AddNode("fact", map[string]any{"summary": "来源确认事实"}, 0, "confirmed", "worker", []int64{asset})
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := sourceGraph.AddIntent(map[string]any{"summary": "来源已结束意图"}, 1, []int64{asset}, "planner")
	if err != nil {
		t.Fatal(err)
	}
	if err = sourceGraph.SetIntentState(terminal, "done"); err != nil {
		t.Fatal(err)
	}
	openIntent, err := sourceGraph.AddIntent(map[string]any{"summary": "来源未结束意图"}, 1, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	textID, err := sourceGraph.AppendActivity(artexdb.Activity{NodeID: &terminal, Worker: "worker", Kind: "text", Summary: "来源公开执行", Detail: "来源公开执行详情"})
	if err != nil {
		t.Fatal(err)
	}
	thinkingID, err := sourceGraph.AppendActivity(artexdb.Activity{NodeID: &terminal, Worker: "worker", Kind: "thinking", Summary: "不继承思考", Detail: "不继承思考"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sourceGraph.AppendActivity(artexdb.Activity{NodeID: &terminal, Worker: "worker", Kind: "usage", Summary: "不继承计量", Detail: "不继承计量"}); err != nil {
		t.Fatal(err)
	}
	foreign, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: other, Name: "其他用户任务", Goal: "私有"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "不允许", SourceTaskIDs: []int64{foreign.ID}}); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign source task: %v", err)
	}
	if _, err = facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "不允许", SourceTaskIDs: []int64{source.ID, source.ID}}); !errors.Is(err, aiapp.ErrInvalidInput) {
		t.Fatalf("duplicate source task: %v", err)
	}
	child, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "继承任务", Goal: "后续验证", SourceTaskIDs: []int64{source.ID}, WorkerCount: 1})
	if err != nil || len(child.SourceTaskIDs) != 1 || child.SourceTaskIDs[0] != source.ID {
		t.Fatalf("create task with source: %+v %v", child, err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_task_relation(tenant_id,task_id,source_task_id) VALUES($1,$2,$3)`, child.TenantID, child.ID, foreign.ID); err == nil {
		t.Fatal("database accepted a cross-owner task relation")
	}
	graph, err := facade.Graph(ctx, owner, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundFact, foundTerminal := false, false
	for _, node := range graph.Nodes {
		if node.ID == fact {
			foundFact = node.Inherited && node.SourceTaskID == source.ID
		}
		if node.ID == terminal {
			foundTerminal = node.Inherited && node.SourceTaskID == source.ID
		}
		if node.ID == openIntent || node.ID == grandFact {
			t.Fatalf("inherited live or indirect node: %+v", node)
		}
	}
	if !foundFact || !foundTerminal {
		t.Fatalf("missing inherited nodes: %+v", graph.Nodes)
	}
	assets, err := facade.Assets(ctx, owner, child.ID, 0)
	if err != nil || len(assets) != 2 || !assets[0].Inherited || assets[0].SourceTaskID != source.ID {
		t.Fatalf("inherited assets: %+v %v", assets, err)
	}
	trace, err := facade.Trace(ctx, owner, child.ID, terminal, nil)
	if err != nil || len(trace) != 1 || trace[0].ID != textID {
		t.Fatalf("inherited terminal trace: %+v %v", trace, err)
	}
	if detail, err := facade.ActivityDetail(ctx, owner, child.ID, textID); err != nil || detail != "来源公开执行详情" {
		t.Fatalf("inherited activity detail: %q %v", detail, err)
	}
	if _, err = facade.ActivityDetail(ctx, owner, child.ID, thinkingID); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("inherited thinking exposed: %v", err)
	}
	_, limitedTarget, limitedAsset := taskFixtureScope(t, ctx, db, "limited-inheritance.example")
	limited, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "限定目标", TargetID: limitedTarget, AssetIDs: []int64{limitedAsset}, SourceTaskIDs: []int64{source.ID}, WorkerCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	limitedRun, limitedConn, limitedGraph := start(limited)
	if _, err = limitedGraph.AddIntent(map[string]any{"summary": "检查限定目标"}, 1, nil, "planner"); err != nil {
		t.Fatal(err)
	}
	limitedProvider := &inheritanceModelFixture{planner: make(chan string, 1), untested: make(chan string, 1)}
	limitedCtx, cancelLimited := context.WithCancel(ctx)
	limitedDone := make(chan struct{})
	limitedWorkspace := t.TempDir()
	go func() {
		_, _ = airuntime.NewTaskRunner(connector, limitedWorkspace, nil).RunTask(limitedCtx, taskExecution(limitedRun), limitedProvider, "fixture")
		close(limitedDone)
	}()
	select {
	case value := <-limitedProvider.untested:
		if !strings.Contains(value, "api.limited-inheritance.example") || strings.Contains(value, "untested.inheritance.example") {
			t.Fatalf("source assets escaped the selected target: %s", value)
		}
	case <-time.After(8 * time.Second):
		cancelLimited()
		t.Fatal("limited worker did not query assets")
	}
	cancelLimited()
	select {
	case <-limitedDone:
	case <-time.After(8 * time.Second):
		t.Fatal("limited task did not stop")
	}
	if err = store.FinishTask(ctx, limitedRun, "paused", ""); err != nil {
		t.Fatal(err)
	}
	limitedConn.Close()
	childRun, childConn, childGraph := start(child)
	defer childConn.Close()
	proofGoal, err := childGraph.AddGoal(map[string]any{"text": "继承证据证明目标"}, "human")
	if err != nil {
		t.Fatal(err)
	}
	stolen := (&artexdb.DB{DB: childConn}).Exploration(source.ExplorationID)
	if err = stolen.SetNodeState(fact, "dismissed"); err == nil {
		t.Fatal("child execution modified a source node")
	}
	tools := agent.NewToolSet(childGraph, "planner")
	tools.SetTaskID(child.ID)
	var addIntent, proveGoal tool.CoreTool
	for _, item := range tools.PlannerTools() {
		if item.Name() == "add_intent" {
			addIntent = item
		}
		if item.Name() == "prove_goal" {
			proveGoal = item
		}
	}
	if addIntent == nil || proveGoal == nil {
		t.Fatal("missing native planner tools")
	}
	input := json.RawMessage(fmt.Sprintf(`{"summary":"根据继承事实验证","parent_ids":[%d]}`, fact))
	result, err := addIntent.Call(ctx, input, nil)
	if err != nil || result.IsError {
		t.Fatalf("native intent from inherited fact: %+v %v", result, err)
	}
	var newIntent int64
	if err = db.QueryRowContext(ctx, `SELECT id FROM tianqiong_ai.exploration_nodes WHERE exploration_id=$1 AND kind='intent' AND payload->>'summary'='根据继承事实验证'`, child.ExplorationID).Scan(&newIntent); err != nil {
		t.Fatal(err)
	}
	var links int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.exploration_edges WHERE exploration_id=$1 AND src_id=$2 AND dst_id=$3 AND rel='derived_from'`, child.ExplorationID, fact, newIntent).Scan(&links); err != nil || links != 1 {
		t.Fatalf("inherited lineage not persisted: %d %v", links, err)
	}
	if err = childGraph.Link(openIntent, "derived_from", newIntent); err == nil {
		t.Fatal("child linked a live inherited intent as a fact")
	}
	if err = childGraph.Link(fact, "yields", newIntent); err == nil {
		t.Fatal("child linked an inherited fact with an invalid relation")
	}
	proofInput := json.RawMessage(fmt.Sprintf(`{"goal_id":%d,"evidence_id":%d,"reason":"来源已有证据"}`, proofGoal, fact))
	proof, err := proveGoal.Call(ctx, proofInput, nil)
	if err != nil || proof.IsError {
		t.Fatalf("native goal proof using inherited fact: %+v %v", proof, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.exploration_edges WHERE exploration_id=$1 AND src_id=$2 AND dst_id=$3 AND rel='proves'`, child.ExplorationID, fact, proofGoal).Scan(&links); err != nil || links != 1 {
		t.Fatalf("inherited goal proof not persisted: %d %v", links, err)
	}
	if _, err = childGraph.AddGoal(map[string]any{"text": "当前任务目标"}, "human"); err != nil {
		t.Fatal(err)
	}
	provider := &inheritanceModelFixture{planner: make(chan string, 1), untested: make(chan string, 1)}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	workspace := t.TempDir()
	go func() {
		_, _ = airuntime.NewTaskRunner(connector, workspace, nil).RunTask(runCtx, taskExecution(childRun), provider, "fixture")
		close(done)
	}()
	defer cancel()
	select {
	case value := <-provider.untested:
		if !strings.Contains(value, "untested.inheritance.example") || strings.Contains(value, "api.inheritance.example") {
			t.Fatalf("inherited untested assets: %s", value)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("worker did not query inherited assets")
	}
	select {
	case value := <-provider.planner:
		if !strings.Contains(value, "来源确认事实") || strings.Contains(value, "不可递归继承的事实") {
			t.Fatalf("planner inherited incorrect context: %s", value)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("planner did not receive inherited context")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("child runtime did not stop")
	}
	if err = sourceGraph.SetIntentState(terminal, "running"); err != nil {
		t.Fatal(err)
	}
	if _, err = facade.Trace(ctx, owner, child.ID, terminal, nil); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("reopened source intent remained visible: %v", err)
	}
	if err = sourceGraph.SetIntentState(terminal, "done"); err != nil {
		t.Fatal(err)
	}
	newAfter, err := childGraph.AddIntent(map[string]any{"summary": "目标删除后检查"}, 1, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE target SET deleted_at=clock_timestamp() WHERE id=$1`, target); err != nil {
		t.Fatal(err)
	}
	if node, err := childGraph.GetNodeWithSources(fact); err != nil || node != nil {
		t.Fatalf("deleted source target remained in native graph: %+v %v", node, err)
	}
	if err = childGraph.Link(fact, "derived_from", newAfter); err == nil {
		t.Fatal("child linked a fact after the source target was deleted")
	}
	loaded, err := facade.Get(ctx, owner, child.ID)
	if err != nil || len(loaded.SourceTaskIDs) != 0 {
		t.Fatalf("deleted source target remained visible: %+v %v", loaded, err)
	}
	if err = store.FinishTask(ctx, childRun, "paused", ""); err != nil {
		t.Fatal(err)
	}
	if err = store.FinishTask(ctx, sourceRun, "paused", ""); err != nil {
		t.Fatal(err)
	}
}

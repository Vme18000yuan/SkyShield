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

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	serverdatabase "github.com/yyhuni/lunafox/server/internal/database"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	settingsrepo "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	aiinfra "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/infrastructure"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type hintPlannerFixture struct{ observed atomic.Bool }

func (p *hintPlannerFixture) Stream(_ context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	raw, _ := json.Marshal(request)
	if strings.Contains(string(raw), "重点检查只读接口") {
		p.observed.Store(true)
	}
	return func(yield func(llm.StreamEvent, error) bool) {
		for _, event := range []llm.StreamEvent{{Type: llm.SEMessageStart}, {Type: llm.SETextDelta, Text: "已收到提示"}, {Type: llm.SEMessageDelta, StopReason: "end_turn"}, {Type: llm.SEMessageStop}} {
			if !yield(event, nil) {
				return
			}
		}
	}
}
func (p *hintPlannerFixture) Complete(ctx context.Context, request llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, request) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func TestARTEXAutoTaskHintsIsolatedPostgres(t *testing.T) {
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
		t.Fatal("test database is not empty")
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
	owner, target, asset := taskFixtureScope(t, ctx, db, "auto-hint.example")
	_, _, foreignAsset := taskFixtureScope(t, ctx, db, "auto-hint-outside.example")
	var other int
	if err = db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := airepo.NewTaskStore(orm)
	facade := aiapp.NewTaskFacade(store, nil, nil)
	task, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "提示任务", Goal: "验证目标", TargetID: target, AssetIDs: []int64{asset}})
	if err != nil {
		t.Fatal(err)
	}
	if err = settingsrepo.NewToolCatalogStore(orm).SeedToolCatalog(ctx, settings.NativeToolSeeds(aiinfra.AutoTaskHintTools(facade, owner, func(context.Context, int) bool { return false }))); err != nil {
		t.Fatal(err)
	}
	conversations := airepo.NewConversationStore(orm)
	conversation, run, instance := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = conversations.CreateAgentConversation(ctx, owner, conversation, "hint guard", "auto"); err != nil {
		t.Fatal(err)
	}
	if err = conversations.BeginRun(ctx, owner, conversation, run, instance, "hint"); err != nil {
		t.Fatal(err)
	}
	mutation := aiapp.AutoTaskMutation{RunID: run, ToolUseID: "hint-batch", InputHash: strings.Repeat("a", 64)}
	items := []aiapp.AutoTaskHint{{Text: "重点检查只读接口", AssetIDs: []int64{asset}}, {Text: "范围外", AssetIDs: []int64{foreignAsset}}, {Text: "  "}}
	result, err := facade.AddAutoTaskHints(ctx, owner, task.ID, mutation, items)
	if err != nil || len(result.IDs) != 3 || result.IDs[0] <= 0 || result.IDs[1] != 0 || result.IDs[2] != 0 || len(result.Errors) != 2 {
		t.Fatalf("partial hint batch: %+v %v", result, err)
	}
	if replay, e := facade.AddAutoTaskHints(ctx, owner, task.ID, mutation, items); e != nil || replay.IDs[0] != result.IDs[0] {
		t.Fatalf("hint replay: %+v %v", replay, e)
	}
	conflict := mutation
	conflict.InputHash = strings.Repeat("b", 64)
	if _, err = facade.AddAutoTaskHints(ctx, owner, task.ID, conflict, items); !errors.Is(err, aiapp.ErrInvalidInput) {
		t.Fatalf("conflicting hint replay: %v", err)
	}
	foreign := mutation
	foreign.ToolUseID = "foreign"
	if _, err = facade.AddAutoTaskHints(ctx, other, task.ID, foreign, items); err == nil {
		t.Fatal("foreign account wrote hints")
	}
	var nodes, anchors, events int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.exploration_nodes WHERE exploration_id=$1 AND kind='hint'`, task.ExplorationID).Scan(&nodes); err != nil || nodes != 1 {
		t.Fatalf("hint node rollback: %d %v", nodes, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.exploration_anchors WHERE node_id=$1 AND asset_id=$2`, result.IDs[0], asset).Scan(&anchors); err != nil || anchors != 1 {
		t.Fatalf("hint anchor: %d %v", anchors, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_task_human_event WHERE task_id=$1 AND kind='hint' AND processed_at IS NULL`, task.ID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("durable planner hint: %d %v", events, err)
	}
	loaded, err := store.GetTask(ctx, owner, task.ID)
	if err != nil || loaded.Queued {
		t.Fatalf("hint unexpectedly started task: %+v %v", loaded, err)
	}
	refs := []aiapp.AutoTaskHint{{Text: "带流量", TrafficRefs: []artexdb.TrafficRef{{TrafficID: "fixture-traffic", Role: "proof"}}}}
	withTraffic := mutation
	withTraffic.ToolUseID = "traffic-disabled"
	withTraffic.InputHash = strings.Repeat("c", 64)
	denied, err := facade.AddAutoTaskHints(ctx, owner, task.ID, withTraffic, refs)
	if err != nil || denied.IDs[0] != 0 || len(denied.Errors) != 1 {
		t.Fatalf("disabled traffic hint: %+v %v", denied, err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_runtime_setting(tenant_id,options) VALUES('00000000-0000-0000-0000-000000000001','{"agent_traffic_binding":true}'::jsonb) ON CONFLICT(tenant_id) DO UPDATE SET options=jsonb_set(ai_runtime_setting.options,'{agent_traffic_binding}','true'::jsonb)`); err != nil {
		t.Fatal(err)
	}
	withTraffic.ToolUseID = "traffic-enabled"
	withTraffic.InputHash = strings.Repeat("d", 64)
	accepted, err := facade.AddAutoTaskHints(ctx, owner, task.ID, withTraffic, refs)
	if err != nil || accepted.IDs[0] <= 0 {
		t.Fatalf("enabled traffic hint: %+v %v", accepted, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.exploration_nodes WHERE id=$1 AND payload->'traffic_refs'->0->>'traffic_id'='fixture-traffic'`, accepted.IDs[0]).Scan(&nodes); err != nil || nodes != 1 {
		t.Fatalf("hint traffic refs: %d %v", nodes, err)
	}
	concurrent := mutation
	concurrent.ToolUseID = "concurrent"
	concurrent.InputHash = strings.Repeat("e", 64)
	type hintResult struct {
		value aiapp.AutoTaskHintResult
		err   error
	}
	concurrentResults := make(chan hintResult, 2)
	for i := 0; i < 2; i++ {
		go func() {
			value, e := facade.AddAutoTaskHints(ctx, owner, task.ID, concurrent, []aiapp.AutoTaskHint{{Text: "并发提示"}})
			concurrentResults <- hintResult{value, e}
		}()
	}
	first, second := <-concurrentResults, <-concurrentResults
	if first.err != nil || second.err != nil || first.value.IDs[0] <= 0 || first.value.IDs[0] != second.value.IDs[0] {
		t.Fatalf("concurrent hint replay: %+v %+v", first, second)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.auto_hint_staging`).Scan(&nodes); err != nil || nodes != 0 {
		t.Fatalf("hint staging was not cleared: %d %v", nodes, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_tool_catalog SET enabled=FALSE WHERE key='add_task_hint'`); err != nil {
		t.Fatal(err)
	}
	blocked := mutation
	blocked.ToolUseID = "disabled"
	if _, err = facade.AddAutoTaskHints(ctx, owner, task.ID, blocked, items); !errors.Is(err, aiapp.ErrRunLeaseLost) {
		t.Fatalf("disabled hint tool ran: %v", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_tool_catalog SET enabled=TRUE WHERE key='add_task_hint'`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE target SET deleted_at=clock_timestamp() WHERE id=$1`, target); err != nil {
		t.Fatal(err)
	}
	blocked.ToolUseID = "deleted-target"
	if _, err = facade.AddAutoTaskHints(ctx, owner, task.ID, blocked, items); err == nil {
		t.Fatal("hint accepted for deleted target")
	}
	if _, err = db.ExecContext(ctx, `UPDATE target SET deleted_at=NULL WHERE id=$1`, target); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_task_archive(task_id,state) VALUES($1,'archiving')`, task.ID); err != nil {
		t.Fatal(err)
	}
	blocked.ToolUseID = "archived"
	if _, err = facade.AddAutoTaskHints(ctx, owner, task.ID, blocked, items); err == nil {
		t.Fatal("hint accepted for archived task")
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM ai_task_archive WHERE task_id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	for _, attempt := range []struct {
		query string
		args  []any
	}{{`INSERT INTO tianqiong_ai.exploration_nodes(exploration_id,kind,payload,state,origin) VALUES($1,'goal','{"text":"forged"}','open','human')`, []any{task.ExplorationID}}, {`UPDATE tianqiong_ai.exploration_nodes SET payload='{"text":"forged"}' WHERE id=$1`, []any{result.IDs[0]}}, {`INSERT INTO tianqiong_ai.exploration_anchors(node_id,asset_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, []any{result.IDs[0], asset}}, {`INSERT INTO tianqiong_ai.auto_hint_staging(node_id,task_id,run_id,transaction_id) VALUES($1,$2,$3::uuid,txid_current())`, []any{result.IDs[0], task.ID, run}}, {`INSERT INTO ai_task_human_event(tenant_id,owner_user_id,task_id,kind,payload) VALUES('00000000-0000-0000-0000-000000000001',$1,$2,'hint','{"hints":["forged"]}'::jsonb)`, []any{owner, task.ID}}} {
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.ExecContext(ctx, `SELECT set_config('tianqiong.task_id',$1,TRUE),set_config('tianqiong.owner_user_id',$2,TRUE),set_config('tianqiong.execution_id',$3,TRUE),set_config('tianqiong.execution_mode','auto_hint',TRUE),set_config('tianqiong.hint_id',$4,TRUE)`, fmt.Sprint(task.ID), fmt.Sprint(owner), run, fmt.Sprint(result.IDs[0])); e != nil {
			t.Fatal(e)
		}
		if _, e = tx.ExecContext(ctx, attempt.query, attempt.args...); e == nil {
			t.Fatalf("forged hint write accepted: %s", attempt.query)
		}
		_ = tx.Rollback()
	}
	if _, err = conversations.StopRun(ctx, owner, conversation); err != nil {
		t.Fatal(err)
	}
	stopped := mutation
	stopped.ToolUseID = "stopped"
	if _, err = facade.AddAutoTaskHints(ctx, owner, task.ID, stopped, items); !errors.Is(err, aiapp.ErrRunLeaseLost) {
		t.Fatalf("stopped Auto wrote hint: %v", err)
	}
	if _, err = facade.SaveGoal(ctx, owner, task.ID, 0, "验证只读接口", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_task SET bootstrap_complete=TRUE WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimTask(ctx, uuid.NewString())
	if err != nil || claimed == nil || claimed.ID != task.ID {
		t.Fatalf("claim hint task: %+v %v", claimed, err)
	}
	claimed, err = store.StampTaskStart(ctx, claimed)
	if err != nil {
		t.Fatal(err)
	}
	connector, err := airuntime.NewTaskGraphConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	planner := &hintPlannerFixture{}
	runtime := airuntime.NewTaskRunner(connector, t.TempDir(), nil)
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { _, e := runtime.RunTask(runCtx, taskExecution(claimed), planner, "fixture"); done <- e }()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_task_human_event WHERE task_id=$1 AND kind='hint' AND processed_at IS NULL`, task.ID).Scan(&events); err != nil {
			t.Fatal(err)
		}
		if events == 0 && planner.observed.Load() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("planner did not stop")
	}
	if events != 0 || !planner.observed.Load() {
		t.Fatalf("planner did not consume Auto hint: pending=%d observed=%v", events, planner.observed.Load())
	}
}

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"iter"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	serverdatabase "github.com/yyhuni/lunafox/server/internal/database"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	settingsrepo "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	aiinfra "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/infrastructure"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	assetrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type companyPlannerFixture struct {
	calls      atomic.Int32
	observed   atomic.Bool
	mu         sync.Mutex
	lastResult string
}

func (p *companyPlannerFixture) Stream(_ context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	index := p.calls.Add(1) - 1
	return func(yield func(llm.StreamEvent, error) bool) {
		if index > 0 {
			for _, message := range request.Messages {
				for _, block := range message.Content {
					if block.Type == llm.BlockToolResult && block.ToolUseID == "company-search" {
						data, _ := json.Marshal(block.Content)
						p.mu.Lock()
						p.lastResult = string(data)
						p.mu.Unlock()
						if !block.IsError && strings.Contains(string(data), "Alpha Organization") && !strings.Contains(string(data), "Beta Organization") {
							p.observed.Store(true)
						}
					}
				}
			}
		}
		events := []llm.StreamEvent{{Type: llm.SEMessageStart}}
		if index == 0 {
			events = append(events, llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "company-search", ToolName: "list_companies"}, llm.StreamEvent{Type: llm.SEToolInputJSON, Text: `{}`}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"})
		} else {
			events = append(events, llm.StreamEvent{Type: llm.SETextDelta, Text: "组织已核对"}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"})
		}
		events = append(events, llm.StreamEvent{Type: llm.SEMessageStop})
		for _, event := range events {
			if !yield(event, nil) {
				return
			}
		}
	}
}
func (p *companyPlannerFixture) Complete(ctx context.Context, request llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, request) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func TestARTEXAgentCompaniesIsolatedPostgres(t *testing.T) {
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
	owner, target, asset := taskFixtureScope(t, ctx, db, "companies-alpha.example")
	_, otherTarget, _ := taskFixtureScope(t, ctx, db, "companies-beta.example")
	var other int
	if err = db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	var alpha, beta int
	if err = db.QueryRowContext(ctx, `INSERT INTO organization(name) VALUES('Alpha Organization') RETURNING id`).Scan(&alpha); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `INSERT INTO organization(name) VALUES('Beta Organization') RETURNING id`).Scan(&beta); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO organization_target(organization_id,target_id) VALUES($1,$2),($3,$4)`, alpha, target, beta, otherTarget); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	assetFacade := assets.NewFacade(assetrepo.NewAssetRegistryStore(orm))
	conversations := airepo.NewConversationStore(orm)
	conversation, run, instance := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = conversations.CreateAgentConversation(ctx, owner, conversation, "company view", "auto"); err != nil {
		t.Fatal(err)
	}
	if err = conversations.BeginRun(ctx, owner, conversation, run, instance, "list"); err != nil {
		t.Fatal(err)
	}
	global, err := assetFacade.AgentCompanies(ctx, owner, 0, run, "")
	if err != nil || global.Count != 2 || global.Truncated {
		t.Fatalf("global companies: %+v %v", global, err)
	}
	filtered, err := assetFacade.AgentCompanies(ctx, owner, 0, run, "ALPHA")
	if err != nil || filtered.Count != 1 || filtered.Companies[0].ID != alpha || filtered.Companies[0].Name != "Alpha Organization" {
		t.Fatalf("company search: %+v %v", filtered, err)
	}
	expected, err := assetFacade.List(ctx, assets.ListInput{UserID: owner, OrganizationID: alpha, Page: 1, PageSize: 1})
	if err != nil || filtered.Companies[0].AssetCount != expected.TotalSize {
		t.Fatalf("company inventory count: %+v %+v %v", filtered, expected, err)
	}
	if _, err = assetFacade.AgentCompanies(ctx, other, 0, run, ""); !errors.Is(err, assets.ErrForbidden) {
		t.Fatalf("foreign run read organizations: %v", err)
	}
	tasks := airepo.NewTaskStore(orm)
	facade := aiapp.NewTaskFacade(tasks, nil, nil)
	task, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "公司范围任务", Goal: "验证", TargetID: target, AssetIDs: []int64{asset}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = facade.SaveGoal(ctx, owner, task.ID, 0, "核对组织范围", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_task SET bootstrap_complete=TRUE WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := tasks.ClaimTask(ctx, uuid.NewString())
	if err != nil || claimed == nil || claimed.ID != task.ID {
		t.Fatalf("claim company task: %+v %v", claimed, err)
	}
	claimed, err = tasks.StampTaskStart(ctx, claimed)
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := assetFacade.AgentCompanies(ctx, owner, task.ID, claimed.ActiveRun, "")
	if err != nil || scoped.Count != 1 || scoped.Companies[0].ID != alpha || len(scoped.Companies[0].Scope) != 1 || scoped.Companies[0].Scope[0] != "companies-alpha.example" {
		t.Fatalf("target-scoped companies: %+v %v", scoped, err)
	}
	expected, err = assetFacade.List(ctx, assets.ListInput{UserID: owner, TargetID: target, OrganizationID: alpha, Page: 1, PageSize: 1})
	if err != nil || scoped.Companies[0].AssetCount != expected.TotalSize {
		t.Fatalf("target-scoped inventory count: %+v %+v %v", scoped, expected, err)
	}
	domain := func(user int) []tool.CoreTool { return aiinfra.CompanyTools(assetFacade, user) }
	catalog := settings.NewToolCatalogFacade(settingsrepo.NewToolCatalogStore(orm), nil, domain, nil)
	catalog.SetSeeds(settings.NativeToolSeeds(domain(owner)))
	if err = catalog.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	connector, err := airuntime.NewTaskGraphConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	runner := airuntime.NewTaskRunner(connector, t.TempDir(), nil)
	layerErrors := make(chan error, 4)
	runner.SetNativeTools(false, func(ctx context.Context, user int, key string, base []tool.CoreTool) ([]tool.CoreTool, error) {
		resolved, e := catalog.Resolve(ctx, user, key, base)
		if e != nil {
			select {
			case layerErrors <- e:
			default:
			}
		}
		return resolved, e
	})
	model := &companyPlannerFixture{}
	runCtx, stop := context.WithCancel(ctx)
	type runResult struct {
		outcome airuntime.TaskOutcome
		err     error
	}
	done := make(chan runResult, 1)
	go func() {
		outcome, e := runner.RunTask(runCtx, taskExecution(claimed), model, "fixture")
		done <- runResult{outcome, e}
	}()
	deadline := time.Now().Add(8 * time.Second)
	var completed *runResult
	for time.Now().Before(deadline) && !model.observed.Load() && completed == nil {
		select {
		case value := <-done:
			completed = &value
		case <-time.After(20 * time.Millisecond):
		}
	}
	stop()
	if completed == nil {
		select {
		case value := <-done:
			completed = &value
		case <-time.After(5 * time.Second):
			t.Fatal("company planner did not stop")
		}
	}
	if !model.observed.Load() {
		model.mu.Lock()
		detail := model.lastResult
		model.mu.Unlock()
		var layerErr error
		select {
		case layerErr = <-layerErrors:
		default:
		}
		t.Fatalf("Planner did not receive the scoped company tool result: calls=%d result=%s run=%+v layer=%v", model.calls.Load(), detail, completed, layerErr)
	}
	if _, err = db.ExecContext(ctx, `WITH fresh AS (INSERT INTO target(name,type) SELECT 'company-scope-'||n||'.example','domain' FROM generate_series(1,101) n RETURNING id) INSERT INTO organization_target(organization_id,target_id) SELECT $1,id FROM fresh`, alpha); err != nil {
		t.Fatal(err)
	}
	filtered, err = assetFacade.AgentCompanies(ctx, owner, 0, run, "Alpha Organization")
	if err != nil || filtered.Count != 1 || !filtered.Companies[0].ScopeTruncated || len(filtered.Companies[0].Scope) != 100 {
		t.Fatalf("company scope truncation: %+v %v", filtered, err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO organization(name) SELECT 'bulk-company-'||n FROM generate_series(1,201) n`); err != nil {
		t.Fatal(err)
	}
	global, err = assetFacade.AgentCompanies(ctx, owner, 0, run, "")
	if err != nil || global.Count != 200 || !global.Truncated {
		t.Fatalf("company pagination limit: count=%d truncated=%v error=%v", global.Count, global.Truncated, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE target SET deleted_at=clock_timestamp() WHERE id=$1`, target); err != nil {
		t.Fatal(err)
	}
	if _, err = assetFacade.AgentCompanies(ctx, owner, task.ID, claimed.ActiveRun, ""); !errors.Is(err, assets.ErrForbidden) {
		t.Fatalf("deleted target exposed companies: %v", err)
	}
	if _, err = conversations.StopRun(ctx, owner, conversation); err != nil {
		t.Fatal(err)
	}
	if _, err = assetFacade.AgentCompanies(ctx, owner, 0, run, ""); !errors.Is(err, assets.ErrForbidden) {
		t.Fatalf("stopped run read organizations: %v", err)
	}
}

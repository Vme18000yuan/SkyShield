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
	"os"
	"strings"
	"sync"
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

type autoPlatformProvider struct {
	mu                              sync.Mutex
	calls                           int
	db                              *sql.DB
	profileID                       string
	taskID, assetID, outsideAssetID int64
}

func (p *autoPlatformProvider) Stream(ctx context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	p.mu.Lock()
	index := p.calls
	p.calls++
	p.mu.Unlock()
	return func(yield func(llm.StreamEvent, error) bool) {
		for _, schema := range request.Tools {
			if schema.Name == "report_finding" || schema.Name == "list_findings" {
				yield(llm.StreamEvent{}, errors.New("independent chat exposed a task-only tool"))
				return
			}
		}
		if index > 0 && index <= 12 {
			found := false
			for _, message := range request.Messages {
				for _, block := range message.Content {
					if block.Type == llm.BlockToolResult && block.ToolUseID == fmt.Sprintf("platform-%d", index-1) && block.IsError == (index == 7) {
						found = true
					}
				}
			}
			if !found {
				yield(llm.StreamEvent{}, fmt.Errorf("platform tool %d did not succeed", index-1))
				return
			}
		}
		if index == 8 {
			var output string
			for _, message := range request.Messages {
				for _, block := range message.Content {
					if block.Type == llm.BlockToolResult && block.ToolUseID == "platform-7" {
						for _, part := range block.Content {
							output += part.Text
						}
					}
				}
			}
			if !strings.Contains(output, p.profileID) || !strings.Contains(output, "fixture-model") || strings.Contains(output, "profile-secret") || strings.Contains(output, "model.example") {
				yield(llm.StreamEvent{}, errors.New("model profile selection leaked or omitted fields"))
				return
			}
		}
		if index == 9 {
			var output string
			for _, message := range request.Messages {
				for _, block := range message.Content {
					if block.Type == llm.BlockToolResult && block.ToolUseID == "platform-8" {
						for _, part := range block.Content {
							output += part.Text
						}
					}
				}
			}
			var result aiapp.AutoTaskHintResult
			if err := json.Unmarshal([]byte(output), &result); err != nil || len(result.IDs) != 4 || result.IDs[0] <= 0 || result.IDs[1] != 0 || result.IDs[2] != 0 || result.IDs[3] != 0 || len(result.Errors) != 3 {
				yield(llm.StreamEvent{}, fmt.Errorf("unexpected batch hint result: %s", output))
				return
			}
		}
		if index == 10 {
			var output string
			for _, message := range request.Messages {
				for _, block := range message.Content {
					if block.Type == llm.BlockToolResult && block.ToolUseID == "platform-9" {
						for _, part := range block.Content {
							output += part.Text
						}
					}
				}
			}
			if !strings.Contains(output, "hint added:") {
				yield(llm.StreamEvent{}, fmt.Errorf("single hint result missing: %s", output))
				return
			}
		}
		if index == 11 {
			var output string
			for _, message := range request.Messages {
				for _, block := range message.Content {
					if block.Type == llm.BlockToolResult && block.ToolUseID == "platform-10" {
						for _, part := range block.Content {
							output += part.Text
						}
					}
				}
			}
			if !strings.Contains(output, "Alpha Auto Organization") || strings.Contains(output, "Beta Auto Organization") {
				yield(llm.StreamEvent{}, fmt.Errorf("Auto company search returned wrong scope: %s", output))
				return
			}
		}
		if index == 12 {
			var output string
			for _, message := range request.Messages {
				for _, block := range message.Content {
					if block.Type == llm.BlockToolResult && block.ToolUseID == fmt.Sprintf("platform-%d", index-1) {
						for _, part := range block.Content {
							output += part.Text
						}
					}
				}
			}
			if !strings.Contains(output, `"nodes"`) {
				yield(llm.StreamEvent{}, fmt.Errorf("explicit-task finding query did not succeed: %s", output))
				return
			}
		}
		calls := []struct{ name, input string }{
			{"create_skill", `{"name":"fixture-playbook","description":"fixture","instructions":"## Inspect"}`},
			{"update_skill_file", `{"name":"fixture-playbook","file":"scripts/inspect.py","content":"print('ok')"}`},
			{"create_custom_tool", `{"key":"fixture_helper","kind":"shell","description":"fixture helper","agents":["auto"]}`},
			{"update_custom_tool", `{"key":"fixture_helper","kind":"shell","description":"updated helper","agents":["auto"]}`},
			{"create_mcp", `{"name":"fixture_mcp","transport":"stdio","command":"python3","args":["fixture.py"],"env":{"TOKEN":"fixture-secret"}}`},
		}
		if index == 5 || index == 6 {
			var id string
			name := "fixture_mcp"
			if index == 6 {
				name = "fixture_mcp_updated"
			}
			if err := p.db.QueryRowContext(ctx, `SELECT id::text FROM ai_mcp_server WHERE name=$1`, name).Scan(&id); err != nil {
				yield(llm.StreamEvent{}, err)
				return
			}
			calls = append(calls, struct{ name, input string }{"update_mcp", fmt.Sprintf(`{"id":%q,"name":"fixture_mcp_updated","transport":"stdio","command":"python3","args":["fixture.py"]}`, id)})
			if index == 6 {
				calls = append(calls, struct{ name, input string }{"update_mcp", fmt.Sprintf(`{"id":%q,"name":"fixture_mcp_updated","transport":"stdio","command":"python3","args":["attacker.py"]}`, id)})
			}
		}
		if index == 7 {
			calls = append(calls, struct{ name, input string }{"unused", "{}"}, struct{ name, input string }{"unused", "{}"}, struct{ name, input string }{"list_llm_profiles", "{}"})
		}
		if index == 8 {
			calls = append(calls, struct{ name, input string }{"unused", "{}"}, struct{ name, input string }{"unused", "{}"}, struct{ name, input string }{"unused", "{}"}, struct{ name, input string }{"add_task_hint", fmt.Sprintf(`{"task_id":%q,"hints":[{"text":"优先检查接口","asset_ids":[%d]},{"text":"越界资产","asset_ids":[%d]},{"text":""},{"text":"关联流量","traffic_refs":[{"traffic_id":"fake","role":"proof"}]}]}`, fmt.Sprint(p.taskID), p.assetID, p.outsideAssetID)})
		}
		if index == 9 {
			calls = append(calls, struct{ name, input string }{"unused", "{}"}, struct{ name, input string }{"unused", "{}"}, struct{ name, input string }{"unused", "{}"}, struct{ name, input string }{"unused", "{}"}, struct{ name, input string }{"add_task_hint", fmt.Sprintf(`{"task_id":%q,"text":"单条战略提示"}`, fmt.Sprint(p.taskID))})
		}
		if index == 10 {
			for i := 0; i < 5; i++ {
				calls = append(calls, struct{ name, input string }{"unused", "{}"})
			}
			calls = append(calls, struct{ name, input string }{"list_companies", `{"search":"Alpha Auto"}`})
		}
		if index == 11 {
			for i := 0; i < 6; i++ {
				calls = append(calls, struct{ name, input string }{"unused", "{}"})
			}
			calls = append(calls, struct{ name, input string }{"list_task_findings", fmt.Sprintf(`{"task_id":%q}`, fmt.Sprint(p.taskID))})
		}
		events := []llm.StreamEvent{{Type: llm.SEMessageStart}}
		if index < len(calls) {
			events = append(events, llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: fmt.Sprintf("platform-%d", index), ToolName: calls[index].name}, llm.StreamEvent{Type: llm.SEToolInputJSON, Text: calls[index].input}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"})
		} else {
			events = append(events, llm.StreamEvent{Type: llm.SETextDelta, Text: "配置已完成"}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"})
		}
		events = append(events, llm.StreamEvent{Type: llm.SEMessageStop})
		for _, event := range events {
			if !yield(event, nil) {
				return
			}
		}
	}
}
func (p *autoPlatformProvider) Complete(ctx context.Context, request llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, request) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func TestARTEXAutoPlatformToolsIsolatedPostgres(t *testing.T) {
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
	var owner, other int
	if err = db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TIANQIONG_AI_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{37}, 32)))
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	skills := settings.NewSkillPackageFacade(settingsrepo.NewSkillStore(orm))
	mcp := settings.NewMCPFacade(settingsrepo.NewMCPStore(orm), airuntime.SecretBox{})
	profiles := settings.NewLLMProfileFacade(settingsrepo.NewLLMProfileStore(orm), airuntime.SecretBox{})
	profile, err := profiles.Save(ctx, owner, "", settings.ProfileInput{Name: "fixture model", Provider: "openai", Model: "fixture-model", BaseURL: "https://model.example", APIKey: "profile-secret", IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	gate := settingsrepo.NewPlatformMutationStore(orm)
	_, target, asset := taskFixtureScope(t, ctx, db, "auto-tool-hint.example")
	_, outsideTarget, outsideAsset := taskFixtureScope(t, ctx, db, "auto-tool-hint-outside.example")
	var alphaOrg, betaOrg int
	if err = db.QueryRowContext(ctx, `INSERT INTO organization(name) VALUES('Alpha Auto Organization') RETURNING id`).Scan(&alphaOrg); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `INSERT INTO organization(name) VALUES('Beta Auto Organization') RETURNING id`).Scan(&betaOrg); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO organization_target(organization_id,target_id) VALUES($1,$2),($3,$4)`, alphaOrg, target, betaOrg, outsideTarget); err != nil {
		t.Fatal(err)
	}
	assetFacade := assets.NewFacade(assetrepo.NewAssetRegistryStore(orm))
	taskStore := airepo.NewTaskStore(orm)
	taskFacade := aiapp.NewTaskFacade(taskStore, nil, nil)
	task, err := taskFacade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "Auto hint target", Goal: "test", TargetID: target, AssetIDs: []int64{asset}})
	if err != nil {
		t.Fatal(err)
	}
	agents := settings.NewAgentCatalogFacade(settingsrepo.NewAgentCatalogStore(orm))
	var catalog *settings.ToolCatalogFacade
	native := func(user int) []tool.CoreTool {
		items := aiinfra.AutoPlatformTools(gate, skills, mcp, func() aiinfra.PlatformToolCatalog { return catalog }, user)
		items = append(items, aiinfra.AutoLLMProfileTools(profiles, user)...)
		items = append(items, aiinfra.AutoTaskHintTools(taskFacade, user, func(context.Context, int) bool { return false })...)
		items = append(items, aiinfra.CompanyTools(assetFacade, user)...)
		items = append(items, aiinfra.AutoTaskReadTools(taskFacade, user)...)
		return append(items, aiinfra.TaskContextFallbackTools()...)
	}
	catalog = settings.NewToolCatalogFacade(settingsrepo.NewToolCatalogStore(orm), agents, native, nil)
	catalog.SetSeeds(settings.NativeToolSeeds(native(owner)))
	if err = catalog.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	// Even an explicit binding must not expose graph-only tools in a taskless chat.
	if err = orm.Exec(`UPDATE ai_tool_catalog SET agents=agents||'["auto"]'::jsonb WHERE key='list_findings' AND NOT agents @> '["auto"]'::jsonb`).Error; err != nil {
		t.Fatal(err)
	}
	standalone, err := catalog.Resolve(ctx, owner, "auto", native(owner))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range standalone {
		if airuntime.RequiresTaskContext(item.Name()) {
			t.Fatal("catalog binding exposed a graph-only tool without a task")
		}
	}
	graphHandler := tool.Build(tool.Spec{Name: "list_findings", Schema: map[string]any{"type": "object"}, Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
		return tool.Text("live graph handler"), nil
	}})
	taskContext := airuntime.WithTaskExecution(ctx, airuntime.TaskExecution{ID: task.ID, ExplorationID: task.ExplorationID, UserID: owner, TenantID: assets.DefaultTenantID})
	bound, err := catalog.Resolve(taskContext, owner, "auto", []tool.CoreTool{graphHandler})
	if err != nil {
		t.Fatal(err)
	}
	foundGraph := false
	for _, item := range bound {
		if item.Name() == "report_finding" {
			t.Fatal("catalog inserted a task-context fallback without a graph handler")
		}
		if item.Name() == "list_findings" {
			result, callErr := item.Call(ctx, json.RawMessage(`{}`), nil)
			if callErr != nil || result.Flatten() != "live graph handler" {
				t.Fatal("global fallback replaced the graph handler")
			}
			foundGraph = true
		}
	}
	if !foundGraph {
		t.Fatal("real task graph handler was hidden")
	}
	conversations := airepo.NewConversationStore(orm)
	conversationID, runID, instanceID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = conversations.CreateAgentConversation(ctx, owner, conversationID, "platform fence", "auto"); err != nil {
		t.Fatal(err)
	}
	if err = conversations.BeginRun(ctx, owner, conversationID, runID, instanceID, "fixture"); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"name":"replay-fixture","description":"test"}`)
	action := func(inner context.Context) (string, error) {
		err := skills.Create(inner, owner, settings.SkillPackage{Name: "replay-fixture", Description: "test"})
		return "created", err
	}
	results := make(chan string, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			result, e := gate.Apply(ctx, owner, runID, "same-call", "create_skill", raw, action)
			results <- result
			errs <- e
		}()
	}
	for i := 0; i < 2; i++ {
		if result, e := <-results, <-errs; e != nil || result != "created" {
			t.Fatalf("concurrent platform replay: %s %v", result, e)
		}
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_skill WHERE name='replay-fixture'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate skill create: %d %v", count, err)
	}
	if _, err = gate.Apply(ctx, owner, runID, "same-call", "create_skill", json.RawMessage(`{"name":"different"}`), action); !errors.Is(err, settings.ErrInvalidProfile) {
		t.Fatalf("conflicting replay accepted: %v", err)
	}
	if _, err = gate.Apply(ctx, other, runID, "foreign", "create_skill", raw, action); err == nil {
		t.Fatal("foreign user used run")
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_tool_catalog SET enabled=FALSE WHERE key='create_skill'`); err != nil {
		t.Fatal(err)
	}
	if _, err = gate.Apply(ctx, owner, runID, "disabled", "create_skill", raw, action); !errors.Is(err, settingsrepo.ErrPlatformRunInactive) {
		t.Fatalf("disabled platform tool ran: %v", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_tool_catalog SET enabled=TRUE WHERE key='create_skill'`); err != nil {
		t.Fatal(err)
	}
	rollback := func(inner context.Context) (string, error) {
		if e := skills.Create(inner, owner, settings.SkillPackage{Name: "rollback-fixture", Description: "test"}); e != nil {
			return "", e
		}
		return "", errors.New("fixture rollback")
	}
	if _, err = gate.Apply(ctx, owner, runID, "rollback", "create_skill", json.RawMessage(`{"name":"rollback-fixture"}`), rollback); err == nil {
		t.Fatal("failed action committed")
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_skill WHERE name='rollback-fixture'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed action left skill: %d %v", count, err)
	}
	rows, err := catalog.List(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	var builtin settings.ToolRecord
	for _, row := range rows {
		if row.Key == "create_skill" {
			builtin = row
			break
		}
	}
	if builtin.Key == "" {
		t.Fatal("create_skill seed missing")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	finished := make(chan error, 2)
	go func() {
		_, e := gate.Apply(ctx, owner, runID, "lock-order", "create_skill", json.RawMessage(`{"name":"lock-fixture"}`), func(inner context.Context) (string, error) {
			close(entered)
			<-release
			e := skills.Create(inner, owner, settings.SkillPackage{Name: "lock-fixture", Description: "test"})
			return "created", e
		})
		finished <- e
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("platform action did not start")
	}
	go func() {
		builtin.Description = "updated by administrator"
		_, e := catalog.Save(ctx, owner, builtin.Key, builtin, false)
		finished <- e
	}()
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case e := <-finished:
			if e != nil {
				t.Fatalf("concurrent catalog edit: %v", e)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("catalog edit and platform action deadlocked")
		}
	}
	if _, err = conversations.StopRun(ctx, owner, conversationID); err != nil {
		t.Fatal(err)
	}
	if _, err = gate.Apply(ctx, owner, runID, "stopped", "create_skill", raw, action); !errors.Is(err, settingsrepo.ErrPlatformRunInactive) {
		t.Fatalf("stopped run wrote settings: %v", err)
	}

	provider := &autoPlatformProvider{db: db, profileID: profile.ID, taskID: task.ID, assetID: asset, outsideAssetID: outsideAsset}
	resolve := func(context.Context, int) (llm.Provider, string, bool, error) { return provider, "fixture", true, nil }
	runner, err := airuntime.NewChatRunner(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	chat := aiapp.NewConversationFacade(conversations, runner, resolve, nil)
	chat.SetNativeTools(false, catalog.Resolve)
	jobCtx, stop := context.WithCancel(ctx)
	chat.Start(jobCtx)
	defer func() { stop(); <-chat.Done() }()
	auto, err := chat.CreateWithAgent(ctx, owner, "platform tools", "auto")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = chat.Send(ctx, owner, auto.ID, "创建测试配置"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(12 * time.Second)
	completed := false
	for time.Now().Before(deadline) {
		page, e := chat.Activities(ctx, owner, auto.ID, 0)
		if e != nil {
			t.Fatal(e)
		}
		if !page.Running {
			if page.RunStatus != "completed" {
				raw, _ := json.Marshal(page)
				t.Fatalf("Auto platform run failed: %s", raw)
			}
			completed = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !completed {
		t.Fatal("Auto platform run did not complete")
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_skill_file f JOIN ai_skill s ON s.id=f.skill_id WHERE s.name='fixture-playbook' AND f.path='scripts/inspect.py' AND convert_from(f.content,'UTF8')='print(''ok'')'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("Auto skill file missing: %d %v", count, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_tool_catalog WHERE key='fixture_helper' AND system=FALSE AND description='updated helper'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("Auto custom tool missing: %d %v", count, err)
	}
	var cipher []byte
	var args string
	if err = db.QueryRowContext(ctx, `SELECT encrypted_env,args::text FROM ai_mcp_server WHERE name='fixture_mcp_updated'`).Scan(&cipher, &args); err != nil || len(cipher) == 0 || bytes.Contains(cipher, []byte("fixture-secret")) || !strings.Contains(args, "fixture.py") || strings.Contains(args, "attacker.py") {
		t.Fatalf("Auto MCP secret lost or plaintext: %v", err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.exploration_nodes n JOIN tianqiong_ai.exploration_anchors a ON a.node_id=n.id WHERE n.exploration_id=$1 AND n.kind='hint' AND n.payload->>'text'='优先检查接口' AND a.asset_id=$2`, task.ExplorationID, asset).Scan(&count); err != nil || count != 1 {
		t.Fatalf("Auto hint tool did not save task hint: %d %v", count, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.exploration_nodes WHERE exploration_id=$1 AND kind='hint' AND payload->>'text'='单条战略提示'`, task.ExplorationID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("single Auto hint missing: %d %v", count, err)
	}
	toolStore := settingsrepo.NewToolCatalogStore(orm)
	legacy := settings.ToolRecord{Key: "legacy_collision", Description: "user handler", Schema: json.RawMessage(`{"type":"object"}`), Agents: []string{"auto"}, Enabled: true, Kind: "command", Exec: json.RawMessage(`{"command":"echo custom"}`)}
	if err = toolStore.SaveToolCatalog(ctx, owner, legacy, true); err != nil {
		t.Fatal(err)
	}
	marker := func(text string) tool.CoreTool {
		return tool.Build(tool.Spec{Name: "legacy_collision", Description: text, Schema: map[string]any{"type": "object"}, Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
			return tool.Text(text), nil
		}})
	}
	shadow := settings.NewToolCatalogFacade(toolStore, agents, func(int) []tool.CoreTool { return []tool.CoreTool{marker("host")} }, func(context.Context, int, settings.ToolRecord, []tool.CoreTool) tool.CoreTool {
		return marker("custom")
	})
	shadow.SetSeeds(settings.NativeToolSeeds([]tool.CoreTool{marker("host")}))
	if err = shadow.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	resolved, err := shadow.Resolve(ctx, owner, "auto", nil)
	if err != nil {
		t.Fatal(err)
	}
	var selected tool.CoreTool
	for _, entry := range resolved {
		if entry.Name() == "legacy_collision" {
			selected = entry
			break
		}
	}
	if selected == nil {
		t.Fatal("existing custom tool was hidden")
	}
	result, err := selected.Call(ctx, json.RawMessage(`{}`), nil)
	if err != nil || result.Flatten() != "custom" {
		t.Fatalf("existing custom tool replaced by host: %+v %v", result, err)
	}
}

package main

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Autumn-27/artex/agent"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	repository "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func verifyARTEXAgentControlsPostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var admin, member int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&admin); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&member); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := repository.NewAgentCatalogStore(orm)
	catalog := settings.NewAgentCatalogFacade(store)
	control := settings.NewAgentControlFacade(catalog, store, repository.NewLLMProfileStore(orm))
	if err := store.SeedDefaultAgents(ctx); err != nil {
		t.Fatal(err)
	}
	for _, preset := range []struct{ key, prompt string }{{"reporter", agent.ReporterDefaultPrompt}, {"retester", agent.RetesterDefaultPrompt}, {"credential_processor", agent.CredentialProcessorPrompt}} {
		item, err := catalog.Get(ctx, admin, preset.key)
		if err != nil || item.Builtin || item.SystemPrompt != preset.prompt {
			t.Fatalf("preset %s: %+v %v", preset.key, item, err)
		}
	}
	triggers, err := control.Triggers(ctx, admin, "reporter")
	if err != nil || len(triggers) != 1 || !triggers[0].OnToolCall || len(triggers[0].ToolNames) != 1 || triggers[0].ToolNames[0] != "report_finding" {
		t.Fatalf("reporter trigger: %+v %v", triggers, err)
	}
	reporter, err := catalog.Get(ctx, admin, "reporter")
	if err != nil || reporter.Options.TriggerRunMode != "parallel" || reporter.Options.TriggerMergeMode != "none" {
		t.Fatalf("reporter behavior: %+v %v", reporter, err)
	}
	if _, err := control.Details(ctx, member, "planner"); !errors.Is(err, settings.ErrForbidden) {
		t.Fatalf("member edited agent controls: %v", err)
	}
	custom, err := catalog.Create(ctx, admin, airuntime.ChatAgentDefinition{Key: "native_control_fixture", Name: "原版编辑器测试"})
	if err != nil {
		t.Fatal(err)
	}
	turns, seconds := 7, 120
	interactive := true
	mode, merge, maxParallel := "parallel", "none", 2
	if err := control.PatchConfig(ctx, admin, custom.Key, settings.AgentConfigPatch{MaxTurns: &turns, RunSeconds: &seconds, InteractiveShell: &interactive, TriggerRunMode: &mode, TriggerMergeMode: &merge, TriggerMaxParallel: &maxParallel}); err != nil {
		t.Fatal(err)
	}
	detail, err := control.Details(ctx, admin, custom.Key)
	if err != nil || detail.Agent.MaxTurns != 7 || detail.Agent.RunSeconds != 120 || !detail.Agent.InteractiveShell || detail.Agent.TriggerMaxParallel != 2 {
		t.Fatalf("agent options: %+v %v", detail, err)
	}
	if _, err := control.SavePrompt(ctx, admin, custom.Key, "你好 {{.Now}} / {{.DataDir}}", false); err != nil {
		t.Fatal(err)
	}
	if _, err := control.SavePrompt(ctx, admin, custom.Key, "{{.Unsupported}}", false); !errors.Is(err, settings.ErrAgentCatalogInvalid) {
		t.Fatalf("unknown prompt variable accepted: %v", err)
	}
	preview, err := control.Preview(ctx, admin, custom.Key, "工作目录 {{.DataDir}}", map[string]string{"DataDir": "/sample/run"})
	if err != nil || !strings.Contains(preview, "/sample/run") {
		t.Fatalf("preview: %q %v", preview, err)
	}
	if err := control.SaveWrapup(ctx, admin, custom.Key, false, "总结当前证据", 3); err != nil {
		t.Fatal(err)
	}
	detail, err = control.Details(ctx, admin, custom.Key)
	if err != nil || detail.WrapupPrompt != "总结当前证据" || detail.WrapupMaxTurns != 3 || len(detail.Versions) < 2 {
		t.Fatalf("prompt or wrapup: %+v %v", detail, err)
	}
	skills := settings.NewSkillFacade(repository.NewSkillStore(orm))
	skill, err := skills.Save(ctx, admin, "", settings.SkillRecord{Name: "native-control-skill", Description: "fixture", Instructions: "fixture instructions", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	mcps := settings.NewMCPFacade(repository.NewMCPStore(orm), airuntime.SecretBox{})
	mcp, err := mcps.Save(ctx, admin, "", settings.MCPInput{Name: "native-control-mcp", Endpoint: "https://fixture.invalid/mcp", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if err := control.SetVisibility(ctx, admin, custom.Key, settings.AgentVisibility{MCP: []string{mcp.ID}, Skill: []string{skill.Name}}); err != nil {
		t.Fatal(err)
	}
	detail, err = control.Details(ctx, admin, custom.Key)
	if err != nil || len(detail.Visibility.MCP) != 1 || detail.Visibility.MCP[0] != mcp.ID || len(detail.Visibility.Skill) != 1 || detail.Agent.MCPCount != 1 || detail.Agent.SkillCount != 1 {
		t.Fatalf("visibility: %+v %v", detail, err)
	}
	trigger, err := control.SaveTrigger(ctx, admin, custom.Key, artexdb.AgentTrigger{Enabled: true, IntervalSec: 60, IntervalMessage: "按计划检查任务", OnTaskCreate: true, TaskCreateMessage: "检查新任务"}, true)
	if err != nil {
		t.Fatal(err)
	}
	trigger.IntervalSec = 90
	if _, err := control.SaveTrigger(ctx, admin, "", *trigger, false); err != nil {
		t.Fatal(err)
	}
	triggerStore := airepo.NewAgentTriggerStore(orm)
	if err := triggerStore.InitializeAgentEvents(ctx); err != nil {
		t.Fatal(err)
	}
	capture := &captureModelChatRunner{runs: make(chan airuntime.ChatRun, 4)}
	chat := aiapp.NewConversationFacade(airepo.NewConversationStore(orm), capture, func(context.Context, int) (llm.Provider, string, bool, error) {
		return &assetFixtureProvider{}, "trigger-fixture", true, nil
	}, nil)
	chat.SetAgentCatalogResolver(catalog.ResolveForChat)
	chatCtx, cancelChat := context.WithCancel(ctx)
	chat.Start(chatCtx)
	defer func() { cancelChat(); <-chat.Done() }()
	dispatch := aiapp.NewAgentTriggerController(triggerStore, chat, nil)
	now := time.Now()
	if err := dispatch.Tick(ctx, now); err != nil {
		t.Fatal(err)
	}
	select {
	case run := <-capture.runs:
		if run.AgentKey != custom.Key {
			t.Fatal("wrong trigger Agent")
		}
	case <-time.After(time.Second):
		t.Fatal("interval trigger did not execute")
	}
	task, err := aiapp.NewTaskFacade(airepo.NewTaskStore(orm), nil, nil).Create(ctx, aiapp.CreateTaskInput{UserID: admin, Goal: "验证任务创建触发"})
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatch.Tick(ctx, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	select {
	case run := <-capture.runs:
		if !strings.Contains(run.Message, "验证任务创建触发") {
			t.Fatal("task context missing from trigger")
		}
	case <-time.After(time.Second):
		t.Fatal("task creation trigger did not execute")
	}
	if err := dispatch.Tick(ctx, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM ai_agent_trigger_job WHERE agent_key=$1 AND run_id IS NOT NULL`, custom.Key).Scan(&count); err != nil || count != 2 {
		t.Fatalf("trigger delivery/replay: %d %v", count, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM ai_task WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := control.DeleteTrigger(ctx, admin, trigger.ID); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Delete(ctx, admin, custom.Key); err != nil {
		t.Fatal(err)
	}
	if err := mcps.Delete(ctx, admin, mcp.ID); err != nil {
		t.Fatal(err)
	}
	if err := skills.Delete(ctx, admin, skill.ID); err != nil {
		t.Fatal(err)
	}
	// Presets are editable custom Agents. A user's deletion must survive restart.
	if err := catalog.Delete(ctx, admin, "reporter"); err != nil {
		t.Fatal(err)
	}
	if err := store.SeedDefaultAgents(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Get(ctx, admin, "reporter"); !errors.Is(err, settings.ErrAgentCatalogNotFound) {
		t.Fatalf("deleted preset was recreated: %v", err)
	}
	if err := catalog.Delete(ctx, admin, "retester"); err != nil {
		t.Fatal(err)
	}
}

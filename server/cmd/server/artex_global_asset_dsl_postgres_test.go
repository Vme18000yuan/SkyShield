package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"iter"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	settingsrepo "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type globalAssetModel struct {
	mentionModelFixture
	calls     atomic.Int64
	deletedID int64
	active    chan string
	deleted   chan string
	page      chan string
}

func (p *globalAssetModel) Stream(ctx context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	call := p.calls.Add(1)
	for _, message := range request.Messages {
		for _, block := range message.Content {
			if block.Type == llm.BlockToolResult && block.ToolUseID == "global-active" {
				raw, _ := json.Marshal(block.Content)
				select {
				case p.active <- string(raw):
				default:
				}
			}
			if block.Type == llm.BlockToolResult && block.ToolUseID == "global-deleted" {
				raw, _ := json.Marshal(block.Content)
				select {
				case p.deleted <- string(raw):
				default:
				}
			}
			if block.Type == llm.BlockToolResult && block.ToolUseID == "global-page" {
				raw, _ := json.Marshal(block.Content)
				select {
				case p.page <- string(raw):
				default:
				}
			}
		}
	}
	if call <= 3 {
		id, input := "global-active", `{"dsl":"domain==api.global-dsl.example","limit":20}`
		if call == 2 {
			id, input = "global-deleted", fmt.Sprintf(`{"id":%d}`, p.deletedID)
		}
		if call == 3 {
			id, input = "global-page", `{"dsl":"domain=global-dsl.example","limit":2,"offset":260}`
		}
		return func(yield func(llm.StreamEvent, error) bool) {
			for _, event := range []llm.StreamEvent{{Type: llm.SEMessageStart}, {Type: llm.SEToolUseStart, ToolID: id, ToolName: "list_assets"}, {Type: llm.SEToolInputJSON, Text: input}, {Type: llm.SEMessageDelta, StopReason: "tool_use"}, {Type: llm.SEMessageStop}} {
				if !yield(event, nil) {
					return
				}
			}
		}
	}
	return p.mentionModelFixture.Stream(ctx, request)
}
func (p *globalAssetModel) Complete(ctx context.Context, request llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, request) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func verifyARTEXGlobalAssetDSLPostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	owner, target, _ := taskFixtureScope(t, ctx, db, "global-dsl.example")
	if _, err := db.ExecContext(ctx, `INSERT INTO subdomain(target_id,dns_name) SELECT $1,'batch-'||n||'.global-dsl.example' FROM generate_series(1,330) n`, target); err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(ctx, `SELECT s.dns_name FROM subdomain s JOIN asset_subdomain_binding b ON b.subdomain_id=s.id WHERE s.target_id=$1 ORDER BY b.asset_id DESC OFFSET 260 LIMIT 2`, target)
	if err != nil {
		t.Fatal(err)
	}
	var expected []string
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		expected = append(expected, value)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(expected) != 2 {
		t.Fatal("pagination fixture missing")
	}
	_, deletedTarget, deletedAsset := taskFixtureScope(t, ctx, db, "deleted-global-dsl.example")
	if _, err := db.ExecContext(ctx, `UPDATE target SET deleted_at=clock_timestamp() WHERE id=$1`, deletedTarget); err != nil {
		t.Fatal(err)
	}
	var other int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := airepo.NewConversationStore(orm)
	conversation, run, instance := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = store.CreateAgentConversation(ctx, owner, conversation, "global DSL", "auto"); err != nil {
		t.Fatal(err)
	}
	if err = store.BeginRun(ctx, owner, conversation, run, instance, "查询资产"); err != nil {
		t.Fatal(err)
	}
	assets := func(user int) []tool.CoreTool { return airuntime.GlobalAssetDSLTools(db, user) }
	catalog := settingsapp.NewToolCatalogFacade(settingsrepo.NewToolCatalogStore(orm), nil, assets, nil)
	catalog.SetSeeds(settingsapp.NativeToolSeeds(assets(owner)))
	if err = catalog.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	resolved, err := catalog.Resolve(ctx, owner, "auto", nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range resolved {
		if item.Name() == "list_assets" {
			found = true
		}
	}
	if !found {
		t.Fatal("Auto tool catalog did not expose list_assets")
	}
	if tools, err := catalog.Resolve(airuntime.WithTaskExecution(ctx, airuntime.TaskExecution{ID: 123}), owner, "planner", nil); err != nil {
		t.Fatal(err)
	} else {
		for _, item := range tools {
			if item.Name() == "list_assets" {
				t.Fatal("global search bypassed task tool grant")
			}
		}
	}
	model := &globalAssetModel{deletedID: deletedAsset, active: make(chan string, 1), deleted: make(chan string, 1), page: make(chan string, 1)}
	runner, err := airuntime.NewChatRunner(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	request := airuntime.ChatRun{TenantID: "00000000-0000-0000-0000-000000000001", UserID: owner, SessionID: conversation, ExecutionID: run, AgentKey: "auto", Message: "查询资产", Model: "fixture", Provider: model, Tools: resolved}
	if _, err = runner.Run(ctx, request, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-model.active:
		if !strings.Contains(got, "api.global-dsl.example") {
			t.Fatalf("global DSL missing asset: %s", got)
		}
	default:
		t.Fatal("global DSL tool not invoked")
	}
	select {
	case got := <-model.deleted:
		if strings.Contains(got, "deleted-global-dsl.example") || !strings.Contains(got, "count") {
			t.Fatalf("deleted target asset exposed: %s", got)
		}
	default:
		t.Fatal("global direct ID lookup not invoked")
	}
	select {
	case got := <-model.page:
		if !strings.Contains(got, expected[0]) || !strings.Contains(got, expected[1]) {
			t.Fatalf("global DSL pagination across batches: %s", got)
		}
	default:
		t.Fatal("global DSL pagination not invoked")
	}
	disabled := false
	disabledModel := &mentionModelFixture{}
	disabledRun := request
	disabledRun.Provider, disabledRun.Tools, disabledRun.ToolLayer, disabledRun.AssetSearchEnabled = disabledModel, nil, catalog.Resolve, &disabled
	if _, err = runner.Run(ctx, disabledRun, nil); err != nil {
		t.Fatal(err)
	}
	disabledModel.mu.Lock()
	for _, item := range disabledModel.request.Tools {
		if item.Name == "list_assets" {
			disabledModel.mu.Unlock()
			t.Fatal("disabled conversation asset grant was bypassed")
		}
	}
	disabledModel.mu.Unlock()
	foreign := &globalAssetModel{deletedID: deletedAsset, active: make(chan string, 1), deleted: make(chan string, 1), page: make(chan string, 1)}
	request.UserID = other
	request.Provider = foreign
	request.Tools = assets(other)
	if _, err = runner.Run(ctx, request, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-foreign.active:
		if strings.Contains(got, "api.global-dsl.example") || !strings.Contains(got, "查询不可用") {
			t.Fatalf("foreign conversation accessed assets: %s", got)
		}
	default:
		t.Fatal("foreign run did not invoke tool")
	}
	if _, err = store.StopRun(ctx, owner, conversation); err != nil {
		t.Fatal(err)
	}
	stale := &globalAssetModel{deletedID: deletedAsset, active: make(chan string, 1), deleted: make(chan string, 1), page: make(chan string, 1)}
	request.UserID = owner
	request.Provider = stale
	request.Tools = resolved
	staleRunner, err := airuntime.NewChatRunner(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = staleRunner.Run(ctx, request, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-stale.active:
		if strings.Contains(got, "api.global-dsl.example") || !strings.Contains(got, "查询不可用") {
			t.Fatalf("stale conversation lease read assets: %s", got)
		}
	default:
		t.Fatal("stale run did not invoke tool")
	}
}

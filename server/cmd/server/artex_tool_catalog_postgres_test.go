package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/Autumn-27/norma/tool"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	repository "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func verifyARTEXToolCatalogPostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	t.Setenv("TIANQIONG_AI_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("t", 32))))
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
	catalog := settings.NewAgentCatalogFacade(repository.NewAgentCatalogStore(orm))
	store := repository.NewToolCatalogStore(orm)
	facade := settings.NewToolCatalogFacade(store, catalog, nil, func(_ context.Context, _ int, row settings.ToolRecord, base []tool.CoreTool) tool.CoreTool {
		return airuntime.BuildCustomTool(row, base)
	})
	seeds := settings.NativeToolSeeds(nil)
	facade.SetSeeds(seeds)
	if err = facade.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := facade.List(ctx, admin)
	if err != nil || len(rows) != len(seeds) || len(rows) < 20 {
		t.Fatalf("native seeds: %d %v", len(rows), err)
	}
	if _, err = facade.List(ctx, member); !errors.Is(err, settings.ErrForbidden) {
		t.Fatalf("member modified catalog: %v", err)
	}
	var original settings.ToolRecord
	for _, row := range rows {
		if row.Key == "graph_overview" {
			original = row
		}
	}
	changed := original
	changed.Description = "edited native description"
	changed.Enabled = false
	if _, err = facade.Save(ctx, admin, changed.Key, changed, false); err != nil {
		t.Fatal(err)
	}
	if err = facade.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err = facade.List(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Key == changed.Key && (row.Description != changed.Description || row.Enabled) {
			t.Fatal("startup replaced tool edit")
		}
	}
	invalid := original
	invalid.Schema = json.RawMessage(`{"type":"object","properties":{"malicious":{"type":"string"}}}`)
	if _, err = facade.Save(ctx, admin, invalid.Key, invalid, false); !errors.Is(err, settings.ErrToolCatalogInvalid) {
		t.Fatalf("builtin structure changed: %v", err)
	}
	if err = facade.Reset(ctx, admin, original.Key); err != nil {
		t.Fatal(err)
	}
	input := settings.ToolRecord{Key: "native_custom_fixture", Description: "fixture", Schema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string","default":"original-default"}}}`), Agents: []string{"auto"}, Enabled: true, Kind: "command", Exec: json.RawMessage(`{"command":"printf %s {value}"}`), Deferred: true}
	if _, err = facade.Save(ctx, admin, input.Key, input, true); err != nil {
		t.Fatal(err)
	}
	base := []tool.CoreTool{tool.NewBash()}
	resolved, err := facade.Resolve(ctx, admin, "auto", base)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range resolved {
		if item.Name() == input.Key {
			found = true
			if mark, ok := item.(interface{ IsDeferredTool() bool }); !ok || !mark.IsDeferredTool() {
				t.Fatal("deferred flag lost")
			}
			result, err := item.Call(ctx, json.RawMessage(`{}`), &tool.ToolContext{WorkingDir: t.TempDir()})
			if err != nil || result.IsError || !strings.Contains(result.Flatten(), "original-default") {
				t.Fatalf("parameter default: %s %v", result.Flatten(), err)
			}
		}
	}
	if !found {
		t.Fatal("custom tool not injected")
	}
	other, err := facade.Resolve(ctx, admin, "worker", base)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range other {
		if item.Name() == input.Key {
			t.Fatal("agent binding ignored")
		}
	}
	if err = facade.Delete(ctx, admin, input.Key); err != nil {
		t.Fatal(err)
	}
	if err = facade.Delete(ctx, admin, original.Key); !errors.Is(err, settings.ErrToolCatalogNotFound) {
		t.Fatalf("builtin deleted: %v", err)
	}
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Fixture") != "value" {
			t.Error("HTTP header template missing")
		}
		_, _ = w.Write([]byte(r.URL.Query().Get("q")))
	}))
	defer fixture.Close()
	row := settings.ToolRecord{Key: "http_fixture", Kind: "http", Exec: json.RawMessage(`{"url":"` + fixture.URL + `?q={query}","headers":{"X-Fixture":"{header}"}}`)}
	raw := json.RawMessage(`{"query":"hello","header":"value"}`)
	result, err := airuntime.BuildCustomTool(row, nil).Call(ctx, raw, nil)
	if err != nil || result.IsError || !strings.Contains(result.Flatten(), "hello") {
		t.Fatalf("HTTP tool: %s %v", result.Flatten(), err)
	}
	profileStore := repository.NewLLMProfileStore(orm)
	runtime := settings.NewRuntimeSettingFacade(store, airuntime.SecretBox{}, profileStore)
	saved, err := runtime.Patch(ctx, admin, map[string]any{"workers": float64(5), "web_search_enabled": true, "web_search_backend": "brave-free", "brave_search_api_key": "fixture-secret-key"})
	if err != nil || saved["brave_key_set"] != true || saved["brave_search_api_key"] != nil {
		t.Fatalf("runtime settings: %+v %v", saved, err)
	}
	var cipher []byte
	if err = db.QueryRowContext(ctx, `SELECT encrypted_value FROM ai_runtime_secret WHERE key='brave_search_api_key'`).Scan(&cipher); err != nil || strings.Contains(string(cipher), "fixture-secret-key") {
		t.Fatal("search credential not encrypted")
	}
	config, enabled, err := runtime.SearchConfig(ctx, member)
	if err != nil || !enabled || config.BraveAPIKey != "fixture-secret-key" {
		t.Fatalf("runtime search config: %+v %v", config, err)
	}
	var searchCalls int
	searchFixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		searchCalls++
		if r.Header.Get("x-api-key") != "fixture-search-key" {
			t.Error("active model credential was not used by DeepSeek search")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"web_search_tool_result\",\"content\":[{\"type\":\"web_search_result\",\"title\":\"fixture\",\"url\":\"https://fixture.invalid\"}]}}\n\n"))
	}))
	defer searchFixture.Close()
	searchProfile, err := settings.NewLLMProfileFacade(profileStore, airuntime.SecretBox{}).Save(ctx, admin, "", settings.ProfileInput{Name: "search fixture", Provider: "anthropic", Model: "search-model", BaseURL: searchFixture.URL, APIKey: "fixture-search-key", IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.ExecContext(context.Background(), `UPDATE ai_llm_profile SET is_default=FALSE WHERE id=$1`, searchProfile.ID)
		_ = settings.NewLLMProfileFacade(profileStore, airuntime.SecretBox{}).Delete(context.Background(), admin, searchProfile.ID)
	}()
	probe, err := runtime.ProbeWebSearch(ctx, admin, settings.WebSearchProbeInput{Backend: "deepseek"})
	if err != nil || !probe.OK || probe.Count != 1 || searchCalls != 1 {
		t.Fatalf("draft DeepSeek search probe: %+v %v calls=%d", probe, err, searchCalls)
	}
	if _, err = runtime.Patch(ctx, admin, map[string]any{"web_search_backend": "deepseek"}); err != nil {
		t.Fatal(err)
	}
	search, err := airuntime.NewDynamicWebSearch(func(ctx context.Context) (tool.WebSearchConfig, bool, error) {
		return runtime.SearchConfig(ctx, member)
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err = search.Call(ctx, json.RawMessage(`{"query":"fixture"}`), nil)
	if err != nil || result.IsError || !strings.Contains(result.Flatten(), "fixture.invalid") || searchCalls != 2 {
		t.Fatalf("Agent web search: %+v %v calls=%d", result, err, searchCalls)
	}
	on := true
	llmStore := repository.NewLLMProfileStore(orm)
	if _, err = llmStore.PatchRuntimeSettings(ctx, admin, settings.LLMRuntimePatch{PoolEnabled: &on}); err != nil {
		t.Fatal(err)
	}
	saved, err = runtime.Get(ctx, admin)
	if err != nil || saved["workers"] != float64(5) || saved["llm_pool_enabled"] != true {
		t.Fatalf("LLM patch erased runtime: %+v %v", saved, err)
	}
	if _, err = runtime.Patch(ctx, member, map[string]any{"workers": float64(1)}); !errors.Is(err, settings.ErrForbidden) {
		t.Fatalf("member changed global runtime: %v", err)
	}
	if _, err = runtime.Patch(ctx, admin, map[string]any{"web_search_enabled": false, "web_search_backend": "ddgs", "brave_search_api_key": "", "llm_pool_enabled": false}); err != nil {
		t.Fatal(err)
	}
	result, err = search.Call(ctx, json.RawMessage(`{"query":"fixture"}`), nil)
	if err != nil || !result.IsError || searchCalls != 2 {
		t.Fatal("disabled global search continued to send requests")
	}
}

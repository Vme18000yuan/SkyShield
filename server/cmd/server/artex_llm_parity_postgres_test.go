package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/transcript"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	infra "github.com/yyhuni/lunafox/server/internal/modules/aisettings/infrastructure"
	repository "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func verifyARTEXLLMParityPostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	t.Setenv("TIANQIONG_AI_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	var admin, analyst int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&admin); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&analyst); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := repository.NewLLMProfileStore(orm)
	facade := settings.NewLLMProfileFacade(store, airuntime.SecretBox{})
	resolver := infra.NewLLMResolver(store, airuntime.SecretBox{})
	draft, err := facade.Save(ctx, admin, "", settings.ProfileInput{Name: "ARTEX keyless draft", Provider: "openai", Model: "draft"})
	if err != nil || draft.HasAPIKey {
		t.Fatalf("keyless profile: %+v %v", draft, err)
	}
	if _, _, configured, err := resolver.ResolveProfile(ctx, analyst, draft.ID); err != nil || configured {
		t.Fatalf("keyless profile became runnable: %v %v", configured, err)
	}
	if err := facade.Delete(ctx, admin, draft.ID); err != nil {
		t.Fatal(err)
	}
	var captured map[string]any
	var capturedSession string
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer llm-parity-key" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			_, _ = w.Write([]byte(`{"data":[{"id":"fixture-a"},{"id":"fixture-b"}]}`))
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Error(err)
		}
		capturedSession = r.Header.Get("X-Run-Session")
		if captured["model"] == "parity-model" && capturedSession == "" {
			t.Error("session header was not applied")
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer good.Close()
	streaming := false
	options := settings.DefaultLLMOptions()
	options.Streaming = &streaming
	options.ContextWindowK = 64
	options.MaxTokens = 321
	options.MaxTokensField = "max_completion_tokens"
	options.ReasoningEffort = "high"
	options.SessionHeaderKey = "X-Run-Session"
	options.Proxy = "http://user:proxy-fixture-secret@127.0.0.1:9"
	options.Retry.Connect.Attempts = -1
	options.Retry.Empty.Attempts = -1
	options.Retry.Stream.Attempts = -1
	input := settings.ProfileInput{Name: "ARTEX full options", Provider: "openai", Model: "parity-model", BaseURL: good.URL, APIKey: "llm-parity-key", IsDefault: true, Options: &options}
	profile, err := facade.Save(ctx, admin, "", input)
	if err != nil {
		t.Fatal(err)
	}
	provider, _, configured, err := resolver.ResolveProfile(ctx, analyst, profile.ID)
	if err != nil || !configured {
		t.Fatalf("provider: %v", err)
	}
	other, _, _, err := resolver.ResolveProfile(ctx, analyst, profile.ID)
	if err != nil || provider != other {
		t.Fatal("profile provider was not shared")
	}
	run := transcript.WithSessionID(ctx, "parity-session")
	if _, _, _, err := provider.Complete(run, llm.CompletionRequest{Messages: []llm.Message{llm.UserText("ping")}}); err != nil {
		t.Fatal(err)
	}
	if captured["max_completion_tokens"] != float64(321) || captured["max_tokens"] != nil || captured["reasoning_effort"] != "high" {
		t.Fatalf("options not applied: %+v", captured)
	}
	if capturedSession != "parity-session" {
		t.Fatal("explicit session header was not preserved")
	}
	modelOptions := airuntime.ModelOptions(provider)
	if modelOptions.ContextWindow != 64000 || !modelOptions.NonStreaming || modelOptions.MaxTokens != 321 {
		t.Fatalf("harness options: %+v", modelOptions)
	}
	runner, err := airuntime.NewChatRunner(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if reply, err := runner.Run(ctx, airuntime.ChatRun{TenantID: "00000000-0000-0000-0000-000000000001", UserID: admin, SessionID: uuid.NewString(), ExecutionID: uuid.NewString(), AgentKey: "auto", SystemPrompt: "Reply OK", Message: "ping", Provider: provider, Model: "parity-model", MaxTurns: 1, MaxDuration: 10 * time.Second}, nil); err != nil || reply != "OK" {
		t.Fatalf("configured non-streaming chat: %q %v", reply, err)
	}
	if captured["stream"] != false || captured["max_completion_tokens"] != float64(321) {
		t.Fatalf("chat ignored profile wire options: %+v", captured)
	}
	var proxyCipher []byte
	var rawOptions string
	if err := db.QueryRowContext(ctx, `SELECT encrypted_proxy_url,options::text FROM ai_llm_profile WHERE id=$1`, profile.ID).Scan(&proxyCipher, &rawOptions); err != nil || bytes.Contains(proxyCipher, []byte("proxy-fixture-secret")) || strings.Contains(rawOptions, "proxy-fixture-secret") {
		t.Fatalf("proxy was not encrypted: %v", err)
	}
	input.APIKey = ""
	input.IsDefault = false
	input.PreserveDefault = true
	if _, err := facade.Save(ctx, admin, profile.ID, input); err != nil {
		t.Fatal(err)
	}
	current, err := store.GetForAdmin(ctx, admin, profile.ID)
	if err != nil || !current.IsDefault || !current.HasAPIKey {
		t.Fatal("editing active profile lost activation or its key")
	}
	models, err := facade.FetchModels(ctx, admin, settings.ModelProbeInput{Provider: "openai", BaseURL: good.URL, ProfileID: profile.ID})
	if err != nil || len(models) != 2 {
		t.Fatalf("models: %v %v", models, err)
	}
	if _, err := facade.FetchModels(ctx, analyst, settings.ModelProbeInput{Provider: "openai", BaseURL: good.URL, APIKey: "llm-parity-key"}); !errors.Is(err, settings.ErrForbidden) {
		t.Fatalf("member accessed admin model discovery: %v", err)
	}
	var failures atomic.Int64
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failures.Add(1)
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid llm-parity-key","type":"authentication_error"}}`))
	}))
	defer bad.Close()
	input.Name = "ARTEX failover head"
	input.BaseURL = bad.URL
	input.APIKey = "llm-parity-key"
	input.IsDefault = true
	input.PreserveDefault = false
	head, err := facade.Save(ctx, admin, "", input)
	if err != nil {
		t.Fatal(err)
	}
	state, err := facade.RuntimeSettings(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	state.PoolEnabled = true
	if err := facade.SaveRuntimeSettings(ctx, admin, state); err != nil {
		t.Fatal(err)
	}
	pool, _, _, err := resolver.Resolve(ctx, analyst)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := pool.Complete(run, llm.CompletionRequest{Messages: []llm.Message{llm.UserText("fallback")}}); err != nil {
		t.Fatal(err)
	}
	status, err := resolver.PoolStatus(ctx, admin)
	if err != nil || status.Chain[0].ProfileID != head.ID || status.Chain[0].State != "tripped" || strings.Contains(status.Chain[0].LastError, "llm-parity-key") {
		t.Fatalf("breaker state: %+v %v", status, err)
	}
	if failures.Load() != 1 {
		t.Fatalf("deterministic auth error retried %d times", failures.Load())
	}
	if _, err := resolver.ResetPool(ctx, admin, head.ID); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM ai_llm_health WHERE runtime_id=$1`, head.RuntimeID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("breaker reset was not durable: %d %v", remaining, err)
	}
	state.PoolEnabled = false
	if err := facade.SaveRuntimeSettings(ctx, admin, state); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id    string
		input settings.ProfileInput
	}{{head.ID, input}, {profile.ID, input}} {
		clear := item.input
		clear.Name = "cleanup-" + item.id
		clear.IsDefault = false
		clear.PreserveDefault = false
		clear.APIKey = ""
		if _, err := facade.Save(ctx, admin, item.id, clear); err != nil {
			t.Fatal(err)
		}
		if err := facade.Delete(ctx, admin, item.id); err != nil {
			t.Fatal(err)
		}
	}
}

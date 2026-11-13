package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
	"github.com/gin-gonic/gin"
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

type assetFixtureProvider struct {
	mu       sync.Mutex
	requests []llm.CompletionRequest
	targetID int
}

type fixtureTokenVersions struct{}

func (fixtureTokenVersions) GetTokenVersion(context.Context, int) (int, error) { return 0, nil }

func (p *assetFixtureProvider) Stream(_ context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	p.mu.Lock()
	index := len(p.requests)
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	return func(yield func(llm.StreamEvent, error) bool) {
		events := []llm.StreamEvent{{Type: llm.SEMessageStart}}
		if index == 0 {
			events = append(events,
				llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "asset-tool-1", ToolName: "search_platform_assets"},
				llm.StreamEvent{Type: llm.SEToolInputJSON, Text: `{"query":"Case%2FPath","type":"service"}`},
				llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "skill-tool-1", ToolName: "Skill"},
				llm.StreamEvent{Type: llm.SEToolInputJSON, Text: `{"name":"fixture-skill"}`},
				llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"})
		} else if index == 1 {
			events = append(events,
				llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "asset-tool-2", ToolName: "insert_assets"},
				llm.StreamEvent{Type: llm.SEToolInputJSON, Text: fmt.Sprintf(`{"assets":[{"type":"service","url":"https://api.runtime-write.example/from-ai","target_id":%d,"page_title":"AI observation"}]}`, p.targetID)},
				llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"})
		} else {
			events = append(events,
				llm.StreamEvent{Type: llm.SETextDelta, Text: "已查询资产"},
				llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"})
		}
		events = append(events, llm.StreamEvent{Type: llm.SEMessageStop})
		for _, event := range events {
			if !yield(event, nil) {
				return
			}
		}
	}
}

func (p *assetFixtureProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, req) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func verifyAIConversationRuntimeContract(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var owner int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := airuntime.NewChatRunner(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var runtimeTarget int
	if err := db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES('runtime-write.example','domain') RETURNING id`).Scan(&runtimeTarget); err != nil {
		t.Fatal(err)
	}
	provider := &assetFixtureProvider{targetID: runtimeTarget}
	assetReader := assetapp.NewFacade(assetrepo.NewAssetRegistryStore(orm))
	assetWriter := assetapp.NewRegistrationFacade(assetrepo.NewAssetRegistrationStore(orm, assetRegistrationCreators(orm)), airuntime.SecretBox{})
	settings := settingsapp.NewAgentSettingFacade(settingsrepo.NewAgentSettingStore(orm))
	facade := aiapp.NewConversationFacade(airepo.NewConversationStore(orm), runner,
		func(context.Context, int) (llm.Provider, string, bool, error) { return provider, "fixture", true, nil }, airuntime.AssetToolResolver(assetReader, assetWriter), settings.Resolve)
	facade.SetSkillResolver(settingsapp.NewSkillFacade(settingsrepo.NewSkillStore(orm)).Resolve)
	jobCtx, cancel := context.WithCancel(ctx)
	facade.Start(jobCtx)
	defer func() { cancel(); <-facade.Done() }()
	conversation, err := facade.Create(ctx, owner, "资产调查")
	if err != nil {
		t.Fatal(err)
	}
	config, err := settings.Resolve(ctx, owner, "assistant")
	if err != nil {
		t.Fatal(err)
	}
	config.Enabled = false
	if err := settings.Save(ctx, owner, config); err != nil {
		t.Fatal(err)
	}
	if _, err := facade.Send(ctx, owner, conversation.ID, "查询资产"); !errors.Is(err, aiapp.ErrAgentDisabled) {
		t.Fatalf("disabled assistant was allowed to run: %v", err)
	}
	config.Enabled = true
	if err := settings.Save(ctx, owner, config); err != nil {
		t.Fatal(err)
	}
	runID, err := facade.Send(ctx, owner, conversation.ID, "查询资产")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		page, err := facade.Activities(ctx, owner, conversation.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if !page.Running {
			if page.RunStatus != "completed" {
				t.Fatalf("run status: %+v", page)
			}
			var hasTool, hasResult bool
			for _, activity := range page.Items {
				if activity.Kind == "tool_use" && activity.Tool == "search_platform_assets" {
					hasTool = true
				}
				if activity.Kind == "result" && activity.Detail == "已查询资产" {
					hasResult = true
				}
			}
			if !hasTool || !hasResult {
				t.Fatalf("missing runtime events: %+v", page.Items)
			}
			var projectedTitle string
			if err := db.QueryRowContext(ctx, `SELECT title FROM website WHERE target_id=$1 AND url='https://api.runtime-write.example/from-ai'`, runtimeTarget).Scan(&projectedTitle); err != nil || projectedTitle != "AI observation" {
				t.Fatalf("AI asset did not return to the original Website table: %q %v", projectedTitle, err)
			}
			executions, err := facade.ToolExecutions(ctx, owner, "search_platform_assets", 0)
			if err != nil || len(executions) != 1 || executions[0].Status != "succeeded" {
				t.Fatalf("tool execution query: %+v, %v", executions, err)
			}
			detail, err := facade.ToolExecution(ctx, owner, executions[0].ID)
			if err != nil || !strings.Contains(detail.Output, "registry-a.example") {
				t.Fatalf("tool execution detail: %+v, %v", detail, err)
			}
			provider.mu.Lock()
			if len(provider.requests) > 0 && !strings.Contains(strings.Join(provider.requests[0].System, "\n"), "fixture-assistant-instruction") {
				provider.mu.Unlock()
				t.Fatal("assistant settings were not applied to model prompt")
			}
			if len(provider.requests) < 2 {
				provider.mu.Unlock()
				t.Fatal("model did not consume tool result")
			}
			encoded, marshalErr := json.Marshal(provider.requests[1].Messages)
			provider.mu.Unlock()
			if marshalErr != nil || !strings.Contains(string(encoded), "registry-a.example") || !strings.Contains(string(encoded), "fixture-skill-instruction") {
				t.Fatalf("unified asset missing from tool result: %s (%v)", encoded, marshalErr)
			}
			verifyAIConversationHTTPContract(t, db, facade, owner, conversation.ID)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("AI run %s did not finish", runID)
}

func verifyAIConversationHTTPContract(t *testing.T, db *sql.DB, facade *aiapp.ConversationFacade, owner int, conversation string) {
	t.Helper()
	var other int
	if err := db.QueryRow(`SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	manager := auth.NewJWTManager("fixture-jwt-secret", time.Minute, time.Hour)
	ownerToken, _, err := manager.GenerateAccessToken(owner, "registry-admin", 0)
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
	airouter.RegisterAIWorkbenchRoutes(group, aihandler.NewConversationHandler(facade))
	request := func(path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	path := "/v1/aiConversations/" + conversation + "/activities"
	if response := request(path, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("missing token: %d", response.Code)
	}
	if response := request(path, otherToken); response.Code != http.StatusNotFound {
		t.Fatalf("other user's conversation: %d", response.Code)
	}
	if response := request(path, ownerToken); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "已查询资产") {
		t.Fatalf("owner activity: %d %s", response.Code, response.Body.String())
	}
	if response := request("/v1/aiToolExecutions", ownerToken); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "search_platform_assets") {
		t.Fatalf("owner tool execution: %d %s", response.Code, response.Body.String())
	}
}

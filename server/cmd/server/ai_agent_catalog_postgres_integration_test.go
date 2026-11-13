package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/auth"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	settingshandler "github.com/yyhuni/lunafox/server/internal/modules/aisettings/handler"
	settingsinfra "github.com/yyhuni/lunafox/server/internal/modules/aisettings/infrastructure"
	settingsrepo "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	settingsrouter "github.com/yyhuni/lunafox/server/internal/modules/aisettings/router"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	aihandler "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/handler"
	aiinfra "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/infrastructure"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	airouter "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/router"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type captureModelChatRunner struct{ runs chan airuntime.ChatRun }

func (r *captureModelChatRunner) Run(_ context.Context, run airuntime.ChatRun, _ func(airuntime.Activity)) (string, error) {
	r.runs <- run
	return "ok", nil
}

func verifyAIAgentCatalogPostgresContract(t *testing.T, ctx context.Context, db *sql.DB) {
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
	profileStore := settingsrepo.NewLLMProfileStore(orm)
	box := airuntime.SecretBox{}
	profiles := settingsapp.NewLLMProfileFacade(profileStore, box)
	profile, err := profiles.Save(ctx, admin, "", settingsapp.ProfileInput{Name: "Agent fixture model", Provider: "openai", Model: "agent-fixture", BaseURL: "https://fixture.invalid/v1", APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	settings := settingsapp.NewAgentSettingFacade(settingsrepo.NewAgentSettingStore(orm))
	agents := settingsapp.NewAgentCatalogFacade(settingsrepo.NewAgentCatalogStore(orm), settings.Resolve)
	planner, err := agents.Get(ctx, admin, "planner")
	if err != nil {
		t.Fatal(err)
	}
	original := planner.SystemPrompt
	planner.SystemPrompt = "目标: {{.Goal}}；目录: {{.DataDir}}"
	planner.MaxTurns, planner.RunSeconds = 17, 90
	planner.WrapupPrompt, planner.WrapupMaxTurns = "test run wrapup", 4
	planner.TaskTimeoutPrompt, planner.TaskTimeoutMaxTurns, planner.UpdateWrapup = "test task wrapup", 7, true
	planner.LLMProfileID, planner.UpdateLLMBinding = &profile.ID, true
	if _, err := agents.Update(ctx, member, "planner", *planner); !errors.Is(err, settingsapp.ErrForbidden) {
		t.Fatalf("non-admin changed agent: %v", err)
	}
	saved, err := agents.Update(ctx, admin, "planner", *planner)
	if err != nil {
		t.Fatal(err)
	}
	if saved.LLMProfileID == nil || *saved.LLMProfileID != profile.ID || saved.SystemPrompt != planner.SystemPrompt || saved.MaxTurns != 17 {
		t.Fatalf("configuration not persisted: %+v", saved)
	}
	if saved.WrapupPrompt != "test run wrapup" || saved.WrapupMaxTurns != 4 || saved.TaskTimeoutPrompt != "test task wrapup" || saved.TaskTimeoutMaxTurns != 7 {
		t.Fatalf("wrap-up configuration was not saved: %+v", saved)
	}
	provider, model, ok, err := settingsinfra.NewLLMResolver(profileStore, box).ResolveProfile(ctx, member, profile.ID)
	if err != nil || !ok || provider == nil || model != "agent-fixture" {
		t.Fatalf("member could not resolve bound model: %s %v %v", model, ok, err)
	}
	versions, err := agents.PromptVersions(ctx, member, "planner")
	if err != nil || len(versions) != 2 || versions[0].Template != planner.SystemPrompt || versions[1].Template != original {
		t.Fatalf("prompt versions: %+v %v", versions, err)
	}
	preview, err := agents.PreviewPrompt(ctx, member, "planner", planner.SystemPrompt)
	if err != nil || strings.Contains(preview, "{{") || !strings.Contains(preview, "/workspace") {
		t.Fatalf("prompt preview: %q %v", preview, err)
	}
	planner.SystemPrompt = "{{.UnknownVariable}}"
	if _, err := agents.Update(ctx, admin, "planner", *planner); !errors.Is(err, settingsapp.ErrAgentCatalogInvalid) {
		t.Fatalf("invalid prompt variable accepted: %v", err)
	}
	conversationProfile, err := profiles.Save(ctx, admin, "", settingsapp.ProfileInput{Name: "Conversation fixture model", Provider: "openai", Model: "conversation-fixture", BaseURL: "https://fixture.invalid/v1", APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	visible, err := profiles.ListForMember(ctx, member)
	if err != nil || len(visible) < 2 || len(visible[0].EncryptedKey) != 0 {
		t.Fatalf("member model options: %+v %v", visible, err)
	}
	chatRunner := &captureModelChatRunner{runs: make(chan airuntime.ChatRun, 3)}
	fakeProvider := &taskPipelineProvider{}
	chat := aiapp.NewConversationFacade(airepo.NewConversationStore(orm), chatRunner, func(context.Context, int) (llm.Provider, string, bool, error) {
		return fakeProvider, "global", true, nil
	}, nil)
	chat.SetAgentCatalogResolver(agents.ResolveForChat)
	chat.SetProfileProviderResolver(func(_ context.Context, _ int, id string) (llm.Provider, string, bool, error) {
		return fakeProvider, id, true, nil
	})
	taskReader := aiapp.NewTaskFacade(airepo.NewTaskStore(orm), nil, nil)
	chat.SetAutoTools(func(user int) []tool.CoreTool { return aiinfra.AutoTaskReadTools(taskReader, user) })
	chatCtx, cancelChat := context.WithCancel(ctx)
	chat.Start(chatCtx)
	defer func() { cancelChat(); <-chat.Done() }()
	conversation, err := chat.CreateWithAgent(ctx, admin, "model precedence", "planner")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.SetProfile(ctx, member, conversation.ID, &conversationProfile.ID); !errors.Is(err, aiapp.ErrConversationNotFound) {
		t.Fatalf("member changed another user's conversation: %v", err)
	}
	if err := chat.SetProfile(ctx, admin, conversation.ID, &conversationProfile.ID); err != nil {
		t.Fatal(err)
	}
	manager := auth.NewJWTManager("fixture-jwt-secret", time.Minute, time.Hour)
	adminToken, _, err := manager.GenerateAccessToken(admin, "registry-admin", 0)
	if err != nil {
		t.Fatal(err)
	}
	memberToken, _, err := manager.GenerateAccessToken(member, "ai-other", 0)
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	group := engine.Group("/v1")
	group.Use(middleware.AuthMiddleware(manager, fixtureTokenVersions{}))
	settingsrouter.RegisterAISettingsRoutes(group, settingshandler.NewLLMProfileHandler(profiles))
	airouter.RegisterAIWorkbenchRoutes(group, aihandler.NewConversationHandler(chat))
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	if response := request(http.MethodGet, "/v1/aiModels", "", memberToken); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "agent-fixture") || strings.Contains(response.Body.String(), "test-key") || strings.Contains(response.Body.String(), "fixture.invalid") {
		t.Fatalf("member models response: %d %s", response.Code, response.Body.String())
	}
	modelPath := "/v1/aiConversations/" + conversation.ID + "/model"
	if response := request(http.MethodPut, modelPath, `{}`, adminToken); response.Code != http.StatusBadRequest {
		t.Fatalf("missing model field accepted: %d", response.Code)
	}
	if response := request(http.MethodPut, modelPath, `{"llmProfileId":null}`, memberToken); response.Code != http.StatusNotFound {
		t.Fatalf("cross-owner model change accepted: %d", response.Code)
	}
	title, pinned := "置顶模型测试", true
	if err := chat.UpdateMetadata(ctx, member, conversation.ID, &title, &pinned); !errors.Is(err, aiapp.ErrConversationNotFound) {
		t.Fatalf("member changed another user's conversation metadata: %v", err)
	}
	if err := chat.UpdateMetadata(ctx, admin, conversation.ID, &title, &pinned); err != nil {
		t.Fatal(err)
	}
	listed, err := chat.List(ctx, admin)
	if err != nil || len(listed) == 0 || listed[0].ID != conversation.ID || listed[0].Title != title || !listed[0].Pinned || listed[0].LLMProfileID == nil || *listed[0].LLMProfileID != conversationProfile.ID {
		t.Fatalf("conversation metadata/model list: %+v %v", listed, err)
	}
	checkModel := func(expected string) {
		t.Helper()
		if _, err := chat.Send(ctx, admin, conversation.ID, "模型优先级测试"); err != nil {
			t.Fatal(err)
		}
		select {
		case run := <-chatRunner.runs:
			if run.Model != expected {
				t.Fatalf("conversation model = %q, want %q", run.Model, expected)
			}
			if run.Wrapup.Prompt != "test run wrapup" || run.Wrapup.MaxTurns != 4 {
				t.Fatalf("chat did not receive wrap-up config: %+v", run.Wrapup)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("conversation did not run")
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			page, err := chat.Activities(ctx, admin, conversation.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			if !page.Running {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("conversation did not finish")
	}
	checkModel(profile.ID)
	saved.LLMProfileID, saved.UpdateLLMBinding = nil, true
	if _, err := agents.Update(ctx, admin, "planner", *saved); err != nil {
		t.Fatal(err)
	}
	checkModel(conversationProfile.ID)
	if err := chat.SetProfile(ctx, admin, conversation.ID, nil); err != nil {
		t.Fatal(err)
	}
	checkModel("global")
	autoConversation, err := chat.CreateWithAgent(ctx, admin, "auto task tools", "auto")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Send(ctx, admin, autoConversation.ID, "查看任务"); err != nil {
		t.Fatal(err)
	}
	select {
	case run := <-chatRunner.runs:
		tools := map[string]bool{}
		for _, entry := range run.Tools {
			tools[entry.Name()] = true
		}
		if !tools["list_tasks"] || !tools["get_task_graph"] || !tools["list_task_findings"] {
			t.Fatalf("Auto task tools not injected: %v", tools)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Auto conversation did not start")
	}
	if err := chat.SetProfile(ctx, admin, conversation.ID, &conversationProfile.ID); err != nil {
		t.Fatal(err)
	}
	if err := profiles.Delete(ctx, admin, conversationProfile.ID); err != nil {
		t.Fatal(err)
	}
	listed, err = chat.List(ctx, admin)
	if err != nil || len(listed) == 0 || listed[0].LLMProfileID != nil {
		t.Fatalf("deleted model left conversation binding: %+v %v", listed, err)
	}
	saved.LLMProfileID, saved.UpdateLLMBinding = &profile.ID, true
	if _, err := agents.Update(ctx, admin, "planner", *saved); err != nil {
		t.Fatal(err)
	}
	if err := profiles.Delete(ctx, admin, profile.ID); err != nil {
		t.Fatal(err)
	}
	retained, err := agents.Get(ctx, member, "planner")
	if err != nil || retained.LLMProfileID != nil || retained.SystemPrompt != saved.SystemPrompt {
		t.Fatalf("profile deletion lost config or kept binding: %+v %v", retained, err)
	}
	custom, err := agents.Create(ctx, admin, airuntime.ChatAgentDefinition{Key: "fixture_agent", Name: "Fixture", SystemPrompt: "工作目录 {{.DataDir}}"})
	if err != nil {
		t.Fatal(err)
	}
	custom.SystemPrompt = "现在 {{.Now}}"
	if _, err := agents.Update(ctx, admin, custom.Key, *custom); err != nil {
		t.Fatal(err)
	}
	versions, err = agents.PromptVersions(ctx, member, custom.Key)
	if err != nil || len(versions) != 2 {
		t.Fatalf("custom prompt versions: %+v %v", versions, err)
	}
	if err := agents.Delete(ctx, admin, custom.Key); err != nil {
		t.Fatal(err)
	}
	// Do not influence the subsequent task pipeline contract.
	if _, err := db.ExecContext(ctx, `DELETE FROM ai_builtin_agent_config WHERE agent_key='planner'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM ai_agent_prompt_version WHERE agent_key='planner'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM ai_agent_wrapup_config WHERE agent_key='planner'`); err != nil {
		t.Fatal(err)
	}
}

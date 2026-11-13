package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/transcript"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/auth"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	settingshandler "github.com/yyhuni/lunafox/server/internal/modules/aisettings/handler"
	settingsinfra "github.com/yyhuni/lunafox/server/internal/modules/aisettings/infrastructure"
	settingsrepo "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	settingsrouter "github.com/yyhuni/lunafox/server/internal/modules/aisettings/router"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyARTEXLLMUsagePostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	t.Setenv("TIANQIONG_AI_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("u", 32))))
	t.Setenv("TIANQIONG_AI_ENCRYPTION_KEY_FILE", "")
	owner, target, _ := taskFixtureScope(t, ctx, db, "llm-usage.example")
	var other int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = orm.Exec(`DELETE FROM ai_llm_usage`).Error; err != nil {
		t.Fatal(err)
	}
	task, err := aiapp.NewTaskFacade(airepo.NewTaskStore(orm), nil, nil).Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "用量隔离测试", TargetID: target})
	if err != nil {
		t.Fatal(err)
	}
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		if input["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"RAW-STREAM\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7}}\n\ndata: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`))
	}))
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"fixture auth failure"}}`))
	}))
	defer bad.Close()
	profiles := settingsrepo.NewLLMProfileStore(orm)
	settings := settingsapp.NewLLMProfileFacade(profiles, airuntime.SecretBox{})
	options := settingsapp.DefaultLLMOptions()
	streaming := false
	options.Streaming = &streaming
	options.Retry.Connect.Attempts, options.Retry.Empty.Attempts, options.Retry.Stream.Attempts = -1, -1, -1
	goodProfile, err := settings.Save(ctx, owner, "", settingsapp.ProfileInput{Name: "用量可用模型", Provider: "openai", Model: "usage-good", BaseURL: good.URL, APIKey: "secret-fixture-key", IsDefault: true, Options: &options})
	if err != nil {
		t.Fatal(err)
	}
	usageStore := settingsrepo.NewLLMUsageStore(orm)
	runtimeSettingsFacade := settingsapp.NewRuntimeSettingFacade(settingsrepo.NewToolCatalogStore(orm), airuntime.SecretBox{}, profiles)
	if _, err = runtimeSettingsFacade.Patch(ctx, owner, map[string]any{"llm_record": false}); err != nil {
		t.Fatal(err)
	}
	configuredKey := os.Getenv("TIANQIONG_AI_ENCRYPTION_KEY")
	if err = os.Setenv("TIANQIONG_AI_ENCRYPTION_KEY", ""); err != nil {
		t.Fatal(err)
	}
	_, missingKeyErr := runtimeSettingsFacade.Patch(ctx, owner, map[string]any{"llm_record": true})
	if err = os.Setenv("TIANQIONG_AI_ENCRYPTION_KEY", configuredKey); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(missingKeyErr, airuntime.ErrEncryptionKeyUnavailable) {
		t.Fatalf("recording enabled without encryption key: %v", missingKeyErr)
	}
	recorder := settingsapp.NewLLMRecordingRecorder(usageStore, usageStore, airuntime.SecretBox{})
	recordings := settingsapp.NewLLMRecordingFacade(usageStore, airuntime.SecretBox{})
	resolver := settingsinfra.NewLLMResolver(profiles, airuntime.SecretBox{})
	resolver.SetUsageSink(recorder)
	provider, _, configured, err := resolver.ResolveProfileExclusive(ctx, owner, goodProfile.ID)
	if err != nil || !configured {
		t.Fatalf("usage provider: %v", err)
	}
	otherProvider, _, configured, err := resolver.ResolveProfileExclusive(ctx, other, goodProfile.ID)
	if err != nil || !configured {
		t.Fatalf("other usage provider: %v", err)
	}
	taskCtx := airuntime.WithTaskExecution(ctx, airuntime.TaskExecution{ID: task.ID, ExplorationID: task.ExplorationID, UserID: owner})
	taskCtx = transcript.WithSessionID(taskCtx, fmt.Sprintf("exp%d-worker-i1", task.ExplorationID))
	conversation := uuid.NewString()
	otherCtx := airuntime.WithLLMUsageScope(ctx, airuntime.LLMUsageScope{UserID: other, ConversationID: conversation, Worker: "auto"})
	var wg sync.WaitGroup
	for _, call := range []struct {
		ctx      context.Context
		provider llm.Provider
	}{{taskCtx, provider}, {taskCtx, provider}, {otherCtx, otherProvider}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, _, callErr := call.provider.Complete(call.ctx, llm.CompletionRequest{Messages: []llm.Message{llm.UserText("secret-fixture-prompt")}}); callErr != nil {
				t.Errorf("metered model request: %v", callErr)
			}
		}()
	}
	wg.Wait()
	if page, err := recordings.List(ctx, owner, settingsapp.LLMRecordingFilter{Size: 25}); err != nil || page.Total != 0 {
		t.Fatalf("disabled model recording: %+v %v", page, err)
	}
	if _, err = runtimeSettingsFacade.Patch(ctx, owner, map[string]any{"llm_record": true}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = provider.Complete(taskCtx, llm.CompletionRequest{Messages: []llm.Message{llm.UserText("recording-secret-complete")}}); err != nil {
		t.Fatal(err)
	}
	for _, streamErr := range provider.Stream(taskCtx, llm.CompletionRequest{Messages: []llm.Message{llm.UserText("recording-secret-stream")}}) {
		if streamErr != nil {
			t.Fatal(streamErr)
		}
	}
	if _, err = runtimeSettingsFacade.Patch(ctx, owner, map[string]any{"llm_record": false}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = provider.Complete(taskCtx, llm.CompletionRequest{Messages: []llm.Message{llm.UserText("recording-disabled")}}); err != nil {
		t.Fatal(err)
	}
	recordPage, err := recordings.List(ctx, owner, settingsapp.LLMRecordingFilter{TaskID: task.ID, Size: 25})
	if err != nil || recordPage.Total != 2 || len(recordPage.Items) != 2 {
		t.Fatalf("enabled model recordings: %+v %v", recordPage, err)
	}
	recordTasks, err := recordings.Tasks(ctx, owner, task.Name)
	if err != nil || len(recordTasks.Items) != 1 || recordTasks.Items[0].ID != fmt.Sprint(task.ID) || recordTasks.Items[0].Name != task.Name || recordTasks.Items[0].Count != 2 {
		t.Fatalf("recording task names: %+v %v", recordTasks, err)
	}
	if foreign, err := recordings.Tasks(ctx, other, task.Name); err != nil || len(foreign.Items) != 0 {
		t.Fatalf("foreign recording task names: %+v %v", foreign, err)
	}
	var streamedID string
	for _, record := range recordPage.Items {
		detail, detailErr := recordings.Detail(ctx, owner, record.ID)
		if detailErr != nil || detail.TaskID == nil || *detail.TaskID != task.ID || detail.Worker != "worker" || detail.Payload.Truncated {
			t.Fatalf("recording detail: %+v %v", detail, detailErr)
		}
		if strings.Contains(detail.Payload.RequestBody, "recording-secret-stream") {
			streamedID = record.ID
			if !strings.Contains(detail.Payload.ResponseBody, "RAW-STREAM") || !strings.Contains(detail.Payload.ResponseBody, "data:") {
				t.Fatalf("raw stream missing: %+v", detail.Payload)
			}
		}
	}
	if streamedID == "" {
		t.Fatal("real streaming request was not recorded")
	}
	if restored, err := settingsapp.NewLLMRecordingFacade(settingsrepo.NewLLMUsageStore(orm), airuntime.SecretBox{}).Detail(ctx, owner, streamedID); err != nil || !strings.Contains(restored.Payload.ResponseBody, "RAW-STREAM") {
		t.Fatalf("persisted recording could not be reopened: %+v %v", restored, err)
	}
	var cipher []byte
	if err = db.QueryRowContext(ctx, `SELECT sealed_payload FROM ai_llm_record WHERE id=$1::uuid`, streamedID).Scan(&cipher); err != nil || strings.Contains(string(cipher), "recording-secret-stream") || strings.Contains(string(cipher), "RAW-STREAM") {
		t.Fatalf("model recording is not sealed: %v", err)
	}
	if _, err = recordings.Detail(ctx, other, streamedID); !errors.Is(err, settingsapp.ErrUsageTaskNotFound) {
		t.Fatalf("cross-account model recording: %v", err)
	}
	badProfile, err := settings.Save(ctx, owner, "", settingsapp.ProfileInput{Name: "用量失败模型", Provider: "openai", Model: "usage-bad", BaseURL: bad.URL, APIKey: "secret-fixture-key", IsDefault: true, Options: &options})
	if err != nil {
		t.Fatal(err)
	}
	runtimeSettings, err := settings.RuntimeSettings(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	runtimeSettings.PoolEnabled = true
	if err = settings.SaveRuntimeSettings(ctx, owner, runtimeSettings); err != nil {
		t.Fatal(err)
	}
	pool, _, configured, err := resolver.Resolve(ctx, owner)
	if err != nil || !configured {
		t.Fatalf("metered pool: %v", err)
	}
	if _, _, _, err = pool.Complete(taskCtx, llm.CompletionRequest{Messages: []llm.Message{llm.UserText("fallback")}}); err != nil {
		t.Fatal(err)
	}
	usage := settingsapp.NewLLMUsageFacade(usageStore)
	summary, err := usage.Summary(ctx, owner, 30)
	if err != nil || summary.Totals.Calls != 7 || summary.Totals.InputTokens != 66 || summary.Totals.OutputTokens != 42 || len(summary.Profiles) != 2 || len(summary.Daily) != 2 {
		t.Fatalf("usage totals or fallback: %+v %v", summary, err)
	}
	otherSummary, err := usage.Summary(ctx, other, 30)
	if err != nil || otherSummary.Totals.Calls != 1 || otherSummary.Totals.InputTokens != 11 || len(otherSummary.Profiles) != 1 {
		t.Fatalf("cross-account usage: %+v %v", otherSummary, err)
	}
	models, err := usage.TaskModels(ctx, owner, task.ID)
	if err != nil || len(models) != 2 || models[0].Model != "usage-good" || models[0].Calls != 6 || models[1].Model != "usage-bad" || models[1].Calls != 1 {
		t.Fatalf("per-task model usage: %+v %v", models, err)
	}
	if _, err = usage.TaskModels(ctx, other, task.ID); !errors.Is(err, settingsapp.ErrUsageTaskNotFound) {
		t.Fatalf("cross-account task usage: %v", err)
	}
	var badRows int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_llm_usage WHERE owner_user_id=$1 AND profile_id=$2::uuid AND status='error'`, owner, badProfile.ID).Scan(&badRows); err != nil || badRows != 1 {
		t.Fatalf("failed model usage: %d %v", badRows, err)
	}
	var raw string
	if err = db.QueryRowContext(ctx, `SELECT row_to_json(u)::text FROM ai_llm_usage u WHERE owner_user_id=$1 LIMIT 1`, owner).Scan(&raw); err != nil || strings.Contains(raw, "secret-fixture-prompt") || strings.Contains(raw, "secret-fixture-key") {
		t.Fatalf("model usage stored secret content: %v", err)
	}
	jwt := auth.NewJWTManager("llm-usage-fixture", time.Minute, time.Hour)
	ownerToken, _, err := jwt.GenerateAccessToken(owner, "registry-admin", 0)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, _, err := jwt.GenerateAccessToken(other, "ai-other", 0)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/v1")
	group.Use(middleware.AuthMiddleware(jwt, fixtureTokenVersions{}))
	handler := settingshandler.NewLLMProfileHandler(nil)
	handler.SetUsage(usage)
	handler.SetRecordings(recordings)
	settingsrouter.RegisterAISettingsRoutes(group, handler)
	get := func(path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	if out := get("/v1/aiModelUsage", ""); out.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous usage: %d", out.Code)
	}
	if out := get("/v1/aiModelUsage?days=30", ownerToken); out.Code != http.StatusOK || out.Header().Get("Cache-Control") != "no-store" || !strings.Contains(out.Body.String(), `"calls":7`) {
		t.Fatalf("model usage HTTP: %d %s", out.Code, out.Body.String())
	}
	path := fmt.Sprintf("/v1/aiModelUsage/tasks/%d", task.ID)
	if out := get(path, otherToken); out.Code != http.StatusNotFound {
		t.Fatalf("cross-account task usage HTTP: %d", out.Code)
	}
	if out := get("/v1/aiModelUsage?days=0", ownerToken); out.Code != http.StatusBadRequest {
		t.Fatalf("invalid usage period: %d", out.Code)
	}
	if out := get("/v1/aiLlmRecords/"+streamedID, ownerToken); out.Code != http.StatusOK || out.Header().Get("Cache-Control") != "no-store" || !strings.Contains(out.Body.String(), "RAW-STREAM") {
		t.Fatalf("model recording HTTP: %d %s", out.Code, out.Body.String())
	}
	if out := get("/v1/aiLlmRecords/"+streamedID, otherToken); out.Code != http.StatusNotFound {
		t.Fatalf("foreign model recording HTTP: %d", out.Code)
	}
	if _, err = runtimeSettingsFacade.Patch(ctx, owner, map[string]any{"llm_record": true}); err != nil {
		t.Fatal(err)
	}
	on, lease, err := recorder.StartLLMRecording(ctx, airuntime.LLMUsageRecord{UserID: owner, TaskID: task.ID})
	if err != nil || !on {
		t.Fatalf("start model recording lease: %t %+v %v", on, lease, err)
	}
	remove := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/v1/aiLlmRecords?task=%d", task.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	if out := remove(otherToken); out.Code != http.StatusNotFound {
		t.Fatalf("foreign model recording deletion: %d", out.Code)
	}
	if out := remove(ownerToken); out.Code != http.StatusOK || !strings.Contains(out.Body.String(), `"deleted":2`) {
		t.Fatalf("delete task model recordings: %d %s", out.Code, out.Body.String())
	}
	if err = recorder.RecordLLMRecording(ctx, airuntime.LLMUsageRecord{UserID: owner, TaskID: task.ID, Status: "ok"}, lease, airuntime.LLMRecordingPayload{RequestBody: "stale"}); err != nil {
		t.Fatal(err)
	}
	if page, err := recordings.List(ctx, owner, settingsapp.LLMRecordingFilter{TaskID: task.ID, Size: 25}); err != nil || page.Total != 0 {
		t.Fatalf("deleted recording recreated by old request: %+v %v", page, err)
	}
	on, concurrentLease, err := recorder.StartLLMRecording(ctx, airuntime.LLMUsageRecord{UserID: owner, TaskID: task.ID})
	if err != nil || !on {
		t.Fatalf("new model recording lease: %t %+v %v", on, concurrentLease, err)
	}
	start := make(chan struct{})
	errorsCh := make(chan error, 8)
	for i := 0; i < cap(errorsCh); i++ {
		go func() {
			<-start
			errorsCh <- recorder.RecordLLMRecording(ctx, airuntime.LLMUsageRecord{UserID: owner, TaskID: task.ID, Status: "ok"}, concurrentLease, airuntime.LLMRecordingPayload{RequestBody: "concurrent"})
		}()
	}
	close(start)
	if _, err = recordings.DeleteTask(ctx, owner, task.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < cap(errorsCh); i++ {
		if err = <-errorsCh; err != nil {
			t.Fatalf("concurrent recording: %v", err)
		}
	}
	if page, err := recordings.List(ctx, owner, settingsapp.LLMRecordingFilter{TaskID: task.ID, Size: 25}); err != nil || page.Total != 0 {
		t.Fatalf("recording raced task deletion: %+v %v", page, err)
	}
	if _, err = runtimeSettingsFacade.Patch(ctx, owner, map[string]any{"llm_record": false}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE target SET deleted_at=clock_timestamp() WHERE id=$1`, target); err != nil {
		t.Fatal(err)
	}
	if out := get(path, ownerToken); out.Code != http.StatusNotFound {
		t.Fatalf("deleted target task usage HTTP: %d", out.Code)
	}
	if summary, err = usage.Summary(ctx, owner, 30); err != nil || summary.Totals.Calls != 7 {
		t.Fatalf("historical aggregate lost after target deletion: %+v %v", summary, err)
	}
}

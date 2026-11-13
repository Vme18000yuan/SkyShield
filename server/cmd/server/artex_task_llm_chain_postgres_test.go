package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Autumn-27/norma/llm"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	settingsinfra "github.com/yyhuni/lunafox/server/internal/modules/aisettings/infrastructure"
	settingsrepo "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyARTEXTaskLLMChainPostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	old, present := os.LookupEnv("TIANQIONG_AI_ENCRYPTION_KEY")
	t.Setenv("TIANQIONG_AI_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("r", 32))))
	defer func() {
		if present {
			_ = os.Setenv("TIANQIONG_AI_ENCRYPTION_KEY", old)
		} else {
			_ = os.Unsetenv("TIANQIONG_AI_ENCRYPTION_KEY")
		}
	}()
	var user, other int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var firstCalls, secondCalls atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if input.Model == "quota-fixture" {
			firstCalls.Add(1)
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"insufficient_quota","code":"insufficient_quota"}}`))
			return
		}
		secondCalls.Add(1)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"chain-ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer fixture.Close()
	profileStore := settingsrepo.NewLLMProfileStore(orm)
	profiles := settings.NewLLMProfileFacade(profileStore, airuntime.SecretBox{})
	resolver := settingsinfra.NewLLMResolver(profileStore, airuntime.SecretBox{})
	stream := false
	options := settings.DefaultLLMOptions()
	options.Streaming = &stream
	options.Retry.Connect.Attempts = -1
	options.Retry.Stream.Attempts = -1
	options.Retry.Empty.Attempts = -1
	create := func(name, model string) *settings.LLMProfile {
		t.Helper()
		item, err := profiles.Save(ctx, user, "", settings.ProfileInput{Name: name, Provider: "openai", Model: model, BaseURL: fixture.URL, APIKey: "fixture-key", Options: &options})
		if err != nil {
			t.Fatal(err)
		}
		return item
	}
	first := create("task quota model", "quota-fixture")
	second := create("task backup model", "healthy-fixture")
	store := airepo.NewTaskStore(orm)
	facade := aiapp.NewTaskFacade(store, nil, nil)
	task, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: user, Name: "模型链测试", Goal: "检查模型链", LLMProfileIDs: []string{first.ID, second.ID}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM ai_task WHERE id=$1`, task.ID)
		_ = profiles.Delete(context.Background(), user, first.ID)
		_ = profiles.Delete(context.Background(), user, second.ID)
	}()
	if len(task.LLMProfileIDs) != 2 || task.ActiveLLMProfileID == nil || *task.ActiveLLMProfileID != first.ID || task.LLMFailoverState != "ready" {
		t.Fatalf("initial task chain: %+v", task)
	}
	if _, err = facade.TaskModels(ctx, other, task.ID); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("task chain leaked to another owner: %v", err)
	}
	run, instance := uuid.NewString(), uuid.NewString()
	if _, err = db.ExecContext(ctx, `UPDATE ai_task SET status='running',queued=FALSE,active_run=$1,lease_owner=$2,lease_until=clock_timestamp()+INTERVAL '45 seconds' WHERE id=$3`, run, instance, task.ID); err != nil {
		t.Fatal(err)
	}
	execution := airuntime.TaskExecution{ID: task.ID, TenantID: task.TenantID, UserID: user, RunID: run}
	selectModel := func(ctx context.Context) (airuntime.TaskModelSelection, error) {
		state, err := store.TaskLLMState(ctx, user, task.ID)
		if err != nil {
			return airuntime.TaskModelSelection{}, err
		}
		if state.ActiveProfileID == nil {
			return airuntime.TaskModelSelection{}, airuntime.ErrTaskModelChainExhausted
		}
		provider, model, ok, err := resolver.ResolveProfileExclusive(ctx, user, *state.ActiveProfileID)
		if err != nil {
			return airuntime.TaskModelSelection{}, err
		}
		if !ok {
			return airuntime.TaskModelSelection{}, aiapp.ErrModelNotConfigured
		}
		return airuntime.TaskModelSelection{Provider: provider, Model: model, ProfileID: *state.ActiveProfileID, Revision: state.Revision}, nil
	}
	router := &airuntime.TaskModelRouter{Context: ctx, Select: selectModel, Exhaust: func(ctx context.Context, selection airuntime.TaskModelSelection, cause error) (airuntime.TaskModelTransition, error) {
		transition, err := store.ExhaustTaskLLMProfile(ctx, execution, selection.ProfileID, selection.Revision, cause.Error())
		return airuntime.TaskModelTransition{Stale: transition.Stale, Advanced: transition.Advanced, Exhausted: transition.Exhausted}, err
	}}
	message, _, _, err := router.Complete(ctx, llm.CompletionRequest{Messages: []llm.Message{llm.UserText("fixture")}})
	if err != nil || message.Text() != "chain-ok" || firstCalls.Load() != 1 || secondCalls.Load() != 1 {
		t.Fatalf("real HTTP task failover: %q %v first=%d second=%d", message.Text(), err, firstCalls.Load(), secondCalls.Load())
	}
	state, err := store.TaskLLMState(ctx, user, task.ID)
	if err != nil || state.State != "ready" || state.ActiveProfileID == nil || *state.ActiveProfileID != second.ID || state.Profiles[0].Status != "quota_exhausted" {
		t.Fatalf("quota transition not persisted: %+v %v", state, err)
	}
	var history, activities int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_task_llm_history WHERE task_id=$1 AND mode='automatic'`, task.ID).Scan(&history); err != nil || history != 1 {
		t.Fatalf("transition history: %d %v", history, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.activity WHERE exploration_id=$1 AND kind='llm_failover'`, task.ExplorationID).Scan(&activities); err != nil || activities != 1 {
		t.Fatalf("transition activity: %d %v", activities, err)
	}
	reset, err := facade.ReplaceTaskModels(ctx, user, task.ID, []string{first.ID, second.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan aiapp.TaskLLMTransition, 2)
	failures := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := store.ExhaustTaskLLMProfile(ctx, execution, first.ID, reset.Revision, "insufficient_quota")
			results <- result
			failures <- err
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	advanced, stale := 0, 0
	for result := range results {
		if result.Advanced {
			advanced++
		}
		if result.Stale {
			stale++
		}
	}
	if advanced != 1 || stale != 1 {
		t.Fatalf("concurrent quota transitions: advanced=%d stale=%d", advanced, stale)
	}
	before, err := store.TaskLLMState(ctx, user, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	selected := second.ID
	after, err := facade.ReplaceTaskModels(ctx, user, task.ID, []string{second.ID, first.ID}, &selected)
	if err != nil {
		t.Fatal(err)
	}
	late, err := store.ExhaustTaskLLMProfile(ctx, execution, second.ID, before.Revision, "insufficient_quota")
	if err != nil || !late.Stale {
		t.Fatalf("late failure changed edited chain: %+v %v", late, err)
	}
	if err = profiles.Delete(ctx, user, second.ID); err != nil {
		t.Fatal(err)
	}
	final, err := store.TaskLLMState(ctx, user, task.ID)
	if err != nil || len(final.Profiles) != 1 || final.ActiveProfileID == nil || *final.ActiveProfileID != first.ID || final.Revision <= after.Revision {
		t.Fatalf("profile deletion broke task cursor: %+v %v", final, err)
	}
}

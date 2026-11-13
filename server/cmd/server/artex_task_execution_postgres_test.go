package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	settingsrepo "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyARTEXTaskExecutionPostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	old, present := os.LookupEnv("TIANQIONG_AI_ENCRYPTION_KEY")
	t.Setenv("TIANQIONG_AI_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("w", 32))))
	defer func() {
		if present {
			_ = os.Setenv("TIANQIONG_AI_ENCRYPTION_KEY", old)
		} else {
			_ = os.Unsetenv("TIANQIONG_AI_ENCRYPTION_KEY")
		}
	}()
	var user int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	profileStore := settingsrepo.NewLLMProfileStore(orm)
	profiles := settings.NewLLMProfileFacade(profileStore, airuntime.SecretBox{})
	makeProfile := func(name string) *settings.LLMProfile {
		t.Helper()
		profile, err := profiles.Save(ctx, user, "", settings.ProfileInput{Name: name, Provider: "openai", Model: "fixture", APIKey: "fixture-key"})
		if err != nil {
			t.Fatal(err)
		}
		return profile
	}
	a := makeProfile("worker pool fixture A")
	b := makeProfile("worker pool fixture B")
	defer profiles.Delete(context.Background(), user, a.ID)
	defer profiles.Delete(context.Background(), user, b.ID)
	runtime := settings.NewRuntimeSettingFacade(settingsrepo.NewToolCatalogStore(orm), airuntime.SecretBox{}, profileStore)
	pool := []map[string]any{{"profile_id": a.ID, "workers": 2}, {"profile_id": b.ID, "workers": 1}}
	if _, err = runtime.Patch(ctx, user, map[string]any{"workers": float64(3), "worker_pools": pool, "task_concurrency_enabled": true, "task_concurrency_limit": float64(1)}); err != nil {
		t.Fatal(err)
	}
	defer runtime.Patch(context.Background(), user, map[string]any{"workers": float64(3), "worker_pools": []any{}, "task_concurrency_enabled": false})
	slots, err := runtime.WorkerSlots(ctx, user, 0)
	if err != nil || len(slots) != 3 || slots[0] != a.ID || slots[1] != a.ID || slots[2] != b.ID {
		t.Fatalf("global worker pools: %+v %v", slots, err)
	}
	override, err := runtime.WorkerSlots(ctx, user, 2)
	if err != nil || len(override) != 2 || override[0] != "" || override[1] != "" {
		t.Fatalf("per-task worker override: %+v %v", override, err)
	}
	if _, err = runtime.Patch(ctx, user, map[string]any{"worker_pools": []map[string]any{{"profile_id": a.ID, "workers": 30}, {"profile_id": b.ID, "workers": 21}}}); !errors.Is(err, settings.ErrInvalidProfile) {
		t.Fatalf("oversized worker pools accepted: %v", err)
	}
	store := airepo.NewTaskStore(orm)
	facade := aiapp.NewTaskFacade(store, nil, nil)
	first, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: user, Name: "不限时任务 A", Goal: "验证默认预算"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: user, Name: "不限时任务 B", Goal: "验证全局并发"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(context.Background(), `DELETE FROM ai_task WHERE id IN ($1,$2)`, first.ID, second.ID)
	if first.WorkerCount != 0 || first.MaxTurns != 0 || first.TimeoutSeconds != 0 || first.PlanHeartbeatSeconds != 600 {
		t.Fatalf("ARTEX task defaults: %+v", first)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_task SET queued=TRUE,created_at='2000-01-01' WHERE id IN ($1,$2)`, first.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan *aiapp.Task, 2)
	failures := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			item, err := store.ClaimTask(ctx, uuid.NewString())
			results <- item
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
	var claimed *aiapp.Task
	count := 0
	for item := range results {
		if item != nil {
			count++
			claimed = item
		}
	}
	if count != 1 {
		t.Fatalf("distributed task concurrency: %d claims", count)
	}
	started, err := store.StampTaskStart(ctx, claimed)
	if err != nil || started.DeadlineAt != nil || started.FirstRunAt == nil {
		t.Fatalf("unlimited task received a deadline: %+v %v", started, err)
	}
	if err = store.FinishTask(ctx, started, "paused", ""); err != nil {
		t.Fatal(err)
	}
	other, err := store.ClaimTask(ctx, uuid.NewString())
	if err != nil || other == nil || other.ID == started.ID {
		t.Fatalf("queued task did not start after slot released: %+v %v", other, err)
	}
	if err = store.FinishTask(ctx, other, "paused", ""); err != nil {
		t.Fatal(err)
	}
}

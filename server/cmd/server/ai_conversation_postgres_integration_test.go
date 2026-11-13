package main

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyAIConversationPostgresContract(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var owner, other int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO auth_user(username,password) VALUES('ai-other','unused') RETURNING id`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := airepo.NewConversationStore(orm)
	conversation, run, instance := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := store.CreateConversation(ctx, owner, conversation, "测试会话"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListActivities(ctx, other, conversation, 0); !errors.Is(err, aiapp.ErrConversationNotFound) {
		t.Fatalf("another user accessed the conversation: %v", err)
	}
	if err := store.BeginRun(ctx, owner, conversation, run, instance, "第一条消息"); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginRun(ctx, owner, conversation, uuid.NewString(), instance, "重复消息"); !errors.Is(err, aiapp.ErrConversationBusy) {
		t.Fatalf("concurrent run was accepted: %v", err)
	}
	if err := store.AppendActivity(ctx, owner, conversation, run, instance, airuntime.Activity{Kind: "text", Detail: "执行中"}); err != nil {
		t.Fatal(err)
	}
	valid, err := store.Heartbeat(ctx, owner, conversation, run, instance)
	if err != nil || !valid {
		t.Fatalf("heartbeat: valid=%v, err=%v", valid, err)
	}
	page, err := store.ListActivities(ctx, owner, conversation, 0)
	if err != nil || !page.Running || len(page.Items) != 2 || page.Items[0].Kind != "user" || page.Items[1].Detail != "执行中" {
		t.Fatalf("activity page %+v, err %v", page, err)
	}
	if _, err := store.StopRun(ctx, owner, conversation); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendActivity(ctx, owner, conversation, run, instance, airuntime.Activity{Kind: "result", Detail: "late"}); !errors.Is(err, aiapp.ErrRunLeaseLost) {
		t.Fatalf("late write after stop was accepted: %v", err)
	}
	page, err = store.ListActivities(ctx, owner, conversation, page.Items[0].ID)
	if err != nil || !page.Running || page.RunStatus != "stopping" {
		t.Fatalf("stop must await executor acknowledgement: %+v %v", page, err)
	}
	if err := store.BeginRun(ctx, owner, conversation, uuid.NewString(), instance, "too early"); !errors.Is(err, aiapp.ErrConversationBusy) {
		t.Fatalf("new run overlapped stopping executor: %v", err)
	}
	if err := store.FinishRunOutcome(ctx, owner, conversation, run, instance, "completed", "completed", ""); err != nil {
		t.Fatal(err)
	}
	page, err = store.ListActivities(ctx, owner, conversation, page.Items[0].ID-1)
	if err != nil || page.Running || page.RunStatus != "stopped" || len(page.Items) != 1 {
		t.Fatalf("stopped page %+v, err %v", page, err)
	}
	newRun := uuid.NewString()
	if err := store.BeginRun(ctx, owner, conversation, newRun, instance, "继续"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRun(ctx, owner, conversation, newRun, instance, "completed"); err != nil {
		t.Fatal(err)
	}
}

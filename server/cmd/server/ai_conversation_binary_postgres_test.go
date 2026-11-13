package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/sidequestion"
	"github.com/Autumn-27/norma/llm"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aires "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyBinaryConversationStoragePostgres(t *testing.T, ctx context.Context, database *sql.DB) {
	t.Helper()
	var user int
	if err := database.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := aires.NewConversationStore(orm)
	conversation, err := store.CreateAgentConversation(ctx, user, uuid.NewString(), "binary storage fixture", "auto")
	if err != nil {
		t.Fatal(err)
	}
	instance, run := uuid.NewString(), uuid.NewString()
	if err = store.BeginRun(ctx, user, conversation.ID, run, instance, "fixture"); err != nil {
		t.Fatal(err)
	}
	if err = store.AppendActivity(ctx, user, conversation.ID, run, instance, airuntime.Activity{Kind: "tool_result", Tool: "probe", ToolUseID: "fixture", Summary: "before\x00after", Detail: "before\x00after\xff"}); err != nil {
		t.Fatalf("binary tool output aborted activity persistence: %v", err)
	}
	page, err := store.ListActivities(ctx, user, conversation.ID, 0)
	if err != nil || !page.Running || len(page.Items) != 2 || page.Items[1].Detail != "before\\x00after\uFFFD" {
		t.Fatal("binary activity was lost or run stopped")
	}
	parent := sidequestion.Parent{ConversationKey: conversation.ID}
	snapshot := sidequestion.Snapshot{Parent: parent, RunID: time.Now().UnixNano(), Version: 2, CapturedAt: time.Now().UTC(), Model: sidequestion.Model{Model: "fixture", WindowTokens: 200000}, Request: llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockThinking, Thinking: "thought\x00complete"}, {Type: llm.BlockToolUse, Name: "probe", ID: "fixture", Input: json.RawMessage(`{"value":"a\u0000b"}`)}}}, {Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.BlockToolResult, ToolUseID: "fixture", Content: []llm.ContentBlock{llm.TextBlock("before\x00after")}}}}}}}
	if err = store.SaveSideSnapshot(ctx, user, parent, run, snapshot); err != nil {
		t.Fatalf("binary snapshot rejected by JSONB: %v", err)
	}
	older := snapshot
	older.Version = 1
	older.Request = llm.CompletionRequest{}
	if err = store.SaveSideSnapshot(ctx, user, parent, run, older); err != nil {
		t.Fatal(err)
	}
	history, err := store.ReadSideHistory(ctx, user, parent, 0)
	if err != nil || history.Snapshot == nil || !history.Snapshot.Available {
		t.Fatal("encoded snapshot unreadable")
	}
	admission, err := store.AdmitSideQuestion(ctx, user, parent, "fixture question\x00end", "fixture-binary", instance)
	if err != nil {
		t.Fatal(err)
	}
	if admission.Snapshot.Version != 2 || admission.ParentStatus == nil || admission.ParentStatus.Status != "running" || admission.Snapshot.Request.Messages[0].Content[0].Thinking != "thought\x00complete" || string(admission.Snapshot.Request.Messages[0].Content[1].Input) != `{"value":"a\u0000b"}` {
		t.Fatal("snapshot or authoritative run state was changed")
	}
	item := admission.Exchange
	item.Answer = "answer\x00end"
	item.Status = "completed"
	item.Sequence++
	if ok, err := store.UpdateSideExchange(ctx, user, instance, admission.SessionID, item.ID, item); err != nil || !ok {
		t.Fatalf("binary answer failed to save: %v", err)
	}
	read, err := store.ReadSideExchange(ctx, user, item.ID)
	if err != nil || read.Answer != item.Answer || read.Question != "fixture question\x00end" {
		t.Fatal("binary exchange did not round trip")
	}
	if err = store.FinishRun(ctx, user, conversation.ID, run, instance, "failed"); err != nil {
		t.Fatal(err)
	}
	next, err := store.AdmitSideQuestion(ctx, user, parent, "why stopped", "fixture-stopped", instance)
	if err != nil || next.ParentStatus == nil || next.ParentStatus.Status != "failed" || next.ParentStatus.FinishedAt == nil || !strings.Contains(next.ParentStatus.ErrorMessage, "失败") {
		t.Fatal("side question missed the failed main run")
	}
	if err = store.CancelSideQuestion(ctx, user, next.Exchange.ID); err != nil {
		t.Fatal(err)
	}
}

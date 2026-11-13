package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Autumn-27/artex/sidequestion"
	"github.com/Autumn-27/norma/llm"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	settingsinfra "github.com/yyhuni/lunafox/server/internal/modules/aisettings/infrastructure"
	settingsrepo "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	aiinfra "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/infrastructure"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	assetrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func verifyARTEXChatParityPostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	t.Setenv("TIANQIONG_AI_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("c", 32))))
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
	store := airepo.NewConversationStore(orm)
	root := t.TempDir()
	files := aiinfra.NewChatFileStorage(root)
	attachments := aiapp.NewAttachmentFacade(store, files)
	var sideCalls atomic.Int32
	var sideMentionSeen atomic.Bool
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		raw, _ := json.Marshal(request)
		answer := "MAIN"
		if strings.Contains(string(raw), "独立的旁路提问") {
			sideCalls.Add(1)
			if strings.Contains(string(raw), "mention-side.example") {
				sideMentionSeen.Store(true)
			}
			answer = "SIDE"
			if strings.Contains(string(raw), "wait-for-clear") {
				<-r.Context().Done()
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"` + answer + `"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer fixture.Close()
	profileStore := settingsrepo.NewLLMProfileStore(orm)
	profileFacade := settings.NewLLMProfileFacade(profileStore, airuntime.SecretBox{})
	resolver := settingsinfra.NewLLMResolver(profileStore, airuntime.SecretBox{})
	resolver.SetUsageSink(settingsrepo.NewLLMUsageStore(orm))
	stream := false
	options := settings.DefaultLLMOptions()
	options.Streaming = &stream
	profile, err := profileFacade.Save(ctx, user, "", settings.ProfileInput{Name: "chat migration fixture", Provider: "openai", Model: "chat-fixture", APIKey: "fixture-chat-key", BaseURL: fixture.URL, Options: &options})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = profileFacade.Delete(context.Background(), user, profile.ID) }()
	provider, model, _, err := resolver.ResolveProfile(ctx, user, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = model
	_, _, sideAsset := taskFixtureScope(t, ctx, db, "mention-side.example")
	mentionFacade := aiapp.NewChatMentionFacade(airepo.NewChatMentionStore(orm), assetapp.NewFacade(assetrepo.NewAssetRegistryStore(orm)))
	side := aiapp.NewSideQuestionFacade(store, resolver.ResolveSideModel)
	side.SetChatMentions(mentionFacade)
	runner, err := airuntime.NewChatRunner(root)
	if err != nil {
		t.Fatal(err)
	}
	facade := aiapp.NewConversationFacade(store, runner, func(context.Context, int) (llm.Provider, string, bool, error) {
		return provider, "chat-fixture", true, nil
	}, nil)
	facade.SetAttachments(attachments)
	facade.SetChatMentions(mentionFacade)
	facade.SetSideQuestions(side)
	runCtx, cancel := context.WithCancel(ctx)
	facade.Start(runCtx)
	defer func() { cancel(); <-facade.Done() }()
	conversation, err := facade.CreateWithAgent(ctx, user, "", "auto")
	if err != nil {
		t.Fatal(err)
	}
	parent := sidequestion.Parent{ConversationKey: conversation.ID}
	if _, err = side.Ask(ctx, user, parent, "too early", "before-snapshot"); !errors.Is(err, aiapp.ErrSnapshotUnavailable) {
		t.Fatalf("missing checkpoint: %v", err)
	}
	file, err := attachments.Upload(ctx, user, "session", "conv-"+conversation.ID, "report.txt", strings.NewReader("fixture contents"))
	if err != nil || file.Size != 16 {
		t.Fatalf("attachment: %+v %v", file, err)
	}
	if _, err = attachments.Upload(ctx, other, "session", conversation.ID, "forbidden.txt", strings.NewReader("x")); !errors.Is(err, aiapp.ErrConversationNotFound) {
		t.Fatalf("cross-user upload: %v", err)
	}
	if _, _, err = attachments.Compose(ctx, user, "session", conversation.ID, "", []aiapp.ChatAttachment{{Path: "uploads/../../escape"}}); !errors.Is(err, aiapp.ErrInvalidInput) {
		t.Fatalf("forged attachment accepted: %v", err)
	}
	injectedName, err := attachments.Upload(ctx, user, "session", "conv-"+conversation.ID, "@[漏洞#9223372036854775807 missing].txt", strings.NewReader("fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = facade.SendWithAttachments(ctx, user, conversation.ID, "read the report", []aiapp.ChatAttachment{{Path: file.Path, Name: "forged name", Size: 999}, {Path: injectedName.Path}}); err != nil {
		t.Fatal(err)
	}
	await := func(check func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if check() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("AI operation did not finish")
	}
	await(func() bool {
		page, err := facade.Activities(ctx, user, conversation.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		return !page.Running
	})
	page, err := facade.History(ctx, user, conversation.ID, 0, 200)
	if err != nil || len(page.Items) == 0 || page.Items[0].Kind != "user" {
		t.Fatalf("history: %+v %v", page, err)
	}
	detail, err := facade.ActivityDetail(ctx, user, conversation.ID, page.Items[0].ID)
	if err != nil || !strings.Contains(detail, "report.txt") || strings.Contains(detail, "forged name") {
		t.Fatalf("attachment transcript: %s %v", detail, err)
	}
	if _, err = facade.ActivityDetail(ctx, other, conversation.ID, page.Items[0].ID); !errors.Is(err, aiapp.ErrConversationNotFound) {
		t.Fatalf("cross-owner transcript: %v", err)
	}
	history, err := side.History(ctx, user, parent, 0)
	if err != nil || history.Snapshot == nil || !history.Snapshot.Available {
		t.Fatalf("UUID checkpoint: %+v %v", history, err)
	}
	sideQuestion := fmt.Sprintf("what has been done? @[服务#%d fixture]", sideAsset)
	exchange, err := side.Ask(ctx, user, parent, sideQuestion, "side-fixture-1")
	if err != nil {
		t.Fatal(err)
	}
	await(func() bool {
		result, err := side.Get(ctx, user, exchange.ID)
		if err != nil {
			t.Fatal(err)
		}
		if result.Running() {
			return false
		}
		if result.Status != "completed" || result.Answer != "SIDE" {
			t.Fatalf("native side answer: %+v", result)
		}
		return true
	})
	if !sideMentionSeen.Load() {
		t.Fatal("side question did not receive reference snapshot")
	}
	var sideUsage int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_llm_usage WHERE owner_user_id=$1 AND conversation_id=$2::uuid AND worker='sidequestion' AND input_tokens=1 AND output_tokens=1`, user, conversation.ID).Scan(&sideUsage); err != nil || sideUsage != 1 {
		t.Fatalf("side question usage: %d %v", sideUsage, err)
	}
	duplicate, err := side.Ask(ctx, user, parent, sideQuestion, "side-fixture-1")
	if err != nil || duplicate.ID != exchange.ID || sideCalls.Load() != 1 {
		t.Fatalf("side request replayed: %+v %v", duplicate, err)
	}
	tail, err := facade.History(ctx, user, conversation.ID, 0, 200)
	if err != nil || len(tail.Items) != len(page.Items) {
		t.Fatal("side question changed main transcript")
	}
	if _, err = side.Get(ctx, other, exchange.ID); !errors.Is(err, aiapp.ErrConversationNotFound) {
		t.Fatalf("cross-owner side history: %v", err)
	}
	pending, err := side.Ask(ctx, user, parent, "wait-for-clear", "side-fixture-clear")
	if err != nil {
		t.Fatal(err)
	}
	if err = side.Clear(ctx, user, parent); err != nil {
		t.Fatal(err)
	}
	if _, err = side.Get(ctx, user, pending.ID); !errors.Is(err, aiapp.ErrConversationNotFound) {
		t.Fatalf("clear retained exchange: %v", err)
	}
	instance, run := uuid.NewString(), uuid.NewString()
	if err = store.BeginRun(ctx, user, conversation.ID, run, instance, "pagination"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_conversation_activity(tenant_id,conversation_id,owner_user_id,run_id,kind,summary,detail) SELECT '00000000-0000-0000-0000-000000000001',$1,$2,$3,'text','page-'||n,'detail-'||n FROM generate_series(1,205) n`, conversation.ID, user, run); err != nil {
		t.Fatal(err)
	}
	if err = store.FinishRun(ctx, user, conversation.ID, run, instance, "completed"); err != nil {
		t.Fatal(err)
	}
	latest, err := facade.History(ctx, user, conversation.ID, 0, 200)
	if err != nil || len(latest.Items) != 200 || !latest.HasMore {
		t.Fatalf("history pagination: %+v %v", latest, err)
	}
	older, err := facade.History(ctx, user, conversation.ID, latest.Items[0].ID, 200)
	if err != nil || len(older.Items) == 0 || older.Items[len(older.Items)-1].ID >= latest.Items[0].ID {
		t.Fatalf("history ordering: %+v %v", older, err)
	}
	symlink := filepath.Join(filepath.Dir(file.Abs), "outside")
	if err = os.Symlink("/etc/passwd", symlink); err != nil {
		t.Fatal(err)
	}
	if _, err = files.Absolute(user, "session", conversation.ID, "uploads/outside"); err == nil {
		t.Fatal("attachment symlink escaped workspace")
	}
	result, err := facade.DeleteConversations(ctx, user, []string{conversation.ID})
	if err != nil || len(result) != 1 || !result[0].OK {
		t.Fatalf("delete chat: %+v %v", result, err)
	}
	if _, err = os.Stat(file.Abs); !os.IsNotExist(err) {
		t.Fatalf("attachment directory retained: %v", err)
	}
}

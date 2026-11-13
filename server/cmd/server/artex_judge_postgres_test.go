package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	approval "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/application"
	approvalrepo "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/repository"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	settingsinfra "github.com/yyhuni/lunafox/server/internal/modules/aisettings/infrastructure"
	settingsrepo "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type judgeMainModel struct {
	mu    sync.Mutex
	calls int
}

func (p *judgeMainModel) Stream(_ context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	p.mu.Lock()
	index := p.calls
	p.calls++
	p.mu.Unlock()
	return func(yield func(llm.StreamEvent, error) bool) {
		raw, _ := json.Marshal(req)
		if index > 0 && !strings.Contains(string(raw), "executed") {
			yield(llm.StreamEvent{}, errors.New("judge-protected tool did not execute"))
			return
		}
		events := []llm.StreamEvent{{Type: llm.SEMessageStart}}
		if index == 0 {
			events = append(events, llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "chat-judge-tool", ToolName: "Bash"}, llm.StreamEvent{Type: llm.SEToolInputJSON, Text: `{"command":"python3 benign.py"}`}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"})
		} else {
			events = append(events, llm.StreamEvent{Type: llm.SETextDelta, Text: "完成"}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"})
		}
		events = append(events, llm.StreamEvent{Type: llm.SEMessageStop})
		for _, event := range events {
			if !yield(event, nil) {
				return
			}
		}
	}
}
func (p *judgeMainModel) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, req) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func verifyARTEXJudgePostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	t.Setenv("TIANQIONG_AI_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("j", 32))))
	var owner, other int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	svc := approval.NewService(approvalrepo.NewStore(orm))
	if err = svc.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	oldCfg, err := svc.JudgeConfig(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	oldTools, err := svc.EnabledTools(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = svc.SetEnabledTools(context.Background(), owner, oldTools) }()
	worker := filepath.Join(t.TempDir(), "ai-tool-worker")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", worker, "./cmd/ai-tool-worker")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build review worker: %v %s", err, out)
	}
	previous, had := os.LookupEnv("TIANQIONG_AI_TOOL_RUNNER")
	if err = os.Setenv("TIANQIONG_AI_TOOL_RUNNER", worker); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if had {
			_ = os.Setenv("TIANQIONG_AI_TOOL_RUNNER", previous)
		} else {
			_ = os.Unsetenv("TIANQIONG_AI_TOOL_RUNNER")
		}
	}()
	root := t.TempDir()
	workspace := filepath.Join(root, "00000000-0000-0000-0000-000000000001", fmt.Sprintf("user-%d", owner), "work")
	if err = os.MkdirAll(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(workspace, "benign.py"), []byte("print('judge-script-marker')\n"), 0600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "private.py")
	if err = os.WriteFile(secret, []byte("private-host-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	var invalid atomic.Bool
	var captured atomic.Value
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer judge-fixture-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		raw, _ := json.Marshal(body)
		captured.Store(string(raw))
		index := requests.Add(1)
		response := `{"decision":"allow","comment":"实际操作：读取脚本；成功后的后果：返回内容；命中规则：A5"}`
		if invalid.Load() || index == 1 {
			response = "invalid verdict"
		}
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": response}, "finish_reason": nil}}})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n", chunk)
	}))
	defer fixture.Close()
	profiles := settings.NewLLMProfileFacade(settingsrepo.NewLLMProfileStore(orm), airuntime.SecretBox{})
	profile, err := profiles.Save(ctx, owner, "", settings.ProfileInput{Name: "judge fixture profile", Provider: "openai", Model: "judge-fixture", BaseURL: fixture.URL, APIKey: "judge-fixture-key"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = profiles.Delete(context.Background(), owner, profile.ID) }()
	defer func() { _ = svc.SaveJudge(context.Background(), owner, oldCfg) }()
	resolver := settingsinfra.NewLLMResolver(settingsrepo.NewLLMProfileStore(orm), airuntime.SecretBox{})
	svc.SetJudgeDependencies(func(ctx context.Context, user int, name string, input json.RawMessage, tc *tool.ToolContext) (intercept.ReviewInput, error) {
		return airuntime.CaptureApprovalReview(ctx, root, user, name, input, tc)
	}, airuntime.NewApprovalReviewer(func(ctx context.Context, user int, id *string) (llm.Provider, error) {
		if id == nil {
			return nil, errors.New("missing fixture profile")
		}
		p, _, ok, err := resolver.ResolveProfileExclusive(ctx, user, *id)
		if err != nil || !ok {
			return nil, errors.New("profile unavailable")
		}
		return p, nil
	}))
	cfg := approval.DefaultJudgeConfig()
	cfg.Enabled = true
	cfg.ProfileID = &profile.ID
	cfg.RetryCount = 1
	cfg.RetryIntervalMS = 0
	cfg.IncompleteScriptAction = "ask"
	if err = svc.SaveJudge(ctx, owner, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.JudgeConfig(ctx, other); !errors.Is(err, approval.ErrForbidden) {
		t.Fatalf("non-admin read judge settings: %v", err)
	}
	if err = svc.SetEnabledTools(ctx, owner, []string{"Bash"}); err != nil {
		t.Fatal(err)
	}
	conversations := airepo.NewConversationStore(orm)
	conv, run, instance := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = conversations.CreateAgentConversation(ctx, owner, conv, "judge fixture", "auto"); err != nil {
		t.Fatal(err)
	}
	if err = conversations.BeginRun(ctx, owner, conv, run, instance, "test"); err != nil {
		t.Fatal(err)
	}
	defer conversations.StopRun(context.Background(), owner, conv)
	var executed atomic.Int32
	base := tool.Build(tool.Spec{Name: "Bash", Description: "fixture", Schema: map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}}, Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
		return permission.Allowed()
	}, Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
		executed.Add(1)
		return tool.Text("executed"), nil
	}})
	call := approval.Call{UserID: owner, RunID: run, ConversationID: conv, AgentName: "auto"}
	reviewCtx := intercept.WithReviewContext(ctx, workspace, intercept.ReviewBackground{Source: intercept.BackgroundUserMessage, Text: "trusted test instruction"})
	tc := &tool.ToolContext{WorkingDir: workspace, ToolUseID: "judge-allow"}
	result, err := svc.Wrap(base, call, nil).Call(reviewCtx, json.RawMessage(`{"command":"python3 benign.py"}`), tc)
	if err != nil || result.IsError || executed.Load() != 1 || requests.Load() != 2 {
		t.Fatalf("judge retry and allow: %s calls=%d requests=%d err=%v", result.Flatten(), executed.Load(), requests.Load(), err)
	}
	if value, _ := captured.Load().(string); !strings.Contains(value, "judge-script-marker") || !strings.Contains(value, "trusted test instruction") || strings.Contains(value, "private-host-secret") {
		t.Fatalf("judge input provenance: %s", value)
	}
	records, total, err := svc.List(ctx, owner, approval.Filter{Source: "model", Page: 1, Size: 20})
	if err != nil || total != 1 || len(records) != 1 || records[0].Status != "allowed" {
		t.Fatalf("judge records: %+v %d %v", records, total, err)
	}
	detail, err := svc.Get(ctx, owner, records[0].ID)
	if err != nil || !strings.Contains(string(detail.Audit), "judge-script-marker") {
		t.Fatalf("judge audit: %+v %v", detail, err)
	}
	usage, err := svc.JudgeUsage(ctx, owner, 30)
	if err != nil || usage.Calls < 2 || len(usage.Daily) == 0 {
		t.Fatalf("judge usage: %+v %v", usage, err)
	}
	outsideInput, _ := json.Marshal(map[string]any{"command": "python3 " + secret})
	done := make(chan tool.Result, 1)
	go func() {
		res, _ := svc.Wrap(base, call, nil).Call(reviewCtx, outsideInput, &tool.ToolContext{WorkingDir: workspace, ToolUseID: "judge-outside"})
		done <- res
	}()
	var pending approval.Approval
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		items, _, e := svc.List(ctx, owner, approval.Filter{Status: "pending", Source: "model", Page: 1, Size: 20})
		if e != nil {
			t.Fatal(e)
		}
		if len(items) > 0 {
			pending = items[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pending.ID == 0 {
		t.Fatal("outside script did not request approval")
	}
	pendingDetail, err := svc.Get(ctx, owner, pending.ID)
	if err != nil || strings.Contains(string(pendingDetail.Audit), "private-host-secret") {
		t.Fatalf("outside script was not isolated: %+v %v", pendingDetail, err)
	}
	if err = svc.Decide(ctx, owner, pending.ID, "denied"); err != nil {
		t.Fatal(err)
	}
	select {
	case res := <-done:
		if !res.IsError {
			t.Fatal("denied incomplete script executed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("judge approval did not unblock")
	}
	invalid.Store(true)
	cfg.FailAction = "deny"
	if err = svc.SaveJudge(ctx, owner, cfg); err != nil {
		t.Fatal(err)
	}
	result, err = svc.Wrap(base, call, nil).Call(reviewCtx, json.RawMessage(`{"command":"echo harmless"}`), &tool.ToolContext{WorkingDir: workspace, ToolUseID: "judge-fallback"})
	if err != nil || !result.IsError || executed.Load() != 1 {
		t.Fatalf("judge failure policy: %s count=%d %v", result.Flatten(), executed.Load(), err)
	}
	invalid.Store(false)
	cfg.FailAction = "ask"
	if err = svc.SaveJudge(ctx, owner, cfg); err != nil {
		t.Fatal(err)
	}
	chatRunner, err := airuntime.NewChatRunner(root)
	if err != nil {
		t.Fatal(err)
	}
	mainModel := &judgeMainModel{}
	chat := aiapp.NewConversationFacade(conversations, chatRunner, func(context.Context, int) (llm.Provider, string, bool, error) {
		return mainModel, "fixture-main", true, nil
	}, nil)
	chat.SetAutoTools(func(int) []tool.CoreTool { return []tool.CoreTool{base} })
	chat.SetApprovalWrapper(func(ctx context.Context, user int, key string, info agent.RunInfo, emit func(airuntime.Activity), item tool.CoreTool) tool.CoreTool {
		return svc.Wrap(item, approval.Call{UserID: user, RunID: airuntime.ExecutionID(ctx), ConversationID: info.SessionID, TaskID: info.TaskID, IntentID: info.IntentID, AgentName: key}, func(kind, summary, detail string) {
			if emit != nil {
				emit(airuntime.Activity{Kind: kind, Summary: summary, Detail: detail})
			}
		})
	})
	runCtx, cancelChat := context.WithCancel(ctx)
	chat.Start(runCtx)
	defer func() { cancelChat(); <-chat.Done() }()
	session, err := chat.CreateWithAgent(ctx, owner, "judge chat", "auto")
	if err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(workspace, "sessions", session.ID)
	if err = os.MkdirAll(sessionDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(sessionDir, "benign.py"), []byte("print('chat-script-marker')\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = chat.Send(ctx, owner, session.ID, "explicit-human-background"); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(8 * time.Second)
	complete := false
	for time.Now().Before(deadline) {
		page, e := chat.Activities(ctx, owner, session.ID, 0)
		if e != nil {
			t.Fatal(e)
		}
		if !page.Running {
			if page.RunStatus != "completed" {
				raw, _ := json.Marshal(page)
				t.Fatalf("judge chat: %s", raw)
			}
			complete = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !complete {
		t.Fatal("judge chat did not complete")
	}
	if value, _ := captured.Load().(string); !strings.Contains(value, "chat-script-marker") || !strings.Contains(value, "explicit-human-background") {
		t.Fatalf("chat judge background and script missing: %s", value)
	}
}

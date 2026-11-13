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
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/auth"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	approval "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/application"
	approvalhandler "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/handler"
	approvalrepo "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/repository"
	approvalrouter "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/router"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type approvalModelFixture struct {
	mu    sync.Mutex
	calls int
}

func (p *approvalModelFixture) Stream(_ context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	p.mu.Lock()
	index := p.calls
	p.calls++
	p.mu.Unlock()
	return func(yield func(llm.StreamEvent, error) bool) {
		raw, _ := json.Marshal(req)
		if index == 1 && !strings.Contains(string(raw), "ask_probe executed") {
			yield(llm.StreamEvent{}, errors.New("approved tool did not execute"))
			return
		}
		if index == 2 && !strings.Contains(string(raw), "平台管控") {
			yield(llm.StreamEvent{}, errors.New("denied tool did not return block"))
			return
		}
		if index == 3 && !strings.Contains(string(raw), "allow_probe executed") {
			yield(llm.StreamEvent{}, errors.New("allowed tool did not execute"))
			return
		}
		events := []llm.StreamEvent{{Type: llm.SEMessageStart}}
		names := []string{"ask_probe", "deny_probe", "allow_probe"}
		if index < len(names) {
			events = append(events, llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: fmt.Sprintf("approval-use-%d", index), ToolName: names[index]}, llm.StreamEvent{Type: llm.SEToolInputJSON, Text: `{"value":"fixture"}`}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"})
		} else {
			events = append(events, llm.StreamEvent{Type: llm.SETextDelta, Text: "审批流程完成"}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"})
		}
		events = append(events, llm.StreamEvent{Type: llm.SEMessageStop})
		for _, event := range events {
			if !yield(event, nil) {
				return
			}
		}
	}
}
func (p *approvalModelFixture) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, req) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func verifyARTEXApprovalPostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
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
	rules, err := svc.Rules(ctx, owner)
	if err != nil || len(rules) != 20 {
		t.Fatalf("native approval defaults: %d %v", len(rules), err)
	}
	if _, err = svc.Rules(ctx, other); !errors.Is(err, approval.ErrForbidden) {
		t.Fatalf("non-admin edited approval rules: %v", err)
	}
	removed := rules[len(rules)-1]
	if err = svc.DeleteRule(ctx, owner, removed.ID); err != nil {
		t.Fatal(err)
	}
	if err = svc.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	rules, err = svc.Rules(ctx, owner)
	if err != nil || len(rules) != 19 {
		t.Fatalf("deleted default was reseeded: %d %v", len(rules), err)
	}
	removed.ID = 0
	if _, err = svc.SaveRule(ctx, owner, removed, true); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.SaveRule(ctx, owner, approval.Rule{Name: "bad regex", Enabled: true, MatchTarget: "tool_input", MatchType: "regex", Pattern: "[", Action: "deny", TimeoutAction: "deny", TimeoutSeconds: 60}, true); !errors.Is(err, approval.ErrInvalidInput) {
		t.Fatalf("invalid regex accepted: %v", err)
	}
	originalTools, err := svc.EnabledTools(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = svc.SetEnabledTools(context.Background(), owner, originalTools) }()
	if err = svc.SetEnabledTools(ctx, owner, []string{"ask_probe", "deny_probe", "allow_probe"}); err != nil {
		t.Fatal(err)
	}
	var created []approval.Rule
	for _, v := range []struct{ name, action string }{{"ask_probe", "ask"}, {"deny_probe", "deny"}, {"allow_probe", "allow"}} {
		item, err := svc.SaveRule(ctx, owner, approval.Rule{Name: "fixture " + v.name, Enabled: true, Priority: 1000, MatchTarget: "tool_name", MatchType: "string", Pattern: v.name, Action: v.action, Message: "fixture policy", TimeoutSeconds: 60, TimeoutAction: "deny"}, true)
		if err != nil {
			t.Fatal(err)
		}
		created = append(created, item)
	}
	defer func() {
		for _, item := range created {
			_ = svc.DeleteRule(context.Background(), owner, item.ID)
		}
	}()
	manualConv, manualRun, manualInstance := uuid.NewString(), uuid.NewString(), uuid.NewString()
	manualStore := airepo.NewConversationStore(orm)
	if _, err = manualStore.CreateAgentConversation(ctx, owner, manualConv, "approval manual", "auto"); err != nil {
		t.Fatal(err)
	}
	if err = manualStore.BeginRun(ctx, owner, manualConv, manualRun, manualInstance, "test"); err != nil {
		t.Fatal(err)
	}
	direct, err := approvalrepo.NewStore(orm).CreateApproval(ctx, approval.Call{UserID: owner, RunID: manualRun, ConversationID: manualConv, AgentName: "auto", ToolName: "ask_probe", ToolUseID: "manual", Input: json.RawMessage(`{"value":"fixture"}`)}, created[0], "ask")
	if err != nil {
		t.Fatalf("direct approval create: %v", err)
	}
	if _, err = manualStore.StopRun(ctx, owner, manualConv); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM ai_approval WHERE id=$1`, direct.ID); err != nil {
		t.Fatal(err)
	}
	var askCount, denyCount, allowCount atomic.Int32
	build := func(name string, count *atomic.Int32) tool.CoreTool {
		return tool.Build(tool.Spec{Name: name, Description: "fixture", Schema: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}}, Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		}, Run: func(_ context.Context, _ json.RawMessage, _ *tool.ToolContext) (tool.Result, error) {
			count.Add(1)
			return tool.Text(name + " executed"), nil
		}})
	}
	runner, err := airuntime.NewChatRunner(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	provider := &approvalModelFixture{}
	conversations := airepo.NewConversationStore(orm)
	chat := aiapp.NewConversationFacade(conversations, runner, func(context.Context, int) (llm.Provider, string, bool, error) { return provider, "fixture", true, nil }, nil)
	chat.SetAutoTools(func(int) []tool.CoreTool {
		return []tool.CoreTool{build("ask_probe", &askCount), build("deny_probe", &denyCount), build("allow_probe", &allowCount)}
	})
	chat.SetApprovalWrapper(func(ctx context.Context, user int, key string, info agent.RunInfo, emit func(airuntime.Activity), item tool.CoreTool) tool.CoreTool {
		return svc.Wrap(item, approval.Call{UserID: user, RunID: airuntime.ExecutionID(ctx), ConversationID: info.SessionID, TaskID: info.TaskID, IntentID: info.IntentID, AgentName: key}, func(kind, summary, detail string) {
			if emit != nil {
				emit(airuntime.Activity{Kind: kind, Summary: summary, Detail: detail})
			}
		})
	})
	runCtx, cancel := context.WithCancel(ctx)
	chat.Start(runCtx)
	defer func() { cancel(); <-chat.Done() }()
	conversation, err := chat.CreateWithAgent(ctx, owner, "approval fixture", "auto")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = chat.Send(ctx, owner, conversation.ID, "test approval"); err != nil {
		t.Fatal(err)
	}
	var pending approval.Approval
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		items, _, e := svc.List(ctx, owner, approval.Filter{Status: "pending", Page: 1, Size: 20})
		if e != nil {
			t.Fatal(e)
		}
		if len(items) > 0 {
			pending = items[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pending.ID == 0 || askCount.Load() != 0 {
		page, _ := chat.Activities(ctx, owner, conversation.ID, 0)
		raw, _ := json.Marshal(page)
		t.Fatalf("approval did not wait: %+v count=%d activities=%s", pending, askCount.Load(), raw)
	}
	execution, err := svc.Execution(ctx, owner, pending.ID)
	if err != nil || execution.ConversationID == nil || *execution.ConversationID != conversation.ID || execution.Seq <= 0 || len(execution.Items) == 0 {
		t.Fatalf("approval execution: %+v %v", execution, err)
	}
	if _, err = svc.Get(ctx, other, pending.ID); !errors.Is(err, approval.ErrNotFound) {
		t.Fatalf("foreign approval read: %v", err)
	}
	manager := auth.NewJWTManager("fixture-jwt-secret", time.Minute, time.Hour)
	token, _, _ := manager.GenerateAccessToken(owner, "registry-admin", 0)
	otherToken, _, _ := manager.GenerateAccessToken(other, "ai-other", 0)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/v1")
	group.Use(middleware.AuthMiddleware(manager, fixtureTokenVersions{}))
	approvalrouter.Register(group, approvalhandler.NewHandler(svc))
	send := func(method, path, token, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		return response
	}
	if r := send(http.MethodGet, "/v1/aiInterceptRules", otherToken, ""); r.Code != http.StatusForbidden {
		t.Fatalf("non-admin rules HTTP: %d %s", r.Code, r.Body.String())
	}
	if r := send(http.MethodGet, "/v1/aiInterceptJudge", token, ""); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"retry_count":3`) {
		t.Fatalf("judge settings HTTP: %d %s", r.Code, r.Body.String())
	}
	if r := send(http.MethodGet, "/v1/aiApprovals/pending", token, ""); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"tool_name":"ask_probe"`) {
		t.Fatalf("pending approvals HTTP: %d %s", r.Code, r.Body.String())
	}
	path := fmt.Sprintf("/v1/aiApprovals/%d", pending.ID)
	if r := send(http.MethodGet, path, "", ""); r.Code != http.StatusUnauthorized {
		t.Fatalf("approval without auth: %d", r.Code)
	}
	if r := send(http.MethodGet, path+"/execution", token, ""); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"seq":`) {
		t.Fatalf("approval execution HTTP: %d %s", r.Code, r.Body.String())
	}
	if r := send(http.MethodGet, path, otherToken, ""); r.Code != http.StatusNotFound {
		t.Fatalf("foreign approval HTTP: %d %s", r.Code, r.Body.String())
	}
	if r := send(http.MethodPost, path+"/decision", otherToken, `{"decision":"allowed"}`); r.Code != http.StatusConflict && r.Code != http.StatusForbidden {
		t.Fatalf("foreign decision: %d %s", r.Code, r.Body.String())
	}
	if r := send(http.MethodPost, path+"/decision", token, `{"decision":"allowed"}`); r.Code != http.StatusOK {
		t.Fatalf("approval decision HTTP: %d %s", r.Code, r.Body.String())
	}
	complete := false
	deadline = time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		page, e := chat.Activities(ctx, owner, conversation.ID, 0)
		if e != nil {
			t.Fatal(e)
		}
		if !page.Running {
			if page.RunStatus != "completed" {
				raw, _ := json.Marshal(page)
				t.Fatalf("approval conversation: %s", raw)
			}
			complete = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !complete {
		t.Fatal("approval conversation did not finish")
	}
	if askCount.Load() != 1 || denyCount.Load() != 0 || allowCount.Load() != 1 {
		t.Fatalf("approval outcomes ask=%d deny=%d allow=%d", askCount.Load(), denyCount.Load(), allowCount.Load())
	}
	saved, err := svc.Get(ctx, owner, pending.ID)
	if err != nil || saved.Status != "allowed" || saved.DecisionSource != "manual" || saved.ExecutionStatus != "succeeded" || !strings.Contains(saved.ExecutionOutput, "ask_probe executed") || !strings.Contains(string(saved.Audit), "test approval") {
		t.Fatalf("persisted approval and context: %+v %v", saved, err)
	}
	if err = svc.Decide(ctx, owner, pending.ID, "denied"); !errors.Is(err, approval.ErrAlreadyDecided) {
		t.Fatalf("approval decided twice: %v", err)
	}
	records, total, err := svc.List(ctx, owner, approval.Filter{Page: 1, Size: 20})
	if err != nil || total != 3 || len(records) != 3 {
		t.Fatalf("approval history: %+v %d %v", records, total, err)
	}
	exact, exactTotal, err := svc.List(ctx, owner, approval.Filter{Query: fmt.Sprintf("#%d", pending.ID), Page: 1, Size: 20})
	if err != nil || exactTotal != 1 || len(exact) != 1 || exact[0].ID != pending.ID {
		t.Fatalf("approval exact search: %+v %d %v", exact, exactTotal, err)
	}
	today := time.Now().UTC().Format("2006-01-02")
	dated, datedTotal, err := svc.List(ctx, owner, approval.Filter{From: today, To: today, Page: 1, Size: 20})
	if err != nil || datedTotal != 3 || len(dated) != 3 {
		t.Fatalf("approval UTC date filter: %+v %d %v", dated, datedTotal, err)
	}
	verifyApprovalTimeoutAndStop(t, ctx, db, svc, conversations, owner)
}

func verifyApprovalTimeoutAndStop(t *testing.T, ctx context.Context, db *sql.DB, svc *approval.Service, conversations *airepo.ConversationStore, owner int) {
	t.Helper()
	if err := svc.SetEnabledTools(ctx, owner, []string{"timeout_deny", "timeout_allow", "cancel_probe", "stopped_probe", "large_probe"}); err != nil {
		t.Fatal(err)
	}
	rules := []approval.Rule{}
	for _, v := range []struct {
		name    string
		timeout bool
		action  string
	}{{"timeout_deny", true, "deny"}, {"timeout_allow", true, "allow"}, {"cancel_probe", false, "deny"}, {"stopped_probe", false, "deny"}} {
		r, err := svc.SaveRule(ctx, owner, approval.Rule{Name: "fixture " + v.name, Enabled: true, Priority: 1000, MatchTarget: "tool_name", MatchType: "string", Pattern: v.name, Action: "ask", TimeoutEnabled: v.timeout, TimeoutSeconds: 1, TimeoutAction: v.action}, true)
		if err != nil {
			t.Fatal(err)
		}
		rules = append(rules, r)
	}
	largeRule, err := svc.SaveRule(ctx, owner, approval.Rule{Name: "fixture large_probe", Enabled: true, Priority: 1000, MatchTarget: "tool_name", MatchType: "string", Pattern: "large_probe", Action: "allow", TimeoutSeconds: 60, TimeoutAction: "deny"}, true)
	if err != nil {
		t.Fatal(err)
	}
	rules = append(rules, largeRule)
	defer func() {
		for _, r := range rules {
			_ = svc.DeleteRule(context.Background(), owner, r.ID)
		}
	}()
	conv, run, instance := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := conversations.CreateAgentConversation(ctx, owner, conv, "approval timeout fixture", "auto"); err != nil {
		t.Fatal(err)
	}
	if err := conversations.BeginRun(ctx, owner, conv, run, instance, "test"); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	base := func(name string) tool.CoreTool {
		return tool.Build(tool.Spec{Name: name, Description: "fixture", Schema: map[string]any{"type": "object", "properties": map[string]any{}}, Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		}, Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
			calls.Add(1)
			return tool.Text("executed"), nil
		}})
	}
	call := approval.Call{UserID: owner, RunID: run, ConversationID: conv, AgentName: "auto"}
	for _, v := range []struct {
		name    string
		allowed bool
	}{{"timeout_deny", false}, {"timeout_allow", true}} {
		result, err := svc.Wrap(base(v.name), call, nil).Call(ctx, json.RawMessage(`{}`), &tool.ToolContext{ToolUseID: v.name})
		if err != nil || result.IsError == v.allowed {
			t.Fatalf("timeout %s: %+v %v", v.name, result, err)
		}
		items, _, err := svc.List(ctx, owner, approval.Filter{Status: "timeout", Page: 1, Size: 100})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range items {
			if item.ToolName == v.name {
				found = true
				if (item.EffectiveAction == "allow") != v.allowed {
					t.Fatalf("timeout action: %+v", item)
				}
			}
		}
		if !found {
			t.Fatalf("timeout record missing: %s", v.name)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("timeout executed %d tools, expected one", calls.Load())
	}
	large := tool.Build(tool.Spec{Name: "large_probe", Description: "fixture", Schema: map[string]any{"type": "object", "properties": map[string]any{}}, Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
		return tool.Text(strings.Repeat("界", 30000)), nil
	}})
	result, err := svc.Wrap(large, call, nil).Call(ctx, json.RawMessage(`{}`), &tool.ToolContext{ToolUseID: "large-output"})
	if err != nil || result.IsError {
		t.Fatalf("large tool output: %v", err)
	}
	matched, _, err := svc.List(ctx, owner, approval.Filter{Query: "large_probe", Page: 1, Size: 20})
	if err != nil || len(matched) != 1 {
		t.Fatalf("large approval lookup: %+v %v", matched, err)
	}
	full, err := svc.Get(ctx, owner, matched[0].ID)
	if err != nil || len(full.ExecutionOutput) > 65536 || !utf8.ValidString(full.ExecutionOutput) || !strings.Contains(string(full.Audit), `"output_truncated": true`) {
		t.Fatalf("large approval audit: len=%d audit=%s err=%v", len(full.ExecutionOutput), full.Audit, err)
	}

	start := func(name string, runCtx context.Context) <-chan tool.Result {
		done := make(chan tool.Result, 1)
		go func() {
			result, _ := svc.Wrap(base(name), call, nil).Call(runCtx, json.RawMessage(`{}`), &tool.ToolContext{ToolUseID: name})
			done <- result
		}()
		return done
	}
	waitPending := func(name string) approval.Approval {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			items, _, err := svc.List(ctx, owner, approval.Filter{Status: "pending", Page: 1, Size: 100})
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range items {
				if item.ToolName == name {
					return item
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("pending %s not found", name)
		return approval.Approval{}
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	done := start("cancel_probe", cancelCtx)
	pending := waitPending("cancel_probe")
	cancel()
	select {
	case result := <-done:
		if !result.IsError {
			t.Fatal("cancelled approval executed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled approval remained blocked")
	}
	saved, err := svc.Get(ctx, owner, pending.ID)
	if err != nil || saved.Status != "denied" || saved.ExecutionStatus != "not_executed" {
		t.Fatalf("cancelled approval: %+v %v", saved, err)
	}
	done = start("stopped_probe", ctx)
	pending = waitPending("stopped_probe")
	if _, err = conversations.StopRun(ctx, owner, conv); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if !result.IsError {
			t.Fatal("stopped run executed tool")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stopped run approval remained blocked")
	}
	saved, err = svc.Get(ctx, owner, pending.ID)
	if err != nil || saved.Status != "denied" {
		t.Fatalf("stopped approval: %+v %v", saved, err)
	}
	if err = svc.Decide(ctx, owner, pending.ID, "allowed"); !errors.Is(err, approval.ErrAlreadyDecided) {
		t.Fatalf("stopped approval accepted decision: %v", err)
	}
}

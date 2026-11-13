package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"iter"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/sidequestion"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	approval "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/application"
	approvalrepo "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/repository"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	aiinfra "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/infrastructure"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	registryapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	registryrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type independentMainProvider struct {
	calls       atomic.Int32
	tool, input string
	requests    chan string
}

func (p *independentMainProvider) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		if p.requests != nil {
			var content strings.Builder
			for _, message := range req.Messages {
				content.WriteString(message.Text())
			}
			select {
			case p.requests <- content.String():
			default:
			}
		}
		if !yield(llm.StreamEvent{Type: llm.SEMessageStart}, nil) {
			return
		}
		if p.tool != "" && p.calls.Add(1) == 1 {
			found := false
			for _, item := range req.Tools {
				if item.Name == p.tool {
					found = true
				}
			}
			if !found {
				yield(llm.StreamEvent{}, fmt.Errorf("missing MainAgent tool %s", p.tool))
				return
			}
			for _, event := range []llm.StreamEvent{{Type: llm.SEToolUseStart, ToolID: "independent-tool", ToolName: p.tool}, {Type: llm.SEToolInputJSON, Text: p.input}, {Type: llm.SEMessageDelta, StopReason: "tool_use"}, {Type: llm.SEMessageStop}} {
				if !yield(event, nil) {
					return
				}
			}
			return
		}
		for _, event := range []llm.StreamEvent{{Type: llm.SETextDelta, Text: "独立对话完成"}, {Type: llm.SEMessageDelta, StopReason: "end_turn"}, {Type: llm.SEMessageStop}} {
			if !yield(event, nil) {
				return
			}
		}
	}
}
func (p *independentMainProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, req) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

type blockingMainProvider struct {
	started   chan struct{}
	cancelled chan struct{}
}

func (p *blockingMainProvider) Stream(ctx context.Context, _ llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		close(p.started)
		<-ctx.Done()
		close(p.cancelled)
		yield(llm.StreamEvent{}, ctx.Err())
	}
}
func (p *blockingMainProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	for _, err := range p.Stream(ctx, req) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
	}
	return llm.Message{}, "", llm.Usage{}, nil
}

func verifyARTEXIndependentMainChatPostgres(t *testing.T, ctx context.Context, db *sql.DB, dsn string) {
	t.Helper()
	owner, target, asset := taskFixtureScope(t, ctx, db, "independent-main.example")
	var other int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := airepo.NewTaskStore(orm)
	connector, err := airuntime.NewTaskGraphConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	var current llm.Provider
	resolve := func(context.Context, int) (llm.Provider, string, bool, error) { return current, "fixture", true, nil }
	var facade *aiapp.TaskFacade
	assetRead := registryapp.NewFacade(registryrepo.NewAssetRegistryStore(orm))
	assetWrite := registryapp.NewRegistrationFacade(registryrepo.NewAssetRegistrationStore(orm, assetRegistrationCreators(orm)), airuntime.SecretBox{})
	approvals := approval.NewService(approvalrepo.NewStore(orm))
	if err = approvals.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	assetWrite.SetAIAssetGate(func(ctx context.Context, user int, scope *registryapp.RegistrationTaskScope, _ string, inputs []registryapp.AssetInput) ([]string, error) {
		return approvals.CheckAssetInputs(ctx, user, scope.TaskID, scope.ExecutionID, inputs)
	})
	platformTools := airuntime.AssetToolResolver(assetRead, assetWrite)
	runner := airuntime.NewTaskRunner(connector, root, func(user int) []tool.CoreTool {
		return append(platformTools(user), aiinfra.TaskScopeTools(facade, user)...)
	})
	runner.SetAssetIDsGate(func(ctx context.Context, task airuntime.TaskExecution, ids []int64) error {
		return approvals.CheckAssetIDs(ctx, task.UserID, task.ID, task.RunID, ids, task.TargetID)
	})
	runner.SetApprovalWrapper(func(ctx context.Context, user int, key string, info agent.RunInfo, emit func(airuntime.Activity), item tool.CoreTool) tool.CoreTool {
		call := approval.Call{UserID: user, RunID: airuntime.ExecutionID(ctx), TaskID: info.TaskID, IntentID: info.IntentID, AgentName: key}
		return approvals.Wrap(item, call, func(kind, summary, detail string) {
			if emit != nil {
				emit(airuntime.Activity{Kind: kind, Summary: summary, Detail: detail})
			}
		})
	})
	runner.SetRoleProviderResolver(func(context.Context, int, string) (llm.Provider, string, bool, error) {
		return sidequestion.Bind(current, sidequestion.Model{Model: "fixture", Name: "fixture", Identity: "independent-main", Streaming: true, WindowTokens: 100000}), "fixture", true, nil
	})
	side := aiapp.NewSideQuestionFacade(airepo.NewConversationStore(orm), func(context.Context, int, sidequestion.Model) (llm.Provider, error) { return current, nil })
	side.Start(ctx)
	defer side.Shutdown()
	runner.SetSidePublisher(func(ctx context.Context, task airuntime.TaskExecution, snapshot sidequestion.Snapshot) {
		side.Publish(ctx, task.UserID, snapshot.Parent, task.RunID, snapshot)
	})
	controller := aiapp.NewTaskController(store, runner, resolve)
	facade = aiapp.NewTaskFacade(store, resolve, controller)
	task, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "独立主会话", TargetID: target, AssetIDs: []int64{asset}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ControlTask(ctx, owner, task.ID, "pause", false); err != nil {
		t.Fatal(err)
	}
	if _, err = facade.Chat(ctx, other, task.ID, "越权"); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign chat: %v", err)
	}
	current = &independentMainProvider{}
	if reply, err := facade.Chat(ctx, owner, task.ID, "查看状态"); err != nil || reply != "独立对话完成" {
		t.Fatalf("paused chat: %q %v", reply, err)
	}
	paused, err := store.GetTask(ctx, owner, task.ID)
	if err != nil || paused.Status != "paused" || paused.Queued {
		t.Fatalf("plain chat resumed scan: %+v %v", paused, err)
	}
	current = &independentMainProvider{tool: "set_goals", input: `{"text":"新目标必须持久唤醒"}`}
	if reply, err := facade.Chat(ctx, owner, task.ID, "增加目标"); err != nil || reply == "" {
		t.Fatalf("goal chat: %q %v", reply, err)
	}
	var goals, events, wakes int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.exploration_nodes WHERE exploration_id=$1 AND kind='goal' AND payload->>'text'='新目标必须持久唤醒'`, task.ExplorationID).Scan(&goals); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_task_human_event WHERE task_id=$1 AND kind='goal' AND payload->'goals' ? '新目标必须持久唤醒'`, task.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_task_main_chat_wake WHERE task_id=$1`, task.ID).Scan(&wakes); err != nil {
		t.Fatal(err)
	}
	if goals != 1 || events != 1 || wakes != 1 {
		t.Fatalf("goal transaction: goals=%d events=%d wakes=%d", goals, events, wakes)
	}
	if err = store.ProcessMainChatWakes(ctx); err != nil {
		t.Fatal(err)
	}
	revived, err := store.GetTask(ctx, owner, task.ID)
	if err != nil || revived.Status != "created" || !revived.Queued {
		t.Fatalf("durable goal wake: %+v %v", revived, err)
	}
	// Explicit pause supersedes an as-yet-unprocessed wake from the chat.
	if _, err = store.ControlTask(ctx, owner, task.ID, "pause", false); err != nil {
		t.Fatal(err)
	}
	current = &independentMainProvider{tool: "add_intent", input: `{"summary":"验证新增意图","priority":8}`}
	if _, err = facade.Chat(ctx, owner, task.ID, "增加意图"); err != nil {
		t.Fatalf("intent chat: %v", err)
	}
	if _, err = store.ControlTask(ctx, owner, task.ID, "pause", false); err != nil {
		t.Fatal(err)
	}
	if err = store.ProcessMainChatWakes(ctx); err != nil {
		t.Fatal(err)
	}
	paused, err = store.GetTask(ctx, owner, task.ID)
	if err != nil || paused.Status != "paused" || paused.Queued {
		t.Fatalf("explicit pause lost: %+v %v", paused, err)
	}
	var intents int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.exploration_nodes WHERE exploration_id=$1 AND kind='intent' AND payload->>'summary'='验证新增意图'`, task.ExplorationID).Scan(&intents); err != nil || intents != 1 {
		t.Fatalf("intent persistence: %d %v", intents, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_task SET status='done',queued=false WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	current = &independentMainProvider{}
	if reply, err := facade.Chat(ctx, owner, task.ID, "已完成后对话"); err != nil || reply == "" {
		t.Fatalf("completed chat: %q %v", reply, err)
	}
	done, err := store.GetTask(ctx, owner, task.ID)
	if err != nil || done.Status != "done" || done.Queued {
		t.Fatalf("completed chat resumed task: %+v %v", done, err)
	}

	// The same scoped asset search and registration gates also work under the
	// independent lease, including an actual human approval of a tool call.
	current = &independentMainProvider{tool: "list_assets", input: fmt.Sprintf(`{"id":%d}`, asset)}
	if _, err = facade.Chat(ctx, owner, task.ID, "查询资产"); err != nil {
		t.Fatalf("asset search chat: %v", err)
	}
	var assetResult string
	if err = db.QueryRowContext(ctx, `SELECT COALESCE(detail,'') FROM tianqiong_ai.activity WHERE exploration_id=$1 AND worker='mainagent' AND kind='tool_result' AND tool='list_assets' ORDER BY id DESC LIMIT 1`, task.ExplorationID).Scan(&assetResult); err != nil || !strings.Contains(assetResult, fmt.Sprint(asset)) {
		t.Fatalf("independent asset search: %q %v", assetResult, err)
	}
	current = &independentMainProvider{tool: "insert_assets", input: `{"assets":[{"type":"service","url":"https://asset.independent-main.example/"}]}`}
	if _, err = facade.Chat(ctx, owner, task.ID, "登记资产"); err != nil {
		t.Fatalf("asset registration chat: %v", err)
	}
	var registrations int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_task_asset a JOIN asset_entity e ON e.id=a.asset_id WHERE a.task_id=$1 AND e.label='https://asset.independent-main.example/'`, task.ID).Scan(&registrations); err != nil || registrations != 1 {
		t.Fatalf("independent asset registration: %d %v", registrations, err)
	}
	oldTools, err := approvals.EnabledTools(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err = approvals.SetEnabledTools(ctx, owner, append(append([]string{}, oldTools...), "add_task_scope")); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = approvals.SetEnabledTools(context.Background(), owner, oldTools) }()
	rule, err := approvals.SaveRule(ctx, owner, approval.Rule{Name: "independent-chat-fixture", Enabled: true, Priority: 1000, MatchTarget: "tool_name", MatchType: "string", Pattern: "add_task_scope", Action: "ask", TimeoutSeconds: 60, TimeoutAction: "deny"}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = approvals.DeleteRule(context.Background(), owner, rule.ID) }()
	current = &independentMainProvider{tool: "add_task_scope", input: `{"kind":"subdomain","value":"child.independent-main.example"}`}
	if err = facade.StartMainChat(ctx, owner, task.ID, "扩大范围", nil, nil); err != nil {
		t.Fatal(err)
	}
	var pending approval.Approval
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		items, _, err := approvals.List(ctx, owner, approval.Filter{Status: "pending", TaskID: task.ID, Page: 1, Size: 20})
		if err != nil {
			t.Fatal(err)
		}
		if len(items) > 0 {
			pending = items[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pending.ID == 0 {
		t.Fatal("independent chat did not request approval")
	}
	if err = approvals.Decide(ctx, owner, pending.ID, "allowed"); err != nil {
		t.Fatalf("independent chat approval: %v", err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		running, err := facade.MainChatRunning(ctx, owner, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("approved chat retained lease")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var scopes int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_task_scope WHERE task_id=$1 AND domain='child.independent-main.example'`, task.ID).Scan(&scopes); err != nil || scopes != 1 {
		t.Fatalf("approved task scope: %d %v", scopes, err)
	}

	files := aiapp.NewAttachmentFacade(airepo.NewConversationStore(orm), aiinfra.NewChatFileStorage(root))
	facade.SetAttachments(files)
	file, err := files.Upload(ctx, owner, "task", fmt.Sprint(task.ID), "evidence.txt", strings.NewReader("附件测试内容"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = files.Upload(ctx, other, "task", fmt.Sprint(task.ID), "foreign.txt", strings.NewReader("x")); err == nil {
		t.Fatal("foreign user uploaded to task")
	}
	forged := file
	forged.Path = "uploads/does-not-exist"
	if err = facade.StartMainChat(ctx, owner, task.ID, "", nil, []aiapp.ChatAttachment{forged}); err == nil {
		t.Fatal("unowned attachment was accepted")
	}
	captured := &independentMainProvider{requests: make(chan string, 1)}
	current = captured
	if err = facade.StartMainChat(ctx, owner, task.ID, "", nil, []aiapp.ChatAttachment{file}); err != nil {
		t.Fatal(err)
	}
	select {
	case prompt := <-captured.requests:
		if !strings.Contains(prompt, file.Abs) {
			t.Fatalf("model did not receive attachment path: %s", prompt)
		}
		content, err := os.ReadFile(file.Abs)
		if err != nil || string(content) != "附件测试内容" {
			t.Fatalf("attachment file: %q %v", content, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("attachment chat did not reach the model")
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		running, err := facade.MainChatRunning(ctx, owner, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("attachment chat retained its lease")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var visibleDetail string
	if err = db.QueryRowContext(ctx, `SELECT COALESCE(detail,'') FROM tianqiong_ai.activity WHERE exploration_id=$1 AND worker='mainagent' AND kind='human_message' AND detail IS NOT NULL ORDER BY id DESC LIMIT 1`, task.ExplorationID).Scan(&visibleDetail); err != nil || !strings.Contains(visibleDetail, "evidence.txt") || strings.Contains(visibleDetail, file.Abs) {
		t.Fatalf("attachment transcript: %q %v", visibleDetail, err)
	}

	// A background turn survives the originating request but is stopped without
	// touching the task state; admission remains serialized while it runs.
	block := &blockingMainProvider{started: make(chan struct{}), cancelled: make(chan struct{})}
	current = block
	requestCtx, cancelRequest := context.WithCancel(ctx)
	if err = facade.StartMainChat(requestCtx, owner, task.ID, "后台消息", nil, nil); err != nil {
		t.Fatal(err)
	}
	cancelRequest()
	select {
	case <-block.started:
	case <-time.After(5 * time.Second):
		t.Fatal("background chat did not start")
	}
	select {
	case <-block.cancelled:
		t.Fatal("request disconnect cancelled the background turn")
	default:
	}
	if err = facade.StartMainChat(ctx, owner, task.ID, "重复消息", nil, nil); !errors.Is(err, aiapp.ErrTaskChatBusy) {
		t.Fatalf("background admission was not serialized: %v", err)
	}
	if ok, err := facade.StopMainChat(ctx, owner, task.ID); err != nil || !ok {
		t.Fatalf("stop background chat: %v %v", ok, err)
	}
	select {
	case <-block.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not cancel the model")
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		running, err := facade.MainChatRunning(ctx, owner, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stopped chat retained its lease")
		}
		time.Sleep(10 * time.Millisecond)
	}
	done, err = store.GetTask(ctx, owner, task.ID)
	if err != nil || done.Status != "done" || done.Queued {
		t.Fatalf("stopping chat changed task: %+v %v", done, err)
	}

	first := uuid.NewString()
	if _, err = store.ClaimMainChat(ctx, owner, task.ID, first); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ClaimMainChat(ctx, owner, task.ID, uuid.NewString()); !errors.Is(err, aiapp.ErrTaskChatBusy) {
		t.Fatalf("duplicate chat lease: %v", err)
	}
	identity := airuntime.TaskExecution{ID: task.ID, ExplorationID: task.ExplorationID, TenantID: task.TenantID, UserID: owner, RunID: first, ExecutionMode: "main_chat"}
	conn, err := connector.OpenForExecution(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `INSERT INTO tianqiong_ai.exploration_nodes(exploration_id,kind,payload,state,origin) VALUES($1,'hint','{"text":"lease test"}','active','human')`, task.ExplorationID); err != nil {
		t.Fatal(err)
	}
	var findingNode, findingID int64
	if err = conn.QueryRowContext(ctx, `INSERT INTO tianqiong_ai.exploration_nodes(exploration_id,kind,payload,state,origin) VALUES($1,'finding','{"summary":"fixture finding"}','confirmed','human') RETURNING id`, task.ExplorationID).Scan(&findingNode); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRowContext(ctx, `INSERT INTO tianqiong_ai.findings(task_id,node_id,vulnclass,name,severity,summary) VALUES($1,$2,'fixture','fixture','low','fixture') RETURNING id`, task.ID, findingNode).Scan(&findingID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.UpdateFindingReport(ctx, owner, first, "main-report", findingNode, "独立对话报告", nil); err != nil {
		t.Fatalf("independent chat report: %v", err)
	}
	var report string
	if err = db.QueryRowContext(ctx, `SELECT report FROM tianqiong_ai.findings WHERE id=$1`, findingID).Scan(&report); err != nil || report != "独立对话报告" {
		t.Fatalf("independent chat report persistence: %q %v", report, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_task_main_chat_run SET lease_until=clock_timestamp()-interval '1 second' WHERE task_id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	second := uuid.NewString()
	if _, err = store.ClaimMainChat(ctx, owner, task.ID, second); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(ctx, `INSERT INTO tianqiong_ai.exploration_nodes(exploration_id,kind,payload,state,origin) VALUES($1,'hint','{"text":"stale"}','active','human')`, task.ExplorationID); err == nil {
		t.Fatal("expired chat lease wrote a graph node")
	}
	if _, err = store.UpdateFindingReport(ctx, owner, first, "stale-report", findingNode, "越界写入", nil); !errors.Is(err, aiapp.ErrRunLeaseLost) {
		t.Fatalf("stale independent report: %v", err)
	}
	if err = store.ReleaseMainChat(ctx, owner, task.ID, first); err != nil {
		t.Fatal(err)
	}
	live, err := store.MainChatRunning(ctx, owner, task.ID)
	if err != nil || !live {
		t.Fatalf("old lease removed successor: %v %v", live, err)
	}
	if _, err = store.StopMainChat(ctx, other, task.ID); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign stop: %v", err)
	}
	if stopped, err := store.StopMainChat(ctx, owner, task.ID); err != nil || stopped != second {
		t.Fatalf("stop: %v %v", stopped, err)
	}
	if ok, err := store.HeartbeatMainChat(ctx, owner, task.ID, second); err != nil || ok {
		t.Fatalf("stopped lease heartbeat: %v %v", ok, err)
	}
	if err = store.ReleaseMainChat(ctx, owner, task.ID, second); err != nil {
		t.Fatal(err)
	}
	trace, err := facade.MainTranscript(ctx, owner, task.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var toolUse, toolResult bool
	for _, step := range trace {
		toolUse = toolUse || step.Kind == "tool_use" && step.Tool == "list_assets"
		toolResult = toolResult || step.Kind == "tool_result" && step.Tool == "list_assets"
	}
	if !toolUse || !toolResult {
		t.Fatalf("main transcript omitted tool trace: %+v", trace)
	}
	if _, err = facade.MainTranscript(ctx, other, task.ID, 0, 0); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign transcript: %v", err)
	}
	parent0 := sidequestion.Parent{TaskID: task.ID, ExplorationID: task.ExplorationID}
	history, err := side.History(ctx, owner, parent0, 0)
	if err != nil || history.Snapshot == nil {
		t.Fatalf("independent main side snapshot: %+v %v", history, err)
	}
	newSegment, err := facade.CreateMainSession(ctx, owner, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	current = &independentMainProvider{}
	if _, err = facade.ChatInSegment(ctx, owner, task.ID, "第二段隔离上下文", &newSegment.Seq); err != nil {
		t.Fatalf("segment chat for side question: %v", err)
	}
	parent1 := sidequestion.Parent{TaskID: task.ID, ExplorationID: task.ExplorationID, MainSeg: newSegment.Seq}
	history, err = side.History(ctx, owner, parent1, 0)
	if err != nil || history.Snapshot == nil {
		t.Fatalf("segment side snapshot: %+v %v", history, err)
	}
	var snapshot string
	if err = db.QueryRowContext(ctx, `SELECT snapshot::text FROM ai_side_session WHERE owner_user_id=$1 AND parent_key=$2`, owner, parent1.Key()).Scan(&snapshot); err != nil || !strings.Contains(snapshot, "第二段隔离上下文") || strings.Contains(snapshot, "查看状态") {
		t.Fatalf("segment side snapshot crossed contexts: %s %v", snapshot, err)
	}
	if _, err = side.History(ctx, other, parent1, 0); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign side snapshot: %v", err)
	}
	missingParent := parent1
	missingParent.MainSeg = 999
	if _, err = side.History(ctx, owner, missingParent, 0); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("unknown segment side snapshot: %v", err)
	}
	question, err := side.Ask(ctx, owner, parent1, "请概括上下文", "side-fixture")
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		item, e := side.Get(ctx, owner, question.ID)
		if e != nil {
			t.Fatal(e)
		}
		if item.Status == "completed" {
			if item.Answer == "" {
				t.Fatal("empty side answer")
			}
			break
		}
		if item.Status == "failed" || time.Now().After(deadline) {
			t.Fatalf("side answer: %+v", item)
		}
		time.Sleep(10 * time.Millisecond)
	}

	messages, err := facade.MainMessages(ctx, owner, task.ID, 0, 0)
	var foundReply bool
	for _, message := range messages {
		foundReply = foundReply || strings.Contains(message.Summary, "独立对话完成")
	}
	if err != nil || len(messages) < 6 || !foundReply {
		t.Fatalf("persisted main messages: %+v %v", messages, err)
	}
}

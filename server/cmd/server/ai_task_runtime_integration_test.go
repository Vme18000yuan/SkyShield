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
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	approval "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/application"
	approvalrepo "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/repository"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	settingsrepo "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	aiinfra "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/infrastructure"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	registryapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	registryrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type taskPipelineProvider struct {
	mu             sync.Mutex
	counts         map[string]int
	db             *sql.DB
	exploration    int64
	asset          int64
	tools          []string
	systems        map[string]string
	toolsByRole    map[string][]string
	mainInputs     []string
	plannerOnce    sync.Once
	plannerStarted chan struct{}
	releasePlanner chan struct{}
}

func (p *taskPipelineProvider) Stream(ctx context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	role := "unknown"
	if strings.Contains(strings.Join(request.System, "\n"), "你是一个授权渗透测试系统的\"主 agent\"") {
		role = "mainagent"
	} else {
		for _, tool := range request.Tools {
			if tool.Name == "add_intent" {
				role = "planner"
				break
			}
			if tool.Name == "record_fact" {
				role = "worker"
			}
			if tool.Name == "set_goals" && role == "unknown" {
				role = "goals"
			}
		}
	}
	p.mu.Lock()
	if p.systems == nil {
		p.systems = map[string]string{}
	}
	if p.toolsByRole == nil {
		p.toolsByRole = map[string][]string{}
	}
	p.systems[role] += strings.Join(request.System, "\n")
	if role == "mainagent" {
		var input string
		for _, message := range request.Messages {
			input += message.Text() + "\n"
		}
		p.mainInputs = append(p.mainInputs, input)
	}
	index := p.counts[role]
	p.counts[role]++
	for _, tool := range request.Tools {
		p.tools = append(p.tools, tool.Name)
		p.toolsByRole[role] = append(p.toolsByRole[role], tool.Name)
	}
	p.mu.Unlock()
	return func(yield func(llm.StreamEvent, error) bool) {
		if role == "planner" && index == 0 && p.plannerStarted != nil && p.releasePlanner != nil {
			p.plannerOnce.Do(func() { close(p.plannerStarted) })
			select {
			case <-p.releasePlanner:
			case <-ctx.Done():
				yield(llm.StreamEvent{}, ctx.Err())
				return
			}
		}
		if role == "mainagent" {
			graphTool := false
			for _, item := range request.Tools {
				name := strings.ToLower(item.Name)
				if name == "graph_overview" {
					graphTool = true
				}
				if name == "bash" || name == "read" || name == "write" || name == "webfetch" {
					yield(llm.StreamEvent{}, errors.New("unsafe MainAgent tool was exposed"))
					return
				}
			}
			if !graphTool {
				yield(llm.StreamEvent{}, errors.New("MainAgent graph tools unavailable"))
				return
			}
		}
		var calls []struct {
			name  string
			input any
		}
		switch {
		case role == "mainagent" && index == 0:
			calls = append(calls, struct {
				name  string
				input any
			}{"graph_overview", map[string]any{}})
		case role == "goals" && index == 0:
			calls = append(calls, struct {
				name  string
				input any
			}{"set_goals", map[string]any{"text": "核验资产并登记证据"}})
			calls = append(calls, struct {
				name  string
				input any
			}{"set_constraints", map[string]any{"type": "deny", "text": "fixture-constraint-secret"}})
			calls = append(calls, struct {
				name  string
				input any
			}{"goal_context_fixture", map[string]any{"probe": "ok"}})
		case role == "planner" && index == 0:
			calls = append(calls, struct {
				name  string
				input any
			}{"add_intent", map[string]any{"summary": "核验所选资产", "asset_ids": []int64{p.asset}, "priority": 5}})
		case role == "worker" && index == 0:
			var intent int64
			if err := p.db.QueryRowContext(ctx, `SELECT id FROM tianqiong_ai.exploration_nodes WHERE exploration_id=$1 AND kind='intent' ORDER BY id LIMIT 1`, p.exploration).Scan(&intent); err != nil {
				yield(llm.StreamEvent{}, err)
				return
			}
			calls = append(calls, struct {
				name  string
				input any
			}{"list_untested_assets", map[string]any{"page": 1, "page_size": 10}})
			calls = append(calls, struct {
				name  string
				input any
			}{"insert_assets", map[string]any{"assets": []map[string]any{{"type": "service", "url": "https://discovered.task-runtime.example/", "page_title": "fixture"}, {"type": "service", "url": "https://agency.gov.cn/"}}}})
			calls = append(calls, struct {
				name  string
				input any
			}{"record_fact", map[string]any{"intent_id": intent, "summary": "模拟资产核验已完成", "detail": "使用确定性测试数据验证工具和图写入链路", "confidence": "observed", "asset_ids": []int64{p.asset}}})
			calls = append(calls, struct {
				name  string
				input any
			}{"report_finding", map[string]any{"intent_id": intent, "vulnclass": "fixture-check", "name": "模拟发现", "severity": "low", "summary": "确定性集成测试发现", "evidence": "本地 fixture，不代表真实漏洞", "asset_ids": []int64{p.asset}}})
		case role == "planner" && index >= 2:
			var intentState string
			if err := p.db.QueryRowContext(ctx, `SELECT state FROM tianqiong_ai.exploration_nodes WHERE exploration_id=$1 AND kind='intent' ORDER BY id LIMIT 1`, p.exploration).Scan(&intentState); err != nil {
				yield(llm.StreamEvent{}, err)
				return
			}
			if intentState != "done" {
				break
			}
			var fact int64
			if err := p.db.QueryRowContext(ctx, `SELECT id FROM tianqiong_ai.exploration_nodes WHERE exploration_id=$1 AND kind='fact' AND state='confirmed' ORDER BY id LIMIT 1`, p.exploration).Scan(&fact); err != nil {
				yield(llm.StreamEvent{}, err)
				return
			}
			rows, err := p.db.QueryContext(ctx, `SELECT id FROM tianqiong_ai.exploration_nodes WHERE exploration_id=$1 AND kind='goal' AND state<>'met' ORDER BY id`, p.exploration)
			if err != nil {
				yield(llm.StreamEvent{}, err)
				return
			}
			var goals []int64
			for rows.Next() {
				var id int64
				if err = rows.Scan(&id); err != nil {
					rows.Close()
					yield(llm.StreamEvent{}, err)
					return
				}
				goals = append(goals, id)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				yield(llm.StreamEvent{}, err)
				return
			}
			for _, goal := range goals {
				calls = append(calls, struct {
					name  string
					input any
				}{"prove_goal", map[string]any{"goal_id": goal, "evidence_id": fact, "reason": "模拟证据已登记"}})
			}
		}
		if !yield(llm.StreamEvent{Type: llm.SEMessageStart}, nil) {
			return
		}
		for i, call := range calls {
			encoded, _ := json.Marshal(call.input)
			if !yield(llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: role + "-call-" + string(rune('a'+index)) + string(rune('a'+i)), ToolName: call.name}, nil) {
				return
			}
			if !yield(llm.StreamEvent{Type: llm.SEToolInputJSON, Text: string(encoded)}, nil) {
				return
			}
		}
		stop := "end_turn"
		if len(calls) > 0 {
			stop = "tool_use"
		} else {
			text := "本轮已完成"
			if role == "mainagent" {
				text = "MainAgent 已根据当前任务状态回复。"
			}
			if !yield(llm.StreamEvent{Type: llm.SETextDelta, Text: text}, nil) {
				return
			}
		}
		if !yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: stop}, nil) {
			return
		}
		yield(llm.StreamEvent{Type: llm.SEMessageStop}, nil)
	}
}

func (p *taskPipelineProvider) Complete(ctx context.Context, request llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, request) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func verifyAITaskRuntimeContract(t *testing.T, ctx context.Context, db *sql.DB, dsn string) {
	t.Helper()
	owner, target, asset := taskFixtureScope(t, ctx, db, "task-runtime.example")
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := airepo.NewTaskStore(orm)
	provider := &taskPipelineProvider{db: db, asset: asset, counts: map[string]int{}, plannerStarted: make(chan struct{}), releasePlanner: make(chan struct{})}
	var releasePlannerOnce sync.Once
	releasePlanner := func() { releasePlannerOnce.Do(func() { close(provider.releasePlanner) }) }
	defer releasePlanner()
	resolve := func(context.Context, int) (llm.Provider, string, bool, error) { return provider, "fixture", true, nil }
	read := registryapp.NewFacade(registryrepo.NewAssetRegistryStore(orm))
	write := registryapp.NewRegistrationFacade(registryrepo.NewAssetRegistrationStore(orm, assetRegistrationCreators(orm)), airuntime.SecretBox{})
	connector, err := airuntime.NewTaskGraphConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	settings := settingsapp.NewAgentSettingFacade(settingsrepo.NewAgentSettingStore(orm))
	toolStore := settingsrepo.NewToolCatalogStore(orm)
	agentCatalog := settingsapp.NewAgentCatalogFacade(settingsrepo.NewAgentCatalogStore(orm))
	var goalHTTPHits atomic.Int32
	goalHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		goalHTTPHits.Add(1)
		_, _ = w.Write([]byte("goal fixture response"))
	}))
	defer goalHTTP.Close()
	toolCatalog := settingsapp.NewToolCatalogFacade(toolStore, agentCatalog, nil, func(_ context.Context, _ int, row settingsapp.ToolRecord, base []tool.CoreTool) tool.CoreTool {
		return airuntime.BuildCustomTool(row, base)
	})
	execution, _ := json.Marshal(map[string]any{"url": goalHTTP.URL, "method": "GET"})
	goalExtra := settingsapp.ToolRecord{Key: "goal_context_fixture", Description: "goal fixture", Schema: json.RawMessage(`{"type":"object","properties":{"probe":{"type":"string"}}}`), Agents: []string{"goals"}, Enabled: true, Kind: "http", Exec: execution}
	if _, err = toolCatalog.Save(ctx, owner, goalExtra.Key, goalExtra, true); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = toolCatalog.Delete(context.Background(), owner, goalExtra.Key) }()
	rows, err := toolCatalog.List(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	var goalTool settingsapp.ToolRecord
	for _, row := range rows {
		if row.Key == "goal_met" {
			goalTool = row
			break
		}
	}
	if goalTool.Key == "" || len(goalTool.Agents) != 0 {
		t.Fatalf("default goal_met binding: %+v", goalTool)
	}
	originalGoalTool := goalTool
	goalTool.Agents = []string{"planner"}
	if _, err = toolCatalog.Save(ctx, owner, goalTool.Key, goalTool, false); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = toolCatalog.Save(context.Background(), owner, originalGoalTool.Key, originalGoalTool, false)
	}()
	var untestedTool settingsapp.ToolRecord
	for _, row := range rows {
		if row.Key == "list_untested_assets" {
			untestedTool = row
			break
		}
	}
	if untestedTool.Key == "" {
		t.Fatal("missing untested-assets catalog entry")
	}
	originalUntested := untestedTool
	untestedTool.Agents = append(append([]string{}, untestedTool.Agents...), "worker")
	if _, err = toolCatalog.Save(ctx, owner, untestedTool.Key, untestedTool, false); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = toolCatalog.Save(context.Background(), owner, originalUntested.Key, originalUntested, false)
	}()
	approvals := approval.NewService(approvalrepo.NewStore(orm))
	if err = approvals.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	write.SetAIAssetGate(func(ctx context.Context, user int, scope *registryapp.RegistrationTaskScope, request string, inputs []registryapp.AssetInput) ([]string, error) {
		return approvals.CheckAssetInputs(ctx, user, scope.TaskID, scope.ExecutionID, inputs)
	})
	oldApprovalTools, err := approvals.EnabledTools(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err = approvals.SetEnabledTools(ctx, owner, append(append([]string{}, oldApprovalTools...), "goal_context_fixture")); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = approvals.SetEnabledTools(context.Background(), owner, oldApprovalTools) }()
	taskApprovalRule, err := approvals.SaveRule(ctx, owner, approval.Rule{Name: "goal approval fixture", Enabled: true, Priority: 1000, MatchTarget: "tool_name", MatchType: "string", Pattern: "goal_context_fixture", Action: "ask", TimeoutSeconds: 60, TimeoutAction: "deny"}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = approvals.DeleteRule(context.Background(), owner, taskApprovalRule.ID) }()
	runner := airuntime.NewTaskRunner(connector, t.TempDir(), airuntime.AssetToolResolver(read, write), settings.Resolve)
	runner.SetAssetIDsGate(func(ctx context.Context, task airuntime.TaskExecution, ids []int64) error {
		return approvals.CheckAssetIDs(ctx, task.UserID, task.ID, task.RunID, ids, task.TargetID)
	})
	runner.SetNativeTools(false, toolCatalog.Resolve)
	runner.SetApprovalWrapper(func(ctx context.Context, user int, key string, info agent.RunInfo, emit func(airuntime.Activity), item tool.CoreTool) tool.CoreTool {
		call := approval.Call{UserID: user, RunID: airuntime.ExecutionID(ctx), ConversationID: info.SessionID, TaskID: info.TaskID, IntentID: info.IntentID, AgentName: key}
		return approvals.Wrap(item, call, func(kind, summary, detail string) {
			if emit != nil {
				emit(airuntime.Activity{Kind: kind, Summary: summary, Detail: detail})
			}
		})
	})

	runner.SetSkillResolver(settingsapp.NewSkillFacade(settingsrepo.NewSkillStore(orm)).Resolve)
	runner.SetConstraintInjectResolver(func(_ context.Context, _ int, role string) (bool, error) { return role != "planner", nil })
	runner.SetAgentResolver(func(ctx context.Context, user int, role string) (airuntime.ChatAgentDefinition, error) {
		definition, _ := airuntime.BuiltinChatAgent(role)
		definition.PromptTemplate += "\nfixture-" + role + "-template"
		settingsKey := definition.SettingsKey
		config, err := settings.Resolve(ctx, user, settingsKey)
		definition.Enabled = config.Enabled
		return definition, err
	})
	var roleMu sync.Mutex
	roleCalls := map[string]int{}
	pinnedCalls := map[string]int{}
	pinnedProfiles := []string{uuid.NewString(), uuid.NewString()}
	runner.SetRoleProviderResolver(func(ctx context.Context, _ int, role string) (llm.Provider, string, bool, error) {
		roleMu.Lock()
		roleCalls[role]++
		if pinned := airuntime.PinnedWorkerProfileFromContext(ctx); pinned != "" {
			pinnedCalls[pinned]++
		}
		roleMu.Unlock()
		return provider, "fixture-" + role, true, nil
	})
	controller := aiapp.NewTaskController(store, runner, resolve)
	controller.SetWorkerSlotsResolver(func(_ context.Context, _ int, override int) ([]string, error) {
		if override != 0 {
			t.Fatalf("expected global worker pools, got override %d", override)
		}
		return pinnedProfiles, nil
	})
	facade := aiapp.NewTaskFacade(store, resolve, controller)
	task, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "原生 Agent 任务闭环", Goal: "核验资产并登记证据", TargetID: target, AssetIDs: []int64{asset}, InterceptRules: []approval.AssetRule{{Enabled: true, Action: "allow", Kind: "fuzzy_domain", Pattern: "task-runtime.example"}}})
	if err != nil {
		t.Fatal(err)
	}
	provider.exploration = task.ExplorationID
	jobCtx, cancel := context.WithCancel(ctx)
	controller.Start(jobCtx)
	defer func() { cancel(); <-controller.Done() }()
	if _, err := facade.Control(ctx, owner, task.ID, "start"); err != nil {
		t.Fatal(err)
	}
	var pendingApproval approval.Approval
	approvalDeadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(approvalDeadline) {
		items, _, e := approvals.List(ctx, owner, approval.Filter{Status: "pending", TaskID: task.ID, Page: 1, Size: 20})
		if e != nil {
			t.Fatal(e)
		}
		if len(items) > 0 {
			pendingApproval = items[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pendingApproval.ID == 0 || goalHTTPHits.Load() != 0 {
		t.Fatalf("goal task did not wait for approval: %+v hits=%d", pendingApproval, goalHTTPHits.Load())
	}
	approvalExec, err := approvals.Execution(ctx, owner, pendingApproval.ID)
	if err != nil || approvalExec.TaskID == nil || *approvalExec.TaskID != fmt.Sprint(task.ID) || approvalExec.Session != "plan" {
		t.Fatalf("task approval execution: %+v %v", approvalExec, err)
	}
	if err = approvals.Decide(ctx, owner, pendingApproval.ID, "allowed"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.plannerStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("AI task planner did not start")
	}
	humanGoal, err := facade.SaveGoal(ctx, owner, task.ID, 0, "人工新增核验目标", "")
	if err != nil {
		t.Fatal(err)
	}
	reply, err := facade.Chat(ctx, owner, task.ID, "请根据当前任务图简要汇报进度")
	if err != nil || reply != "MainAgent 已根据当前任务状态回复。" {
		t.Fatalf("active task MainAgent chat: %q %v", reply, err)
	}
	sessions, err := facade.MainSessions(ctx, owner, task.ID)
	if err != nil || len(sessions) != 1 || sessions[0].Seq != 0 {
		t.Fatalf("initial MainAgent session: %+v %v", sessions, err)
	}
	var otherUser int
	if err = db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&otherUser); err != nil {
		t.Fatal(err)
	}
	if _, err = facade.MainSessions(ctx, otherUser, task.ID); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign main sessions: %v", err)
	}
	created, err := facade.CreateMainSession(ctx, owner, task.ID)
	if err != nil || created.Seq != 1 || created.CreatedAt == nil {
		t.Fatalf("create main session: %+v %v", created, err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO tianqiong_ai.main_sessions(exploration_id,seq) VALUES($1,2)`, task.ExplorationID); err == nil {
		t.Fatal("unfenced main session creation accepted")
	}
	if reply, err = facade.Chat(ctx, owner, task.ID, "新会话独立消息"); err != nil || reply == "" {
		t.Fatalf("new main session chat: %q %v", reply, err)
	}
	zero := 0
	if reply, err = facade.ChatInSegment(ctx, owner, task.ID, "回到原始会话", &zero); err != nil || reply == "" {
		t.Fatalf("original main session chat: %q %v", reply, err)
	}
	missing := 99
	if _, err = facade.ChatInSegment(ctx, owner, task.ID, "不存在的会话", &missing); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("invalid main session: %v", err)
	}
	provider.mu.Lock()
	var newIsolated, oldResumed bool
	for _, input := range provider.mainInputs {
		if strings.Contains(input, "新会话独立消息") && !strings.Contains(input, "请根据当前任务图简要汇报进度") {
			newIsolated = true
		}
		if strings.Contains(input, "回到原始会话") && strings.Contains(input, "请根据当前任务图简要汇报进度") && !strings.Contains(input, "新会话独立消息") {
			oldResumed = true
		}
	}
	provider.mu.Unlock()
	if !newIsolated || !oldResumed {
		t.Fatalf("MainAgent transcript segments crossed or failed to resume: new=%t old=%t", newIsolated, oldResumed)
	}
	oldMessages, err := facade.MainMessages(ctx, owner, task.ID, 0, 0)
	if err != nil || len(oldMessages) != 4 {
		t.Fatalf("original segment messages: %+v %v", oldMessages, err)
	}
	newMessages, err := facade.MainMessages(ctx, owner, task.ID, 1, 0)
	if err != nil || len(newMessages) != 2 {
		t.Fatalf("new segment messages: %+v %v", newMessages, err)
	}
	provider.mu.Lock()
	mainAgentTools := append([]string(nil), provider.toolsByRole["mainagent"]...)
	plannerTools := append([]string(nil), provider.toolsByRole["planner"]...)
	provider.mu.Unlock()
	if !slices.Contains(mainAgentTools, "graph_overview") || !slices.Contains(mainAgentTools, "steer_work") || !slices.Contains(mainAgentTools, "list_assets") || slices.Contains(mainAgentTools, "Bash") || slices.Contains(mainAgentTools, "Read") {
		t.Fatalf("MainAgent capability boundary: %v", mainAgentTools)
	}
	if slices.Contains(plannerTools, "list_assets") || slices.Contains(plannerTools, "list_untested_assets") {
		t.Fatalf("disabled Planner asset grant was bypassed: %v", plannerTools)
	}
	releasePlanner()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		current, err := facade.Get(ctx, owner, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status == "done" {
			var processed bool
			if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_task_human_event WHERE task_id=$1 AND kind='goal' AND processed_at IS NOT NULL AND payload->'goals' ? '人工新增核验目标')`, task.ID).Scan(&processed); err != nil || !processed {
				t.Fatalf("human goal event was not processed: %v %v", processed, err)
			}
			var goalState string
			if err = db.QueryRowContext(ctx, `SELECT state FROM tianqiong_ai.exploration_nodes WHERE id=$1`, humanGoal.ID).Scan(&goalState); err != nil || goalState != "met" {
				t.Fatalf("human goal was not planned: %q %v", goalState, err)
			}
			if _, err := store.ControlTask(ctx, owner, task.ID, "resume", false); err == nil {
				t.Fatal("completed task accepted resume")
			}
			graph, err := facade.Graph(ctx, owner, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			var goalMet, intentDone, fact bool
			var findingNode, intentNode int64
			for _, node := range graph.Nodes {
				if node.Kind == "goal" && node.State == "met" {
					goalMet = true
				}
				if node.Kind == "intent" && node.State == "done" {
					intentDone = true
					intentNode = node.ID
				}
				if node.Kind == "fact" && node.State == "confirmed" {
					fact = true
				}
				if node.Kind == "finding" {
					findingNode = node.ID
				}
			}
			if !goalMet || !intentDone || !fact {
				t.Fatalf("task graph did not preserve agent lifecycle: %+v", graph.Nodes)
			}
			autoTools := aiinfra.AutoTaskReadTools(facade, owner)
			payload, _ := json.Marshal(map[string]any{"task_id": task.ID})
			for _, entry := range autoTools {
				input := payload
				if entry.Name() == "list_tasks" {
					input = []byte(`{}`)
				}
				if entry.Name() == "get_task_node_detail" {
					input, _ = json.Marshal(map[string]any{"task_id": task.ID, "id": findingNode})
				}
				if entry.Name() == "get_task_worker_trace" {
					input, _ = json.Marshal(map[string]any{"task_id": task.ID, "intent_id": intentNode})
				}
				if entry.Name() == "search_task_worker_traces" {
					input, _ = json.Marshal(map[string]any{"task_id": task.ID, "q": "record_fact"})
				}
				result, err := entry.Call(ctx, input, nil)
				if err != nil || result.IsError || !strings.Contains(result.Flatten(), "任务") && entry.Name() == "list_tasks" {
					t.Fatalf("auto tool %s: %+v %v", entry.Name(), result, err)
				}
				if entry.Name() == "list_task_findings" && !strings.Contains(result.Flatten(), "模拟发现") {
					t.Fatalf("task findings missing: %s", result.Flatten())
				}
				if entry.Name() == "get_task_node_detail" && !strings.Contains(result.Flatten(), "模拟发现") {
					t.Fatalf("task node detail missing: %s", result.Flatten())
				}
				if entry.Name() == "get_task_worker_trace" && !strings.Contains(result.Flatten(), "record_fact") {
					t.Fatalf("worker trace missing: %s", result.Flatten())
				}
				if entry.Name() == "get_task_worker_trace" {
					activities, err := facade.Activities(ctx, owner, task.ID, 0)
					if err != nil {
						t.Fatal(err)
					}
					var stepID int64
					for _, activity := range activities {
						if activity.NodeID != nil && *activity.NodeID == intentNode && activity.Kind != "thinking" {
							stepID = activity.ID
							break
						}
					}
					if stepID == 0 {
						t.Fatal("worker trace has no selectable step")
					}
					input, _ := json.Marshal(map[string]any{"task_id": task.ID, "intent_id": intentNode, "step_ids": []int64{stepID}})
					detail, err := entry.Call(ctx, input, nil)
					if err != nil || detail.IsError || !strings.Contains(detail.Flatten(), `"detail"`) {
						t.Fatalf("worker step detail: %+v %v", detail, err)
					}
				}
			}
			var otherUser int
			if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&otherUser); err != nil {
				t.Fatal(err)
			}
			for _, entry := range aiinfra.AutoTaskReadTools(facade, otherUser) {
				if entry.Name() != "get_task_graph" && entry.Name() != "get_task_node_detail" && entry.Name() != "get_task_worker_trace" {
					continue
				}
				input := payload
				if entry.Name() == "get_task_node_detail" {
					input, _ = json.Marshal(map[string]any{"task_id": task.ID, "id": findingNode})
				}
				if entry.Name() == "get_task_worker_trace" {
					input, _ = json.Marshal(map[string]any{"task_id": task.ID, "intent_id": intentNode})
				}
				result, err := entry.Call(ctx, input, nil)
				if err != nil || !result.IsError {
					t.Fatalf("auto read another owner's task: %+v %v", result, err)
				}
			}
			var discovered int
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM website w
				JOIN asset_website_binding b ON b.website_id=w.id
				JOIN ai_task_asset a ON a.asset_id=b.asset_id AND a.task_id=$1 AND a.source='discovered'
				JOIN tianqiong_ai.exploration_anchors anchor ON anchor.asset_id=a.asset_id
				JOIN tianqiong_ai.exploration_nodes n ON n.id=anchor.node_id AND n.exploration_id=$2 AND n.kind='intent'
				WHERE w.target_id=$3 AND w.url='https://discovered.task-runtime.example/'`, task.ID, task.ExplorationID, target).Scan(&discovered); err != nil || discovered != 1 {
				activities, _ := facade.Activities(ctx, owner, task.ID, 0)
				t.Fatalf("discovered asset did not reach original table and task intent: %d %v activities=%+v", discovered, err, activities)
			}
			calls, err := facade.ToolExecutions(ctx, owner, "insert_assets", 0)
			if err != nil || len(calls) != 1 || calls[0].Status != "succeeded" || calls[0].Output == "" || calls[0].TaskID != task.ID {
				t.Fatalf("task tool execution and result pairing: %+v %v", calls, err)
			}
			if !strings.Contains(calls[0].Output, "命中资产拦截规则") {
				t.Fatalf("Worker asset insertion did not enforce global rule: %+v", calls[0])
			}
			coverageCalls, err := facade.ToolExecutions(ctx, owner, "list_untested_assets", 0)
			if err != nil || len(coverageCalls) != 1 || coverageCalls[0].Status != "succeeded" {
				t.Fatalf("task asset tool failed: %+v %v", coverageCalls, err)
			}
			assets, err := facade.Assets(ctx, owner, task.ID, 0)
			if err != nil || len(assets) != 2 {
				t.Fatalf("task assets: %+v %v", assets, err)
			}
			var recorded, discoveredOnly bool
			for _, entry := range assets {
				if entry.ID == asset && entry.FactCount > 0 {
					recorded = true
				}
				if entry.Label == "https://discovered.task-runtime.example/" && entry.FactCount == 0 {
					discoveredOnly = true
				}
			}
			if !recorded || !discoveredOnly {
				t.Fatalf("fact attribution is incorrect: %+v", assets)
			}
			activities, err := facade.Activities(ctx, owner, task.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			var mainUserMessage, mainAssistantMessage bool
			for _, activity := range activities {
				if activity.Worker == "mainagent" && activity.Kind == "human_message" && strings.Contains(activity.Summary, "汇报进度") {
					mainUserMessage = true
				}
				if activity.Worker == "mainagent" && activity.Kind == "assistant_message" && strings.Contains(activity.Summary, "MainAgent 已根据当前任务状态回复") {
					mainAssistantMessage = true
				}
			}
			if !mainUserMessage || !mainAssistantMessage {
				t.Fatalf("MainAgent messages were not retained with task activities: %+v", activities)
			}
			mainCalls, err := facade.ToolExecutions(ctx, owner, "graph_overview", 0)
			if err != nil || len(mainCalls) != 1 || mainCalls[0].Status != "succeeded" || mainCalls[0].TaskID != task.ID {
				t.Fatalf("MainAgent graph tool execution: %+v %v", mainCalls, err)
			}
			verifyAITaskHTTPContract(t, db, facade, owner, task.ID, calls[0].ID)
			workerConfig, err := settings.Resolve(ctx, owner, "worker")
			if err != nil {
				t.Fatal(err)
			}
			workerConfig.Enabled = false
			if err := settings.Save(ctx, owner, workerConfig); err != nil {
				t.Fatal(err)
			}
			outcome, runErr := runner.RunTask(ctx, taskExecution(current), provider, "fixture")
			if runErr != nil || outcome.Status != "paused" {
				t.Fatalf("disabled worker did not pause task: %+v %v", outcome, runErr)
			}
			workerConfig.Enabled = true
			if err := settings.Save(ctx, owner, workerConfig); err != nil {
				t.Fatal(err)
			}
			var risks int
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM vulnerability v JOIN ai_finding_risk_link l ON l.vulnerability_id=v.id WHERE l.task_id=$1 AND v.target_id=$2`, task.ID, target).Scan(&risks); err != nil || risks != 1 {
				activities, _ := facade.Activities(ctx, owner, task.ID, 0)
				for _, activity := range activities {
					if activity.Kind == "tool_result" && activity.IsError {
						t.Logf("tool %s failed: %s", activity.Tool, activity.Detail)
					}
				}
				t.Fatalf("AI finding did not reach original risk table: %d %v", risks, err)
			}
			provider.mu.Lock()
			defer provider.mu.Unlock()
			if !slices.Contains(provider.toolsByRole["goals"], "goal_context_fixture") || goalHTTPHits.Load() != 1 {
				t.Fatalf("Goals custom tool not executed: tools=%v hits=%d", provider.toolsByRole["goals"], goalHTTPHits.Load())
			}
			if !slices.Contains(provider.toolsByRole["planner"], "goal_met") {
				t.Fatal("explicitly bound goal_met did not reach planner")
			}
			if strings.Contains(provider.systems["planner"], "fixture-constraint-secret") || !strings.Contains(provider.systems["worker"], "fixture-constraint-secret") {
				t.Fatalf("constraint injection settings not applied: planner=%q worker=%q", provider.systems["planner"], provider.systems["worker"])
			}
			if !strings.Contains(provider.systems["planner"], "fixture-planner-instruction") || !strings.Contains(provider.systems["worker"], "fixture-worker-instruction") {
				t.Fatalf("agent settings did not reach task prompts")
			}
			for _, role := range []string{"goals", "planner", "worker", "mainagent"} {
				if !strings.Contains(provider.systems[role], "fixture-"+role+"-template") {
					t.Fatalf("%s prompt override did not reach the model", role)
				}
				roleMu.Lock()
				calls := roleCalls[role]
				roleMu.Unlock()
				if calls < 2 {
					t.Fatalf("%s provider was not resolved for execution: %d", role, calls)
				}
			}
			roleMu.Lock()
			firstPinned, secondPinned := pinnedCalls[pinnedProfiles[0]], pinnedCalls[pinnedProfiles[1]]
			roleMu.Unlock()
			if firstPinned < 2 || secondPinned < 1 {
				t.Fatalf("worker pools did not resolve their pinned models: %d %d", firstPinned, secondPinned)
			}
			for _, name := range provider.toolsByRole["planner"] {
				if name == "search_platform_assets" {
					t.Fatal("disabled planner tool remained available")
				}
			}
			if !slices.Contains(provider.toolsByRole["worker"], "Skill") || slices.Contains(provider.toolsByRole["planner"], "Skill") {
				t.Fatal("skill Agent bindings were not respected")
			}
			if !slices.Contains(provider.toolsByRole["worker"], "search_platform_assets") {
				t.Fatal("planner setting affected worker tool grants")
			}
			for _, name := range provider.tools {
				if name == "Bash" || name == "Read" || name == "Write" || name == "WebFetch" {
					t.Fatalf("task leaked an unprovided host tool: %s", name)
				}
			}
			return
		}
		if current.Status == "failed" || current.Status == "paused" {
			activities, _ := facade.Activities(ctx, owner, task.ID, 0)
			t.Fatalf("task failed: %+v activities=%+v", current, activities)
		}
		time.Sleep(20 * time.Millisecond)
	}
	activities, _ := facade.Activities(ctx, owner, task.ID, 0)
	t.Fatalf("task pipeline timed out: %+v", activities)
}

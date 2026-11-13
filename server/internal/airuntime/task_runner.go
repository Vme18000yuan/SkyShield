package airuntime

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Autumn-27/artex/agent"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/llmpool"
	"github.com/Autumn-27/artex/sidequestion"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/skill"
	"github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

type TaskExecution struct {
	ID                   int64
	ExplorationID        int64
	TenantID             string
	UserID               int
	RunID                string
	ExecutionMode        string // empty for a planner/worker run; main_chat for an independent turn
	Goal                 string
	Description          string
	TargetID             int
	WorkerCount          int
	WorkerProfiles       []string
	MaxTurns             int
	PlanHeartbeatSeconds int
	Deadline             time.Time
	Bootstrap            bool
	CoverageEnabled      *bool
}

func taskCoverageEnabled(task TaskExecution) bool {
	return task.CoverageEnabled == nil || *task.CoverageEnabled
}

type TaskOutcome struct{ Status, Reason string }

type TaskRunner struct {
	modelBudget           ModelRunBudgetResolver
	runLocks              executionLocks
	governor              *ExecutionGovernor
	localTools            bool
	toolLayer             ToolLayerResolver
	sidePublisher         func(context.Context, TaskExecution, sidequestion.Snapshot)
	sideDrain             func(int, string)
	connector             *TaskGraphConnector
	root                  string
	tools                 func(int) []tool.CoreTool
	settings              AgentConfigResolver
	skills                SkillResolver
	mcp                   MCPAugmenter
	agents                TaskAgentResolver
	providers             RoleProviderResolver
	constraintInject      ConstraintInjectResolver
	approvalWrapper       ApprovalToolWrapper
	assetIDsGate          TaskAssetIDsGate
	intelligenceEnabled   func(context.Context, int) bool
	intelligenceRecord    func(context.Context, int64, int64, string, artexdb.IntelligenceInput) (int64, error)
	trafficResolver       TaskTrafficResolver
	findingTrafficEnabled func(context.Context, int) bool
	findingTrafficRecord  func(context.Context, artexdb.RecordFindingInput, []artexdb.TrafficRef) (*artexdb.RecordedFinding, error)
	chatMu                sync.RWMutex
	chats                 map[int64]*taskChatSession
}

func (r *TaskRunner) SetExecutionGovernor(g *ExecutionGovernor)            { r.governor = g }
func (r *TaskRunner) SetTaskBudgetResolver(resolve ModelRunBudgetResolver) { r.modelBudget = resolve }

// SetSkillResolver is configured once by the host before the runner starts.
func (r *TaskRunner) SetSkillResolver(resolver SkillResolver)        { r.skills = resolver }
func (r *TaskRunner) SetApprovalWrapper(wrapper ApprovalToolWrapper) { r.approvalWrapper = wrapper }
func (r *TaskRunner) SetNativeTools(enabled bool, layer ToolLayerResolver) {
	r.localTools = enabled
	r.toolLayer = layer
}
func (r *TaskRunner) SetMCPAugmenter(augmenter MCPAugmenter) { r.mcp = augmenter }
func (r *TaskRunner) SetSidePublisher(publish func(context.Context, TaskExecution, sidequestion.Snapshot)) {
	r.sidePublisher = publish
}

func (r *TaskRunner) SetSideDrain(drain func(int, string)) { r.sideDrain = drain }

type TaskAgentResolver func(context.Context, int, string) (ChatAgentDefinition, error)
type RoleProviderResolver func(context.Context, int, string) (llm.Provider, string, bool, error)
type ConstraintInjectResolver func(context.Context, int, string) (bool, error)
type TaskAssetIDsGate func(context.Context, TaskExecution, []int64) error

func (r *TaskRunner) SetAssetIDsGate(gate TaskAssetIDsGate)          { r.assetIDsGate = gate }
func (r *TaskRunner) SetTrafficResolver(resolve TaskTrafficResolver) { r.trafficResolver = resolve }
func (r *TaskRunner) SetFindingTrafficHooks(enabled func(context.Context, int) bool, record func(context.Context, artexdb.RecordFindingInput, []artexdb.TrafficRef) (*artexdb.RecordedFinding, error)) {
	r.findingTrafficEnabled = enabled
	r.findingTrafficRecord = record
}

func (r *TaskRunner) SetIntelligenceHooks(enabled func(context.Context, int) bool, record func(context.Context, int64, int64, string, artexdb.IntelligenceInput) (int64, error)) {
	r.intelligenceEnabled, r.intelligenceRecord = enabled, record
}

func (r *TaskRunner) SetConstraintInjectResolver(resolver ConstraintInjectResolver) {
	r.constraintInject = resolver
}
func (r *TaskRunner) constraintsEnabled(ctx context.Context, user int, role string) bool {
	if r.constraintInject == nil {
		return true
	}
	enabled, err := r.constraintInject(ctx, user, role)
	return err != nil || enabled
}

func (r *TaskRunner) SetAgentResolver(resolver TaskAgentResolver)           { r.agents = resolver }
func (r *TaskRunner) SetRoleProviderResolver(resolver RoleProviderResolver) { r.providers = resolver }

// Resolve on each model call so a task follows changes to its role binding.
type roleProvider struct {
	ctx     context.Context
	resolve RoleProviderResolver
	user    int
	role    string
}

func (p roleProvider) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		provider, _, ok, err := p.resolve(ctx, p.user, p.role)
		if err == nil && (!ok || provider == nil) {
			err = errors.New("AI role model is not configured")
		}
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}
		for event, streamErr := range provider.Stream(ctx, req) {
			if !yield(event, streamErr) {
				return
			}
		}
	}
}

func (p roleProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	provider, _, ok, err := p.resolve(ctx, p.user, p.role)
	if err != nil {
		return llm.Message{}, "", llm.Usage{}, err
	}
	if !ok || provider == nil {
		return llm.Message{}, "", llm.Usage{}, errors.New("AI role model is not configured")
	}
	return provider.Complete(ctx, req)
}

func (p roleProvider) RuntimeModelOptions() RuntimeModelOptions {
	provider, _, ok, err := p.resolve(p.ctx, p.user, p.role)
	if err == nil && ok {
		return ModelOptions(provider)
	}
	return RuntimeModelOptions{ContextWindow: 32_000}
}

func (p roleProvider) ContextWindowTokens() int { return p.RuntimeModelOptions().ContextWindow }

func NewTaskRunner(connector *TaskGraphConnector, root string, tools func(int) []tool.CoreTool, resolvers ...AgentConfigResolver) *TaskRunner {
	runner := &TaskRunner{connector: connector, root: root, tools: tools, chats: make(map[int64]*taskChatSession)}
	if len(resolvers) > 0 {
		runner.settings = resolvers[0]
	}
	return runner
}

type taskWork struct {
	cancel       context.CancelCauseFunc
	steer        []string
	pauseRequest string
}
type taskWorkEvent struct {
	intent int64
	detail string
	slot   int
}

var taskGraphTools = map[string]bool{
	"graph_overview": true, "list_findings": true, "list_facts": true, "node_detail": true, "expand_digest": true,
	"get_worker_output": true, "get_worker_trace": true, "search_all_worker_traces": true, "list_goals": true,
	"add_intent": true, "prove_goal": true, "goal_met": true, "kill_work": true, "steer_work": true, "report_finding": true,
	"record_fact": true, "record_intelligence": true, "add_hint": true, "set_goals": true, "set_constraints": true,
}

func RequiresTaskContext(name string) bool { return taskGraphTools[name] }

// RunTask retains ARTEX's Goals, Planner, Worker, graph tools, transcript resume,
// context management and terminal vocabulary. The host supplies durable task
// admission and fencing, rather than starting ARTEX's independent Web server.
func (r *TaskRunner) RunTask(ctx context.Context, task TaskExecution, provider llm.Provider, model string) (out TaskOutcome, runErr error) {
	if r == nil || r.connector == nil || provider == nil || task.ID <= 0 || task.ExplorationID <= 0 || task.UserID <= 0 ||
		!canonicalUUID(task.TenantID) || !canonicalUUID(task.RunID) || task.WorkerCount < 0 || task.WorkerCount > 50 || !filepath.IsAbs(r.root) {
		return TaskOutcome{Status: "failed"}, errors.New("invalid task execution")
	}
	if !task.Deadline.IsZero() {
		var stop context.CancelFunc
		ctx, stop = context.WithDeadlineCause(ctx, task.Deadline.Add(30*time.Second), agent.AbortRunHardTimeout)
		defer stop()
	}
	unlock, lockErr := r.runLocks.acquire(ctx, fmt.Sprintf("%s/%d", task.TenantID, task.ID))
	if lockErr != nil {
		if errors.Is(context.Cause(ctx), agent.AbortRunHardTimeout) {
			return TaskOutcome{Status: "timeout", Reason: "等待上次执行退出时任务预算已用尽"}, nil
		}
		return TaskOutcome{Status: "paused", Reason: "等待上次执行退出时中断"}, lockErr
	}
	defer unlock()
	if len(task.WorkerProfiles) > 0 {
		if len(task.WorkerProfiles) > 50 || task.WorkerCount > 0 && len(task.WorkerProfiles) != task.WorkerCount {
			return TaskOutcome{Status: "failed"}, errors.New("invalid worker slots")
		}
		task.WorkerCount = len(task.WorkerProfiles)
	}
	if task.WorkerCount == 0 {
		task.WorkerCount = 3
	}
	ctx = WithTaskExecution(ctx, task)
	ctx = WithProcessGovernor(ctx, r.governor, task.UserID)
	budgetCtx, budgetErr := withModelRunBudget(ctx, task.UserID, r.modelBudget)
	if budgetErr != nil {
		return TaskOutcome{Status: "paused", Reason: "无法读取任务运行预算配置"}, budgetErr
	}
	ctx = budgetCtx

	permitCtx, release, permitErr := r.governor.Acquire(ctx, task.UserID, "task", true)
	if permitErr != nil {
		if errors.Is(context.Cause(ctx), agent.AbortRunHardTimeout) {
			return TaskOutcome{Status: "timeout", Reason: "等待执行容量时任务时间预算已用尽"}, nil
		}
		return TaskOutcome{Status: "paused", Reason: "等待 AI 执行容量时中断"}, permitErr
	}
	defer release()
	ctx = permitCtx
	plannerConfig, workerConfig := DefaultAgentConfig("planner"), DefaultAgentConfig("worker")
	if r.settings != nil {
		var err error
		plannerConfig, err = r.settings(ctx, task.UserID, "planner")
		if err != nil {
			return TaskOutcome{Status: "paused", Reason: "无法读取规划 Agent 设置"}, err
		}
		workerConfig, err = r.settings(ctx, task.UserID, "worker")
		if err != nil {
			return TaskOutcome{Status: "paused", Reason: "无法读取执行 Agent 设置"}, err
		}
	}
	definitions := make(map[string]ChatAgentDefinition)
	if r.agents != nil {
		for _, role := range []string{"planner", "worker"} {
			definition, err := r.agents(ctx, task.UserID, role)
			if err != nil {
				return TaskOutcome{Status: "paused", Reason: "无法读取任务 Agent 配置"}, err
			}
			if !definition.Enabled {
				return TaskOutcome{Status: "paused", Reason: "任务所需的 Agent 已停用"}, nil
			}
			definitions[role] = definition
		}
		for role, config := range map[string]*AgentConfig{"planner": &plannerConfig, "worker": &workerConfig} {
			definition := definitions[role]
			config.Enabled = definition.Enabled
			if definition.RuntimeConfigured {
				config.MaxTurns, config.RunSeconds = definition.MaxTurns, definition.RunSeconds
			}
		}
	}
	if !plannerConfig.Enabled || !workerConfig.Enabled {
		return TaskOutcome{Status: "paused", Reason: "任务所需的 Agent 已停用"}, nil
	}
	var plannerSkills, workerSkills, goalSkills []skill.Skill
	if r.skills != nil {
		var err error
		plannerSkills, err = r.skills(ctx, task.UserID, "planner")
		if err != nil {
			return TaskOutcome{Status: "paused", Reason: "无法读取规划 Agent 的 Skill"}, err
		}
		workerSkills, err = r.skills(ctx, task.UserID, "worker")
		if err != nil {
			return TaskOutcome{Status: "paused", Reason: "无法读取执行 Agent 的 Skill"}, err
		}
	}
	userRoot := filepath.Join(r.root, task.TenantID, fmt.Sprintf("user-%d", task.UserID))
	if err := os.MkdirAll(userRoot, 0o700); err != nil {
		return TaskOutcome{Status: "failed"}, err
	}
	var capture *TrafficAccess
	if r.trafficResolver != nil {
		var err error
		capture, err = r.trafficResolver(ctx, task)
		if err != nil {
			return TaskOutcome{Status: "paused", Reason: "流量录制环境不可用"}, err
		}
		if capture != nil {
			ctx, err = withTrafficCapture(ctx, capture)
			if err != nil {
				if capture.Lease != nil {
					capture.Lease.Close()
				}
				return TaskOutcome{Status: "paused", Reason: "流量录制环境不可用"}, err
			}
			defer capture.Lease.Close()
		}
	}
	var local *NativeLocalTools
	workRoot := filepath.Join(userRoot, "work")
	ctx = withWorkspace(ctx, workRoot, filepath.Join(workRoot, "tasks", fmt.Sprint(task.ID)))
	if r.localTools {
		var err error
		local, err = NewNativeLocalTools(ctx, filepath.Join(userRoot, "work"))
		if err != nil {
			return TaskOutcome{Status: "paused", Reason: "本地工具执行环境启动失败"}, err
		}
		defer local.Close()
	}
	sqlDB, err := r.connector.open(ctx, task)
	if err != nil {
		return TaskOutcome{Status: "failed"}, err
	}
	db := &artexdb.DB{DB: sqlDB}
	graph := db.Exploration(task.ExplorationID)
	taskCtx, cancelTask := context.WithCancelCause(ctx)
	if !task.Deadline.IsZero() {
		hardStop := time.AfterFunc(time.Until(task.Deadline.Add(30*time.Second)), func() { cancelTask(agent.AbortRunHardTimeout) })
		defer hardStop.Stop()
	}
	var workWG sync.WaitGroup
	var controlWG sync.WaitGroup
	var worksMu sync.Mutex
	works := map[int64]*taskWork{}
	var writeMu sync.Mutex
	var writeErr error
	var chatSession *taskChatSession
	defer func() {
		if chatSession != nil {
			r.removeTaskChat(chatSession)
		}
		cancelTask(agent.AbortShutdown)
		if chatSession != nil {
			chatSession.close()
		}
		controlWG.Wait()
		workWG.Wait()
		if r.sideDrain != nil {
			r.sideDrain(task.UserID, task.RunID)
		}
		if out.Status == "done" || out.Status == "timeout" {
			state := "stopped"
			if out.Status == "timeout" {
				state = "exhausted"
			}
			_, _ = sqlDB.Exec(`UPDATE exploration_nodes SET state=$1,completed_at=clock_timestamp() WHERE exploration_id=$2 AND kind='intent' AND state='running'`, state, task.ExplorationID)
		}
		_ = sqlDB.Close()
	}()
	ctx = withExecutionID(taskCtx, task.RunID)
	if r.sidePublisher != nil {
		ctx = sidequestion.WithPublisher(ctx, func(snapshot sidequestion.Snapshot) { r.sidePublisher(ctx, task, snapshot) })
	}
	ctx = withTaskTarget(ctx, task.TargetID)
	ctx = agent.WithTaskClock(ctx, agent.TaskClock{DeadlineUnix: taskDeadlineUnix(task.Deadline)})
	var emit func(artexdb.Activity)
	ctx = agent.WithHostPolicy(ctx, agent.HostPolicy{ExternalBackgroundTasks: r.localTools, AllowWebFetch: false, CheckAssetIDs: func(ctx context.Context, ids []int64) error {
		if r.assetIDsGate == nil {
			return nil
		}
		return r.assetIDsGate(ctx, task, ids)
	}, IntelligenceEnabled: func(ctx context.Context) bool {
		return r.intelligenceEnabled != nil && r.intelligenceEnabled(ctx, task.UserID)
	}, IntelligenceRecord: r.intelligenceRecord, FindingTrafficEnabled: func(ctx context.Context) bool {
		return r.findingTrafficEnabled != nil && r.findingTrafficEnabled(ctx, task.UserID)
	}, FindingTrafficRecord: r.findingTrafficRecord, DecorateTool: func(ctx context.Context, key string, item tool.CoreTool) tool.CoreTool {
		if r.approvalWrapper == nil {
			return item
		}
		info := agent.RunInfoFrom(ctx)
		return r.approvalWrapper(ctx, task.UserID, key, info, func(event Activity) {
			var nodeID *int64
			if info.IntentID > 0 {
				value := info.IntentID
				nodeID = &value
			}
			if emit != nil {
				emit(artexdb.Activity{NodeID: nodeID, Worker: key, Kind: event.Kind, Tool: event.Tool, ToolUseID: event.ToolUseID, IsError: event.IsError, Summary: event.Summary, Detail: event.Detail})
			}
		}, item)
	}, PromptTemplate: func(key string) (string, bool) {
		definition, ok := definitions[key]
		return definition.PromptTemplate, ok
	}, MaxTurns: func(key string, fallback int) int {
		if key == "goals" {
			return fallback
		}
		if definition, ok := definitions[key]; ok && definition.RuntimeConfigured {
			return definition.MaxTurns
		}
		return fallback
	}, Wrapup: func(key string) agent.WrapupPolicy {
		return AgentWrapupPolicy(definitions[key])
	}, AugmentTools: func(ctx context.Context, key string) ([]tool.CoreTool, agent.DeferredInfo, func()) {
		if r.mcp == nil {
			return nil, agent.DeferredInfo{}, nil
		}
		return r.mcp(ctx, task.UserID, key)
	}, AdditionalSystemPrompt: func(key string) string {
		if key == "planner" {
			return plannerConfig.AdditionalPrompt
		}
		if key == "worker" {
			return workerConfig.AdditionalPrompt
		}
		return ""
	}, TransformTools: func(ctx context.Context, key string, base []tool.CoreTool) []tool.CoreTool {
		allowed := make([]tool.CoreTool, 0, len(base))
		for _, item := range base {
			if taskGraphTools[item.Name()] {
				allowed = append(allowed, item)
			}
		}
		if r.tools != nil {
			config := plannerConfig
			if key == "worker" {
				config = workerConfig
			}
			allowed = append(allowed, independentChatTools(FilterAgentTools(config, r.tools(task.UserID)))...)
		}
		if key == "planner" || key == "worker" {
			config := plannerConfig
			if key == "worker" {
				config = workerConfig
			}
			assets := []tool.CoreTool{taskListAssetsTool(sqlDB, task)}
			if taskCoverageEnabled(task) {
				assets = append(assets, taskUntestedAssetsTool(sqlDB, task))
			}
			allowed = append(allowed, FilterAgentTools(config, assets)...)
		}
		allowed = append(allowed, scopedTrafficTools(capture)...)
		if key == "goals" {
			allowed = AppendSkills(allowed, goalSkills)
		}
		if key == "planner" {
			allowed = AppendSkills(allowed, plannerSkills)
		}
		if key == "worker" {
			allowed = AppendSkills(allowed, workerSkills)
		}
		if local != nil && key != "goals" {
			options := DefaultAgentOptions(key)
			if definition, ok := definitions[key]; ok {
				options = definition.Options
			}
			allowed = append(allowed, local.Tools(options.InteractiveShell)...)
		}
		if r.toolLayer != nil {
			resolved, err := r.toolLayer(ctx, task.UserID, key, allowed)
			if err != nil {
				cancelTask(err)
				return nil
			}
			allowed = resolved
		}
		return allowed
	}})
	emit = func(event artexdb.Activity) {
		if _, err := graph.AppendActivity(event); err != nil {
			writeMu.Lock()
			if writeErr == nil {
				writeErr = err
			}
			writeMu.Unlock()
			cancelTask(err)
		}
	}
	ctx = withExecutionProgress(ctx, func(message string) {
		emit(artexdb.Activity{Worker: "system", Kind: "progress", Summary: message, Detail: message})
	})
	// A resumed run owns a new token before recovering interrupted intentions.
	if _, err := sqlDB.Exec(`UPDATE exploration_nodes SET state='open',owner=NULL,completed_at=NULL WHERE exploration_id=$1 AND kind='intent' AND state='running'`, task.ExplorationID); err != nil {
		return TaskOutcome{Status: "failed"}, err
	}
	goals, err := graph.ListByKind(artexdb.KindGoal, 10000)
	if err != nil {
		return TaskOutcome{Status: "failed"}, err
	}
	roleModel := func(role string) (llm.Provider, string, error) {
		if r.providers == nil {
			return provider, model, nil
		}
		_, roleModel, ok, err := r.providers(ctx, task.UserID, role)
		if err != nil {
			return nil, "", err
		}
		if !ok {
			return nil, "", errors.New("AI role model is not configured")
		}
		return roleProvider{ctx: ctx, resolve: r.providers, user: task.UserID, role: role}, roleModel, nil
	}
	plannerProvider, plannerModel, err := roleModel("planner")
	if err != nil {
		return TaskOutcome{Status: "paused", Reason: "规划 Agent 的模型不可用"}, err
	}
	workerProvider, workerModel, err := roleModel("worker")
	if err != nil {
		return TaskOutcome{Status: "paused", Reason: "执行 Agent 的模型不可用"}, err
	}
	if len(goals) == 0 && task.Bootstrap {
		if r.agents != nil {
			definition, err := r.agents(ctx, task.UserID, "goals")
			if err != nil {
				return TaskOutcome{Status: "paused", Reason: "无法读取目标拆解 Agent 配置"}, err
			}
			if !definition.Enabled {
				return TaskOutcome{Status: "paused", Reason: "目标拆解 Agent 已停用"}, nil
			}
			definitions["goals"] = definition
		}
		if r.skills != nil {
			goalSkills, err = r.skills(ctx, task.UserID, "goals")
			if err != nil {
				return TaskOutcome{Status: "paused", Reason: "无法读取目标拆解 Agent 的 Skill"}, err
			}
		}
		goalsProvider, _, err := roleModel("goals")
		if err != nil {
			return TaskOutcome{Status: "paused", Reason: "目标拆解 Agent 的模型不可用"}, err
		}
		emit(artexdb.Activity{Worker: "planner", Kind: "round", Summary: "第 0 轮目标拆解"})
		if taskCtx.Err() != nil {
			return TaskOutcome{Status: "paused", Reason: "任务执行已中断"}, taskCtx.Err()
		}
		goalsCtx, cancelGoals := context.WithCancel(ctx)
		if !task.Deadline.IsZero() {
			cancelGoals()
			goalsCtx, cancelGoals = context.WithDeadline(ctx, task.Deadline)
		}
		cancelBudget := func() {}
		if definition, ok := definitions["goals"]; ok && definition.RunSeconds > 0 {
			goalsCtx, cancelBudget = context.WithTimeout(goalsCtx, time.Duration(definition.RunSeconds)*time.Second)
		}
		options := ModelOptions(goalsProvider)
		agent.DecomposeGoalsWithProvider(goalsCtx, goalsProvider, userRoot, task.Goal, task.Description, nil, graph, task.ID, options.NonStreaming, options.MaxTokens, emit)
		cancelBudget()
		cancelGoals()
		if taskCtx.Err() != nil {
			return TaskOutcome{Status: "paused", Reason: "任务执行已中断"}, taskCtx.Err()
		}
		if !task.Deadline.IsZero() && !time.Now().Before(task.Deadline) {
			return TaskOutcome{Status: "timeout", Reason: "目标拆解已达到任务时间预算"}, nil
		}
		goals, err = graph.ListByKind(artexdb.KindGoal, 10000)
		if err != nil {
			return TaskOutcome{Status: "failed"}, err
		}
		if len(goals) == 0 {
			if _, err := graph.AddGoal(map[string]any{"text": task.Goal}, "human"); err != nil {
				return TaskOutcome{Status: "failed"}, err
			}
		}
	}
	if task.Bootstrap {
		result, err := sqlDB.ExecContext(ctx, `UPDATE public.ai_task SET bootstrap_complete=TRUE WHERE id=$1 AND tenant_id=$2::uuid AND owner_user_id=$3 AND active_run=$4::uuid AND status='running' AND lease_until>clock_timestamp() AND deleted_at IS NULL`, task.ID, task.TenantID, task.UserID, task.RunID)
		if err != nil {
			return TaskOutcome{Status: "paused", Reason: "无法保存目标拆解状态"}, err
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return TaskOutcome{Status: "paused", Reason: "任务运行租约已失效"}, errors.New("task bootstrap lease lost")
		}
	}
	store := transcript.NewStore(filepath.Join(userRoot, "transcripts"))
	plannerTurns := task.MaxTurns
	if definitions["planner"].RuntimeConfigured || plannerConfig.MaxTurns > 0 {
		plannerTurns = plannerConfig.MaxTurns
	}
	planner := agent.NewPlanner(plannerProvider, plannerModel, filepath.Join(userRoot, "work"), store, ModelOptions(plannerProvider).ContextWindow, plannerTurns)
	planner.SetRunTimeout(time.Duration(plannerConfig.RunSeconds) * time.Second)
	planner.SetCompactionWindowResolver(func() int { return ModelOptions(plannerProvider).ContextWindow })
	planner.SetNonStreaming(func() bool { return ModelOptions(plannerProvider).NonStreaming })
	planner.SetMaxTokens(func() int { return ModelOptions(plannerProvider).MaxTokens })
	planner.SetNoaEnabled(func() bool { return ModelOptions(plannerProvider).NoaCompaction })
	planner.SetConstraintInject(func() bool { return r.constraintsEnabled(taskCtx, task.UserID, "planner") })
	planner.SetKillWork(func(id int64) error {
		worksMu.Lock()
		defer worksMu.Unlock()
		work := works[id]
		if work == nil {
			return errors.New("work is not running")
		}
		work.cancel(agent.AbortKilledByPlanner)
		return nil
	})
	planner.SetSteerWork(func(id int64, message string) error {
		worksMu.Lock()
		defer worksMu.Unlock()
		work := works[id]
		if work == nil {
			return errors.New("work is not running")
		}
		work.steer = append(work.steer, message)
		return nil
	})
	completed := make(chan taskWorkEvent, task.WorkerCount*2)
	type workerSlot struct {
		name, profile     string
		busy, unavailable bool
		lastNotice        time.Time
	}
	slots := make([]workerSlot, task.WorkerCount)
	slotProfiles := make(map[string]string, task.WorkerCount)
	for i := range slots {
		slots[i].name = fmt.Sprintf("work#%d", i+1)
		if len(task.WorkerProfiles) > 0 {
			slots[i].profile = task.WorkerProfiles[i]
		}
		slotProfiles[slots[i].name] = slots[i].profile
	}
	slotRetry := time.NewTicker(1500 * time.Millisecond)
	defer slotRetry.Stop()
	findingWake := make(chan agent.TriggerEvent, 32)
	humanWake := make(chan agent.TriggerEvent, 32)
	chatSession = &taskChatSession{
		task: task, ctx: taskCtx, db: sqlDB, graph: graph, provider: provider, model: model, userRoot: userRoot,
		transcript: store, works: works, worksMu: &worksMu, wake: humanWake, emit: emit,
		chatFinished: make(chan struct{}, 1),
	}
	r.registerTaskChat(chatSession)
	controlWG.Add(1)
	go func() {
		defer controlWG.Done()
		pollIntentPauses(taskCtx, sqlDB, task, &worksMu, works)
	}()
	heartbeatSeconds := max(task.PlanHeartbeatSeconds, 600)
	heartbeat := time.NewTicker(time.Duration(heartbeatSeconds) * time.Second)
	defer heartbeat.Stop()
	var deadlineC, hardDeadlineC <-chan time.Time
	settling := false
	if !task.Deadline.IsZero() {
		deadline := time.NewTimer(time.Until(task.Deadline))
		defer deadline.Stop()
		hardDeadline := time.NewTimer(time.Until(task.Deadline.Add(30 * time.Second)))
		defer hardDeadline.Stop()
		deadlineC, hardDeadlineC = deadline.C, hardDeadline.C
		settling = time.Now().After(task.Deadline)
	}
	// ARTEX starts existing intents before requesting another planning round.
	// This matters when an interrupted intent is resumed after all goals were met:
	// planning it again could otherwise finish the task before the worker runs.
	activeIntent, err := graph.HasActiveIntent()
	if err != nil {
		return TaskOutcome{Status: "failed", Reason: "无法读取任务意图"}, err
	}
	pendingHuman, err := hasHumanTaskEvents(taskCtx, sqlDB, task)
	if err != nil {
		return TaskOutcome{Status: "paused", Reason: "无法读取任务目标变更"}, err
	}
	planPending := !activeIntent || pendingHuman
	var triggers []agent.TriggerEvent
	active := 0
	finishMainAgent := func() (bool, error) {
		if chatSession == nil {
			return false, nil
		}
		chatSession.beginFinish()
		for chatSession.activeChatCount() > 0 {
			select {
			case <-chatSession.chatFinished:
			case <-taskCtx.Done():
				return false, taskCtx.Err()
			}
		}
		if err := taskCtx.Err(); err != nil {
			return false, err
		}
		changed := planPending || len(triggers) > 0
		for {
			select {
			case event := <-humanWake:
				triggers = append(triggers, event)
				changed = true
			default:
				if changed {
					chatSession.reopen()
					planPending = true
				}
				return changed, nil
			}
		}
	}
	var detachedRunning atomic.Int64
	startWork := func(intent *artexdb.Node, slotID int, slotName, profileID string, message *taskIntentMessage) {
		workCtx, cancel := context.WithCancelCause(WithPinnedWorkerProfile(ctx, profileID))
		if slotID >= 0 {
			slots[slotID].busy = true
		}
		worksMu.Lock()
		works[intent.ID] = &taskWork{cancel: cancel}
		worksMu.Unlock()
		if slotID >= 0 {
			active++
		} else {
			detachedRunning.Add(1)
		}
		workWG.Add(1)
		go func(intent *artexdb.Node, slotID int, slotName, profileID string, message *taskIntentMessage) {
			defer workWG.Done()
			defer cancel(nil)
			if workerConfig.RunSeconds > 0 {
				var stop context.CancelFunc
				workCtx, stop = context.WithTimeout(workCtx, time.Duration(workerConfig.RunSeconds)*time.Second+30*time.Second)
				defer stop()
			}
			workerTurns := task.MaxTurns
			if definitions["worker"].RuntimeConfigured || workerConfig.MaxTurns > 0 {
				workerTurns = workerConfig.MaxTurns
			}
			selectedProvider, selectedModel := workerProvider, workerModel
			if profileID != "" {
				selectedProvider = roleProvider{ctx: workCtx, resolve: r.providers, user: task.UserID, role: "worker"}
				_, selectedModel, _, _ = r.providers(workCtx, task.UserID, "worker")
			}
			worker := agent.NewWorker(selectedProvider, selectedModel, filepath.Join(userRoot, "work"), store, ModelOptions(selectedProvider).ContextWindow, workerTurns)
			worker.SetCompactionWindowResolver(func() int { return ModelOptions(selectedProvider).ContextWindow })
			worker.SetNonStreaming(func() bool { return ModelOptions(selectedProvider).NonStreaming })
			worker.SetMaxTokens(func() int { return ModelOptions(selectedProvider).MaxTokens })
			worker.SetNoaEnabled(func() bool { return ModelOptions(selectedProvider).NoaCompaction })
			worker.SetConstraintInject(func() bool { return r.constraintsEnabled(workCtx, task.UserID, "worker") })
			worker.SetRunTimeout(time.Duration(workerConfig.RunSeconds) * time.Second)
			hooks := &taskSteerHooks{drain: func() string {
				worksMu.Lock()
				defer worksMu.Unlock()
				work := works[intent.ID]
				if work == nil || len(work.steer) == 0 {
					return ""
				}
				message := strings.Join(work.steer, "\n")
				work.steer = nil
				return message
			}}
			runWorker := func() (harness.TerminalReason, error) {
				if err := guardTaskTranscript(workCtx, sqlDB, store, agent.WorkerSessionID(task.ExplorationID, intent.ID),
					`SELECT EXISTS(SELECT 1 FROM activity WHERE exploration_id=$1 AND node_id=$2 AND kind IN ('tool_use','tool_result','text','thinking','result'))`, task.ExplorationID, intent.ID); err != nil {
					emit(artexdb.Activity{Worker: slotName, NodeID: &intent.ID, Kind: "result", IsError: true, Summary: err.Error(), Detail: err.Error()})
					return harness.ReasonPersistenceError, err
				}
				notify := func(id int64, detail string) {
					select {
					case findingWake <- agent.TriggerEvent{Kind: "finding", IntentID: id, Detail: detail}:
					default:
					}
				}
				if message != nil {
					reason, _, err := worker.ExecuteWithMessage(workCtx, slotName, task.ID, nil, graph, intent, hooks, emit, nil, notify, message.RequestID, message.ModelText)
					return reason, err
				}
				reason, _, err := worker.Execute(workCtx, slotName, task.ID, nil, graph, intent, hooks, emit, nil, notify)
				return reason, err
			}
			retryRule := ModelOptions(selectedProvider).IntentRetry
			retries := 2
			if retryRule.Attempts != 0 {
				retries = max(0, retryRule.Attempts)
			}
			delay := 3 * time.Second
			if retryRule.Interval() > 0 {
				delay = retryRule.Interval()
			}
			var reason harness.TerminalReason
			var err error
			for attempt := 0; ; attempt++ {
				reason, err = runWorker()
				if errors.Is(err, agent.ErrRunBudget) || reason != harness.ReasonModelError || workCtx.Err() != nil || attempt >= retries || errors.Is(err, llmpool.ErrExhausted) || err != nil && agent.IsQuotaExhaustedMessage(err.Error()) {
					break
				}
				emit(artexdb.Activity{Kind: "note", Summary: fmt.Sprintf("模型调用失败，重跑意图 (%d/%d)", attempt+1, retries)})
				timer := time.NewTimer(delay)
				select {
				case <-workCtx.Done():
					timer.Stop()
				case <-timer.C:
				}
				if workCtx.Err() != nil {
					break
				}
			}
			if message != nil && taskCtx.Err() == nil {
				recorded, recordErr := intentMessageRecorded(store, task.ExplorationID, intent.ID, message.RequestID)
				if recordErr == nil && recorded {
					recordErr = finishIntentMessage(taskCtx, sqlDB, message.ID)
				} else if recordErr == nil && workCtx.Err() == nil {
					recordErr = errors.New("worker message was not recorded in its transcript")
				}
				if recordErr != nil {
					cancelTask(recordErr)
				}
			}
			// Serialize the cancellation decision with the poller: a pause
			// arriving during settlement must not be acknowledged after a
			// completed state was chosen.
			worksMu.Lock()
			state := "done"
			switch {
			case errors.Is(context.Cause(workCtx), agent.AbortWorkPausedByUser):
				state = "paused"
			case errors.Is(context.Cause(workCtx), agent.AbortKilledByPlanner):
				state = "stopped"
			case errors.Is(workCtx.Err(), context.DeadlineExceeded):
				state = "exhausted"
			case reason == harness.ReasonRunBudget || errors.Is(err, agent.ErrRunBudget):
				state = "exhausted"
			case err != nil:
				state = "blocked"
			case reason == harness.ReasonMaxTurns || reason == harness.ReasonTimeout:
				state = "exhausted"
			}
			work := works[intent.ID]
			var settleErr error
			if taskCtx.Err() == nil {
				var changed bool
				changed, settleErr = graph.CompareAndSetIntentState(intent.ID, "running", state)
				if settleErr == nil && !changed {
					settleErr = errors.New("intent state changed before worker settlement")
				}
				if work != nil && work.pauseRequest != "" {
					status := "conflict"
					if settleErr == nil && state == "paused" {
						status = "paused"
					}
					if controlErr := finishIntentPause(taskCtx, sqlDB, work.pauseRequest, status); controlErr != nil && settleErr == nil {
						settleErr = controlErr
					}
				}
			}
			delete(works, intent.ID)
			worksMu.Unlock()
			if settleErr != nil {
				cancelTask(settleErr)
			}
			if slotID < 0 {
				defer detachedRunning.Add(-1)
			}
			select {
			case completed <- taskWorkEvent{intent: intent.ID, detail: state, slot: slotID}:
			case <-taskCtx.Done():
			}
		}(intent, slotID, slotName, profileID, message)
	}
	// Human messages use a separate Worker, just as ARTEX does, so they are not
	// delayed by a full pool or by a planner model call. The durable message row
	// also lets a later execution resume after a process or lease interruption.
	controlWG.Add(1)
	go func() {
		defer controlWG.Done()
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-taskCtx.Done():
				return
			case <-ticker.C:
			}
			if !task.Deadline.IsZero() && !time.Now().Before(task.Deadline) {
				continue
			}
			messages, err := pendingIntentMessages(taskCtx, sqlDB, task)
			if err != nil {
				continue
			}
			for _, message := range messages {
				profileID := slotProfiles[message.Worker]
				if profileID != "" {
					if r.providers == nil {
						continue
					}
					probe := WithPinnedWorkerProfile(ctx, profileID)
					selected, _, ok, err := r.providers(probe, task.UserID, "worker")
					if err != nil || !ok || selected == nil {
						continue
					}
				}
				intent, err := graph.GetNode(message.IntentID)
				if err != nil || intent == nil {
					continue
				}
				claimed, err := graph.ClaimIntent(intent.ID, "chat")
				if err != nil || !claimed {
					continue
				}
				if err = prepareIntentMessage(taskCtx, sqlDB, task, message); err != nil {
					_, _ = graph.CompareAndSetIntentState(intent.ID, "running", "open")
					cancelTask(err)
					return
				}
				startWork(intent, -1, "chat", profileID, &message)
			}
		}
	}()
	dispatch := func() error {
		if settling || active >= task.WorkerCount {
			return nil
		}
		available := []int{}
		for i := range slots {
			slot := &slots[i]
			if slot.busy {
				continue
			}
			if slot.profile != "" {
				if r.providers == nil {
					continue
				}
				probe := WithPinnedWorkerProfile(ctx, slot.profile)
				selected, _, ok, err := r.providers(probe, task.UserID, "worker")
				if err != nil || !ok || selected == nil {
					if !slot.unavailable || time.Since(slot.lastNotice) >= 5*time.Minute {
						emit(artexdb.Activity{Worker: "system", Kind: "text", Summary: slot.name + " 绑定的模型配置不可用，暂不领取意图"})
						slot.lastNotice = time.Now()
					}
					slot.unavailable = true
					continue
				}
				if slot.unavailable {
					emit(artexdb.Activity{Worker: "system", Kind: "text", Summary: slot.name + " 绑定的模型配置已恢复"})
					slot.unavailable = false
				}
			}
			available = append(available, i)
		}
		if len(available) == 0 {
			return nil
		}
		frontier, err := regularTaskIntents(taskCtx, sqlDB, task.ExplorationID, len(available))
		if err != nil {
			return err
		}
		for index, intentID := range frontier {
			slotID := available[index]
			slot := &slots[slotID]
			intent, err := graph.GetNode(intentID)
			if err != nil {
				return err
			}
			if intent == nil {
				continue
			}
			claimed, err := graph.ClaimIntent(intent.ID, slot.name)
			if err != nil {
				return err
			}
			if !claimed {
				continue
			}
			startWork(intent, slotID, slot.name, slot.profile, nil)
		}
		return nil
	}
	planResults := make(chan taskPlanResult, 1)
	planning := false
	var finishedPlan *taskPlanResult
	for {
		if taskCtx.Err() != nil {
			if errors.Is(context.Cause(taskCtx), agent.AbortRunHardTimeout) {
				return TaskOutcome{Status: "timeout", Reason: "任务收尾预算已用尽"}, nil
			}
			writeMu.Lock()
			err := writeErr
			writeMu.Unlock()
			if err != nil {
				return TaskOutcome{Status: "failed", Reason: "任务活动写入失败"}, err
			}
			return TaskOutcome{Status: "paused", Reason: "任务执行已中断"}, taskCtx.Err()
		}
		if finishedPlan != nil {
			result := *finishedPlan
			finishedPlan = nil
			met, reason, err := result.met, result.reason, result.err
			if err != nil {
				if errors.Is(err, agent.ErrRunBudget) {
					return TaskOutcome{Status: "paused", Reason: agent.ErrRunBudget.Error()}, err
				}
				if errors.Is(err, ErrTaskModelChainExhausted) {
					return TaskOutcome{Status: "paused", Reason: "任务模型链额度已耗尽，请更换或重置模型后恢复"}, err
				}
				return TaskOutcome{Status: "failed", Reason: "规划者调用失败"}, err
			}
			if err = acknowledgeHumanTaskEvents(ctx, sqlDB, task, result.human); err != nil {
				return TaskOutcome{Status: "paused", Reason: "无法保存任务目标变更状态"}, err
			}
			if met && !planPending {
				changed, finishErr := finishMainAgent()
				if finishErr != nil {
					if errors.Is(context.Cause(taskCtx), agent.AbortRunHardTimeout) {
						return TaskOutcome{Status: "timeout", Reason: "任务收尾预算已用尽"}, nil
					}
					return TaskOutcome{Status: "paused", Reason: "任务执行已中断"}, finishErr
				}
				if changed {
					continue
				}
				pending, checkErr := hasHumanTaskEvents(taskCtx, sqlDB, task)
				if checkErr != nil {
					return TaskOutcome{Status: "paused", Reason: "无法检查任务目标变更"}, checkErr
				}
				if pending {
					chatSession.reopen()
					planPending = true
					continue
				}
				return TaskOutcome{Status: "done", Reason: reason}, nil
			}
			if result.settling {
				return TaskOutcome{Status: "timeout", Reason: "任务时间预算已用尽"}, nil
			}
			if !task.Deadline.IsZero() && time.Now().After(task.Deadline) {
				settling = true
				planPending = true
				continue
			}
		}
		if planPending && !planning && (!settling || active == 0 && detachedRunning.Load() == 0) {
			planCtx := ctx
			if settling {
				planCtx = agent.WithTaskClock(ctx, agent.TaskClock{DeadlineUnix: task.Deadline.Unix(), Final: true})
			}
			humanEvents, err := readHumanTaskEvents(planCtx, sqlDB, task)
			if err != nil {
				return TaskOutcome{Status: "paused", Reason: "无法读取任务目标变更"}, err
			}
			// Reopened work is dispatched before another planner round, matching
			// ARTEX's independent worker loop. The event also fences the race
			// between a human rerun and task completion.
			var reopened, planningEvents []humanTaskEvent
			for _, event := range humanEvents {
				if event.event.Kind == "intent_reopened" {
					reopened = append(reopened, event)
				} else {
					planningEvents = append(planningEvents, event)
				}
			}
			if len(reopened) > 0 && settling {
				// Do not acknowledge work that the deadline prevents us from
				// dispatching. FinishTask will requeue it with a fresh deadline.
				return TaskOutcome{Status: "timeout", Reason: "任务时间预算已用尽"}, nil
			}
			if len(reopened) > 0 && !settling {
				if err = dispatch(); err != nil {
					return TaskOutcome{Status: "failed", Reason: "意图调度失败"}, err
				}
				if err = acknowledgeHumanTaskEvents(planCtx, sqlDB, task, reopened); err != nil {
					return TaskOutcome{Status: "paused", Reason: "无法保存意图重跑状态"}, err
				}
				humanEvents = planningEvents
				if len(humanEvents) == 0 && len(triggers) == 0 {
					planPending = false
					continue
				}
			}
			// ARTEX does not call the planner when there are no open goals. It
			// drains notifications and lets directly submitted intents finish.
			// A later goal change remains pending and is observed by the next poll
			// (or by the atomic completion guard in FinishTask).
			openGoal, err := graph.HasOpenGoal()
			if err != nil {
				return TaskOutcome{Status: "failed", Reason: "无法读取任务目标"}, err
			}
			if !openGoal && !settling {
				if err = acknowledgeHumanTaskEvents(planCtx, sqlDB, task, humanEvents); err != nil {
					return TaskOutcome{Status: "paused", Reason: "无法保存任务目标变更状态"}, err
				}
				triggers = nil
				planPending = false
			} else {
				for _, event := range humanEvents {
					triggers = append(triggers, event.event)
				}
				var round int64
				if err = sqlDB.QueryRowContext(planCtx, `SELECT round_no+1 FROM tianqiong_ai.explorations WHERE id=$1`, task.ExplorationID).Scan(&round); err != nil {
					return TaskOutcome{Status: "paused", Reason: "无法读取规划轮次"}, err
				}
				emit(artexdb.Activity{Worker: "planner", Kind: "round", Summary: fmt.Sprintf("第 %d 轮规划", round)})
				if taskCtx.Err() != nil {
					return TaskOutcome{Status: "paused", Reason: "任务执行已中断"}, taskCtx.Err()
				}
				// Keep the coordinator responsive while the planner waits on a model
				// or tool. Only this loop owns slot state and completion decisions.
				planning = true
				planPending = false
				batch := append([]agent.TriggerEvent(nil), triggers...)
				triggers = nil
				controlWG.Add(1)
				go func(planCtx context.Context, events []humanTaskEvent, final bool) {
					defer controlWG.Done()
					met, reason, err := planner.Plan(planCtx, task.ID, nil, graph, task.Goal, batch, emit)
					select {
					case planResults <- taskPlanResult{met: met, reason: reason, err: err, human: events, settling: final}:
					case <-taskCtx.Done():
					}
				}(planCtx, append([]humanTaskEvent(nil), humanEvents...), settling)
			}
		}
		if err := dispatch(); err != nil {
			return TaskOutcome{Status: "failed", Reason: "意图调度失败"}, err
		}
		if active == 0 && detachedRunning.Load() == 0 && !planning && !planPending {
			open, err := graph.HasOpenGoal()
			if err != nil {
				return TaskOutcome{Status: "failed"}, err
			}
			pending, err := graph.HasActiveIntent()
			if err != nil {
				return TaskOutcome{Status: "failed"}, err
			}
			if !open && !pending {
				changed, finishErr := finishMainAgent()
				if finishErr != nil {
					if errors.Is(context.Cause(taskCtx), agent.AbortRunHardTimeout) {
						return TaskOutcome{Status: "timeout", Reason: "任务收尾预算已用尽"}, nil
					}
					return TaskOutcome{Status: "paused", Reason: "任务执行已中断"}, finishErr
				}
				if changed {
					continue
				}
				pending, checkErr := hasHumanTaskEvents(taskCtx, sqlDB, task)
				if checkErr != nil {
					return TaskOutcome{Status: "paused", Reason: "无法检查任务目标变更"}, checkErr
				}
				if pending {
					chatSession.reopen()
					planPending = true
					continue
				}
				return TaskOutcome{Status: "done"}, nil
			}
		}
		select {
		case <-taskCtx.Done():
			continue
		case result := <-planResults:
			planning = false
			finishedPlan = &result
		case event := <-completed:
			if event.slot >= 0 {
				active--
			}
			if event.slot >= 0 {
				slots[event.slot].busy = false
			}
			if event.detail != "paused" {
				triggers = append(triggers, agent.TriggerEvent{Kind: "done", IntentID: event.intent, Detail: event.detail})
				planPending = true
			}
		case event := <-findingWake:
			triggers = append(triggers, event)
			planPending = true
		case event := <-humanWake:
			triggers = append(triggers, event)
			planPending = true
		case <-heartbeat.C:
			planPending = true
		case <-slotRetry.C:
			pending, checkErr := hasHumanTaskEvents(taskCtx, sqlDB, task)
			if checkErr != nil {
				return TaskOutcome{Status: "paused", Reason: "无法检查任务目标变更"}, checkErr
			}
			if pending {
				planPending = true
			}
		case <-deadlineC:
			settling = true
			planPending = true
		case <-hardDeadlineC:
			return TaskOutcome{Status: "timeout", Reason: "任务收尾预算已用尽"}, nil
		}
	}
}

func taskDeadlineUnix(deadline time.Time) int64 {
	if deadline.IsZero() {
		return 0
	}
	return deadline.Unix()
}

type taskSteerHooks struct {
	drain       func() string
	emptyNudges int
}

func (h *taskSteerHooks) PreToolUse(_ context.Context, _ string, _ []byte) (bool, string, []byte) {
	if message := h.drain(); message != "" {
		return true, "【规划者实时纠偏】" + message + "\n本次工具调用未执行，请调整后继续。", nil
	}
	return false, "", nil
}
func (h *taskSteerHooks) PostToolUse(context.Context, string, []byte, []byte, bool) {}
func (h *taskSteerHooks) Stop(_ context.Context, messages []llm.Message) (bool, []string, string) {
	if len(messages) > 0 && h.emptyNudges < 2 {
		last := messages[len(messages)-1]
		if last.Role == llm.RoleAssistant && strings.TrimSpace(last.Text()) == "" && len(last.ToolUses()) == 0 {
			h.emptyNudges++
			return false, []string{"请继续执行当前意图并登记已确认事实，不要只输出思考后结束。"}, ""
		}
	}
	return false, nil, ""
}

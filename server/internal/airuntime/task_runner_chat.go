package airuntime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Autumn-27/artex/agent"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/artex/sidequestion"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/skill"
	"github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

var (
	ErrTaskChatUnavailable = errors.New("AI task MainAgent is not active on this instance")
	ErrTaskChatBusy        = errors.New("AI task MainAgent is already handling a message")
	ErrTaskChatInvalid     = errors.New("invalid AI task message")
	ErrTaskChatDisabled    = errors.New("AI task MainAgent is disabled")
)

type taskChatSession struct {
	task       TaskExecution
	ctx        context.Context
	db         *sql.DB
	graph      *artexdb.ExplorationStore
	provider   llm.Provider
	model      string
	userRoot   string
	transcript *transcript.Store
	works      map[int64]*taskWork
	worksMu    *sync.Mutex
	wake       chan<- agent.TriggerEvent
	emit       func(artexdb.Activity)

	mu           sync.Mutex
	chatMu       sync.Mutex
	chatWG       sync.WaitGroup
	closed       bool
	closing      bool
	activeChats  int
	chatFinished chan struct{}
}

func (s *taskChatSession) close() {
	s.mu.Lock()
	s.closed = true
	s.closing = true
	s.mu.Unlock()
	s.chatWG.Wait()
}

func (s *taskChatSession) beginFinish() {
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
}

func (s *taskChatSession) activeChatCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeChats
}

func (s *taskChatSession) reopen() {
	s.mu.Lock()
	if !s.closed {
		s.closing = false
	}
	s.mu.Unlock()
}

func (r *TaskRunner) registerTaskChat(session *taskChatSession) {
	r.chatMu.Lock()
	if r.chats == nil {
		r.chats = make(map[int64]*taskChatSession)
	}
	r.chats[session.task.ID] = session
	r.chatMu.Unlock()
}

func (r *TaskRunner) removeTaskChat(session *taskChatSession) {
	r.chatMu.Lock()
	if r.chats[session.task.ID] == session {
		delete(r.chats, session.task.ID)
	}
	r.chatMu.Unlock()
}

// ChatTask routes one operator message into the active ARTEX MainAgent session.
// The run context preserves the task's execution lease and cancels the chat if
// the task is paused, stopped, or loses its lease.
func (r *TaskRunner) ChatTask(ctx context.Context, task TaskExecution, message string) (string, error) {
	message = strings.TrimSpace(message)
	if ctx == nil || task.ID <= 0 || task.RunID == "" || message == "" || len(message) > 384*1024 {
		return "", ErrTaskChatInvalid
	}
	r.chatMu.RLock()
	session := r.chats[task.ID]
	r.chatMu.RUnlock()
	if session == nil || session.task.RunID != task.RunID || session.task.ExplorationID != task.ExplorationID {
		return "", ErrTaskChatUnavailable
	}
	return r.chatSession(ctx, task, session, message)
}

// ChatIndependent opens its own graph connection and transcript so that a
// MainAgent turn can outlive a planner/worker run without acquiring its lease.
func (r *TaskRunner) ChatIndependent(ctx context.Context, task TaskExecution, provider llm.Provider, model, message string) (string, error) {
	message = strings.TrimSpace(message)
	if r == nil || r.connector == nil || ctx == nil || task.ID <= 0 || task.ExplorationID <= 0 || task.UserID <= 0 ||
		!canonicalUUID(task.TenantID) || !canonicalUUID(task.RunID) || task.ExecutionMode != "main_chat" ||
		!filepath.IsAbs(r.root) || message == "" || len(message) > 384*1024 {
		return "", ErrTaskChatInvalid
	}
	budgetCtx, budgetErr := withModelRunBudget(ctx, task.UserID, r.modelBudget)
	if budgetErr != nil {
		return "", budgetErr
	}
	ctx = budgetCtx
	ctx = WithProcessGovernor(ctx, r.governor, task.UserID)
	permitCtx, release, err := r.governor.Acquire(ctx, task.UserID, "main_chat", true)
	if err != nil {
		return "", err
	}
	defer release()
	ctx = permitCtx

	userRoot := filepath.Join(r.root, task.TenantID, fmt.Sprintf("user-%d", task.UserID))
	if err := os.MkdirAll(userRoot, 0o700); err != nil {
		return "", err
	}
	db, err := r.connector.open(ctx, task)
	if err != nil {
		return "", err
	}
	defer db.Close()
	graph := (&artexdb.DB{DB: db}).Exploration(task.ExplorationID)
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(context.Canceled)
	if r.sideDrain != nil {
		defer r.sideDrain(task.UserID, task.RunID)
	}
	works := map[int64]*taskWork{}
	var worksMu sync.Mutex
	var wake chan<- agent.TriggerEvent
	r.chatMu.RLock()
	active := r.chats[task.ID]
	if active != nil && active.task.ExplorationID == task.ExplorationID {
		works, wake = active.works, active.wake
	}
	r.chatMu.RUnlock()
	sharedMu := &worksMu
	if active != nil && active.task.ExplorationID == task.ExplorationID {
		sharedMu = active.worksMu
	}
	var writeMu sync.Mutex
	var writeErr error
	emit := func(event artexdb.Activity) {
		if _, err := graph.AppendActivity(event); err != nil {
			writeMu.Lock()
			if writeErr == nil {
				writeErr = err
			}
			writeMu.Unlock()
			cancel(err)
		}
	}
	session := &taskChatSession{task: task, ctx: runCtx, db: db, graph: graph, provider: provider, model: model,
		userRoot: userRoot, transcript: transcript.NewStore(filepath.Join(userRoot, "transcripts")),
		works: works, worksMu: sharedMu, wake: wake, emit: emit, chatFinished: make(chan struct{}, 1)}
	reply, err := r.chatSession(ctx, task, session, message)
	if err != nil && runCtx.Err() == nil {
		segment, specified := taskChatSegment(ctx)
		if !specified {
			segment, _ = graph.CurrentMainSeg()
		}
		emit(artexdb.Activity{Worker: "mainagent", Kind: "assistant_message", IsError: true, Summary: "（主 Agent 出错：" + err.Error() + "）", MainSeg: &segment})
	}
	writeMu.Lock()
	persistenceErr := writeErr
	writeMu.Unlock()
	if persistenceErr != nil {
		return "", persistenceErr
	}
	return reply, err
}

func (r *TaskRunner) chatSession(ctx context.Context, task TaskExecution, session *taskChatSession, message string) (string, error) {
	if !session.chatMu.TryLock() {
		return "", ErrTaskChatBusy
	}
	defer session.chatMu.Unlock()
	session.mu.Lock()
	if session.closed || session.closing || session.ctx.Err() != nil {
		session.mu.Unlock()
		return "", ErrTaskChatUnavailable
	}
	session.chatWG.Add(1)
	session.activeChats++
	session.mu.Unlock()
	defer func() {
		session.mu.Lock()
		session.activeChats--
		session.mu.Unlock()
		select {
		case session.chatFinished <- struct{}{}:
		default:
		}
		session.chatWG.Done()
	}()
	segment, specified := taskChatSegment(ctx)
	if !specified {
		var err error
		segment, err = session.graph.CurrentMainSeg()
		if err != nil {
			return "", err
		}
	} else if segment < 0 {
		return "", ErrTaskChatInvalid
	} else if segment > 0 {
		var exists bool
		if err := session.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tianqiong_ai.main_sessions WHERE exploration_id=$1 AND seq=$2)`, session.task.ExplorationID, segment).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return "", ErrTaskChatInvalid
		}
	}
	emit := func(activity artexdb.Activity) {
		if session.emit != nil {
			activity.MainSeg = &segment
			session.emit(activity)
		}
	}

	display := taskChatDisplay(ctx)
	if display == "" {
		display = message
	}
	emit(artexdb.Activity{Worker: "mainagent", Kind: "human_message", Summary: display, Detail: taskChatDetail(ctx)})

	config := DefaultAgentConfig("assistant")
	if r.settings != nil {
		var err error
		config, err = r.settings(ctx, task.UserID, "assistant")
		if err != nil {
			return "", err
		}
	}
	var definition ChatAgentDefinition
	if r.agents != nil {
		var err error
		definition, err = r.agents(ctx, task.UserID, "mainagent")
		if err != nil {
			return "", err
		}
		config.Enabled = definition.Enabled
		if definition.RuntimeConfigured {
			config.MaxTurns, config.RunSeconds = definition.MaxTurns, definition.RunSeconds
		}
	}
	if !config.Enabled {
		return "", ErrTaskChatDisabled
	}
	var skills []skill.Skill
	if r.skills != nil {
		var err error
		skills, err = r.skills(ctx, task.UserID, "mainagent")
		if err != nil {
			return "", err
		}
	}

	var remaining time.Duration
	if !session.task.Deadline.IsZero() {
		remaining = time.Until(session.task.Deadline)
		if remaining <= 0 {
			return "", ErrTaskChatUnavailable
		}
	}
	if config.RunSeconds > 0 && (remaining == 0 || time.Duration(config.RunSeconds)*time.Second < remaining) {
		remaining = time.Duration(config.RunSeconds) * time.Second
	}
	runCtx, cancelRun := context.WithCancelCause(session.ctx)
	stopRequest := context.AfterFunc(ctx, func() { cancelRun(context.Cause(ctx)) })
	defer func() {
		stopRequest()
		cancelRun(context.Canceled)
	}()
	chatCtx := context.Context(runCtx)
	cancelTimeout := func() {}
	if remaining > 0 {
		chatCtx, cancelTimeout = context.WithTimeout(runCtx, remaining)
	}
	defer cancelTimeout()
	var capture *TrafficAccess
	if r.trafficResolver != nil {
		var err error
		capture, err = r.trafficResolver(chatCtx, session.task)
		if err != nil {
			return "", err
		}
		if capture != nil {
			chatCtx, err = withTrafficCapture(chatCtx, capture)
			if err != nil {
				if capture.Lease != nil {
					capture.Lease.Close()
				}
				return "", err
			}
			defer capture.Lease.Close()
		}
	}
	var local *NativeLocalTools
	workRoot := filepath.Join(session.userRoot, "work")
	chatCtx = withWorkspace(chatCtx, workRoot, filepath.Join(workRoot, "tasks", fmt.Sprint(session.task.ID)))
	if r.localTools {
		var err error
		local, err = NewNativeLocalTools(chatCtx, filepath.Join(session.userRoot, "work"))
		if err != nil {
			return "", err
		}
		defer local.Close()
	}
	chatCtx = withExecutionID(chatCtx, session.task.RunID)
	background := taskChatDisplay(ctx)
	if background == "" {
		background = message
	}
	chatCtx = intercept.WithReviewContext(chatCtx, "", intercept.ReviewBackground{Source: intercept.BackgroundUserMessage, Text: background})
	chatCtx = sidequestion.WithParent(chatCtx, sidequestion.Parent{TaskID: session.task.ID, ExplorationID: session.task.ExplorationID, MainSeg: segment})
	if r.sidePublisher != nil {
		publishCtx := chatCtx
		chatCtx = sidequestion.WithPublisher(chatCtx, func(snapshot sidequestion.Snapshot) { r.sidePublisher(publishCtx, session.task, snapshot) })
	}
	chatCtx = WithTaskExecution(chatCtx, session.task)
	chatCtx = withTaskTarget(chatCtx, session.task.TargetID)
	chatCtx = agent.WithTaskClock(chatCtx, agent.TaskClock{DeadlineUnix: taskDeadlineUnix(session.task.Deadline)})
	chatCtx = agent.WithHostPolicy(chatCtx, agent.HostPolicy{
		ExternalBackgroundTasks: r.localTools,
		AllowWebFetch:           false,
		CheckAssetIDs: func(ctx context.Context, ids []int64) error {
			if r.assetIDsGate == nil {
				return nil
			}
			return r.assetIDsGate(ctx, session.task, ids)
		},
		IntelligenceEnabled: func(ctx context.Context) bool {
			return r.intelligenceEnabled != nil && r.intelligenceEnabled(ctx, task.UserID)
		},
		IntelligenceRecord: r.intelligenceRecord,
		FindingTrafficEnabled: func(ctx context.Context) bool {
			return r.findingTrafficEnabled != nil && r.findingTrafficEnabled(ctx, task.UserID)
		},
		FindingTrafficRecord: r.findingTrafficRecord,
		DecorateTool: func(ctx context.Context, key string, item tool.CoreTool) tool.CoreTool {
			if r.approvalWrapper == nil {
				return item
			}
			info := agent.RunInfoFrom(ctx)
			return r.approvalWrapper(ctx, task.UserID, key, info, func(event Activity) {
				emit(artexdb.Activity{Worker: "mainagent", Kind: event.Kind, Tool: event.Tool, ToolUseID: event.ToolUseID, IsError: event.IsError, Summary: event.Summary, Detail: event.Detail})
			}, item)
		},
		Wrapup: func(key string) agent.WrapupPolicy {
			if key == "mainagent" {
				return AgentWrapupPolicy(definition)
			}
			return agent.WrapupPolicy{}
		},
		PromptTemplate: func(key string) (string, bool) {
			return definition.PromptTemplate, key == "mainagent" && definition.PromptTemplate != ""
		},
		TransformTools: func(ctx context.Context, key string, base []tool.CoreTool) []tool.CoreTool {
			if key != "mainagent" {
				return nil
			}
			allowed := make([]tool.CoreTool, 0, len(base)+8)
			for _, item := range base {
				if taskGraphTools[item.Name()] {
					allowed = append(allowed, item)
				}
			}
			if r.tools != nil {
				allowed = append(allowed, independentChatTools(FilterAgentTools(config, r.tools(task.UserID)))...)
			}
			assetTools := []tool.CoreTool{taskListAssetsTool(session.db, session.task)}
			if taskCoverageEnabled(session.task) {
				assetTools = append(assetTools, taskUntestedAssetsTool(session.db, session.task))
			}
			allowed = append(allowed, FilterAgentTools(config, assetTools)...)
			allowed = append(allowed, scopedTrafficTools(capture)...)
			allowed = AppendSkills(allowed, skills)
			if local != nil {
				allowed = append(allowed, local.Tools(definition.Options.InteractiveShell)...)
			}
			if r.toolLayer != nil {
				resolved, err := r.toolLayer(ctx, task.UserID, key, allowed)
				if err != nil {
					cancelRun(err)
					return nil
				}
				allowed = resolved
			}
			return allowed
		},
		AdditionalSystemPrompt: func(key string) string {
			if key == "mainagent" {
				return config.AdditionalPrompt
			}
			return ""
		},
		AugmentTools: func(ctx context.Context, key string) ([]tool.CoreTool, agent.DeferredInfo, func()) {
			if r.mcp == nil || key != "mainagent" {
				return nil, agent.DeferredInfo{}, nil
			}
			return r.mcp(ctx, task.UserID, "mainagent")
		},
	})

	provider, model := session.provider, session.model
	if r.providers != nil {
		_, resolvedModel, ok, err := r.providers(chatCtx, task.UserID, "mainagent")
		if err != nil {
			return "", err
		}
		if !ok {
			return "", errors.New("AI MainAgent model is not configured")
		}
		provider, model = roleProvider{ctx: chatCtx, resolve: r.providers, user: task.UserID, role: "mainagent"}, resolvedModel
	}
	if provider == nil {
		return "", errors.New("AI MainAgent model is not configured")
	}
	main := agent.NewMainAgent(provider, model, filepath.Join(session.userRoot, "work"), session.transcript, ModelOptions(provider).ContextWindow, config.MaxTurns)
	main.SetNonStreaming(func() bool { return ModelOptions(provider).NonStreaming })
	main.SetMaxTokens(func() int { return ModelOptions(provider).MaxTokens })
	main.SetNoaEnabled(func() bool { return ModelOptions(provider).NoaCompaction })
	main.SetSteerWork(func(intentID int64, message string) error {
		session.worksMu.Lock()
		defer session.worksMu.Unlock()
		work := session.works[intentID]
		if work == nil {
			return errors.New("work is not running")
		}
		work.steer = append(work.steer, message)
		return nil
	})
	wake := func(event agent.TriggerEvent) {
		if session.task.ExecutionMode == "main_chat" {
			return
		}
		select {
		case session.wake <- event:
		default:
		}
	}
	transcriptID := fmt.Sprintf("exp%d-main", task.ExplorationID)
	if segment > 0 {
		transcriptID = fmt.Sprintf("exp%d-main-s%d", task.ExplorationID, segment)
	}
	if err := guardTaskTranscript(chatCtx, session.db, session.transcript, transcriptID,
		`SELECT EXISTS(SELECT 1 FROM activity WHERE exploration_id=$1 AND worker='mainagent' AND COALESCE(main_seg,0)=$2 AND kind IN ('assistant_message','thinking','text','tool_use','tool_result','result')) OR (SELECT COUNT(*) FROM activity WHERE exploration_id=$1 AND worker='mainagent' AND COALESCE(main_seg,0)=$2 AND kind='human_message')>1`, task.ExplorationID, segment); err != nil {
		return "", err
	}
	reply, err := main.Chat(chatCtx, session.task.ID, segment, nil, session.graph, session.task.Goal, message,
		emit,
		func() { wake(agent.TriggerEvent{Kind: "human_update", Detail: "操作员更新了任务图"}) },
		func() { wake(agent.TriggerEvent{Kind: "human_goal", Detail: "操作员更新了任务目标"}) },
		func(values []string) {
			wake(agent.TriggerEvent{Kind: "human_goal", Detail: strings.Join(values, "\n")})
		},
		func(values []string) {
			wake(agent.TriggerEvent{Kind: "human_hint", Detail: strings.Join(values, "\n")})
		})
	if err != nil {
		if session.ctx.Err() != nil {
			return "", ErrTaskChatUnavailable
		}
		return "", err
	}
	emit(artexdb.Activity{Worker: "mainagent", Kind: "assistant_message", Summary: reply})
	return reply, nil
}

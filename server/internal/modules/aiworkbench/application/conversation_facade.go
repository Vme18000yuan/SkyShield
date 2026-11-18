package application

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/sidequestion"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/skill"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"github.com/yyhuni/lunafox/server/internal/pkg"
	"go.uber.org/zap"
)

type ChatRunner interface {
	Run(context.Context, airuntime.ChatRun, func(airuntime.Activity)) (string, error)
}

type ProviderResolver func(context.Context, int) (llm.Provider, string, bool, error)
type ProfileProviderResolver func(context.Context, int, string) (llm.Provider, string, bool, error)
type ToolResolver func(int) []tool.CoreTool

type manualChatTimingKey struct{}

type ConversationFacade struct {
	chatBudget            airuntime.ModelRunBudgetResolver
	governor              *airuntime.ExecutionGovernor
	localTools            bool
	toolLayer             airuntime.ToolLayerResolver
	approvalWrapper       airuntime.ApprovalToolWrapper
	trafficResolver       airuntime.ConversationTrafficResolver
	findingTrafficEnabled func(context.Context, int) bool
	mentions              *ChatMentionFacade
	attachments           *AttachmentFacade
	sideQuestions         *SideQuestionFacade
	store                 ConversationStore
	runner                ChatRunner
	provider              ProviderResolver
	profileProvider       ProfileProviderResolver
	tools                 ToolResolver
	autoTools             ToolResolver
	settings              airuntime.AgentConfigResolver
	skills                airuntime.SkillResolver
	mcp                   airuntime.MCPAugmenter
	agentCatalog          AgentCatalogResolver
	failureRecovery       func(context.Context, int, string) (string, error)
	instanceID            string
	rootCtx               context.Context
	cancelRun             sync.Map
	workers               sync.WaitGroup
	lifecycleMu           sync.Mutex
	closing               bool
	startOnce             sync.Once
	done                  chan struct{}
}

func (f *ConversationFacade) SetExecutionGovernor(g *airuntime.ExecutionGovernor) { f.governor = g }
func (f *ConversationFacade) SetChatBudgetResolver(resolve airuntime.ModelRunBudgetResolver) {
	f.chatBudget = resolve
}

// SetSkillResolver is configured by the host before Start.
func (f *ConversationFacade) SetSkillResolver(resolver airuntime.SkillResolver) { f.skills = resolver }
func (f *ConversationFacade) SetFailureRecovery(recover func(context.Context, int, string) (string, error)) {
	f.failureRecovery = recover
}
func (f *ConversationFacade) SetMCPAugmenter(augmenter airuntime.MCPAugmenter) { f.mcp = augmenter }
func (f *ConversationFacade) SetAgentCatalogResolver(resolver AgentCatalogResolver) {
	f.agentCatalog = resolver
}
func (f *ConversationFacade) SetProfileProviderResolver(resolver ProfileProviderResolver) {
	f.profileProvider = resolver
}
func (f *ConversationFacade) SetApprovalWrapper(wrapper airuntime.ApprovalToolWrapper) {
	f.approvalWrapper = wrapper
}
func (f *ConversationFacade) SetFindingTrafficEnabled(resolve func(context.Context, int) bool) {
	f.findingTrafficEnabled = resolve
}
func (f *ConversationFacade) SetTrafficResolver(resolve airuntime.ConversationTrafficResolver) {
	f.trafficResolver = resolve
}
func (f *ConversationFacade) SetAutoTools(resolver ToolResolver) { f.autoTools = resolver }
func (f *ConversationFacade) SetNativeTools(enabled bool, layer airuntime.ToolLayerResolver) {
	f.localTools = enabled
	f.toolLayer = layer
}

func NewConversationFacade(store ConversationStore, runner ChatRunner, provider ProviderResolver, tools ToolResolver, resolvers ...airuntime.AgentConfigResolver) *ConversationFacade {
	facade := &ConversationFacade{store: store, runner: runner, provider: provider, tools: tools, instanceID: uuid.NewString(), done: make(chan struct{})}
	if len(resolvers) > 0 {
		facade.settings = resolvers[0]
	}
	return facade
}

func (f *ConversationFacade) Start(ctx context.Context) {
	f.startOnce.Do(func() {
		f.rootCtx = ctx
		if f.sideQuestions != nil {
			f.sideQuestions.Start(ctx)
		}
		go func() {
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for ctx.Err() == nil {
				if recovery, ok := f.store.(interface {
					RecoverExpiredRuns(context.Context) (int64, error)
				}); ok {
					check, stop := context.WithTimeout(ctx, 5*time.Second)
					_, err := recovery.RecoverExpiredRuns(check)
					stop()
					if err != nil && ctx.Err() == nil {
						pkg.Warn("AI conversation lease recovery failed")
					}
				}
				select {
				case <-ctx.Done():
				case <-ticker.C:
				}
			}
			f.lifecycleMu.Lock()
			f.closing = true
			f.lifecycleMu.Unlock()
			f.workers.Wait()
			if f.sideQuestions != nil {
				f.sideQuestions.Shutdown()
			}
			close(f.done)
		}()
	})
}

func (f *ConversationFacade) Done() <-chan struct{} { return f.done }

func (f *ConversationFacade) Create(ctx context.Context, userID int, title string) (*Conversation, error) {
	return f.CreateWithAgent(ctx, userID, title, "assistant")
}

func (f *ConversationFacade) CreateWithAgent(ctx context.Context, userID int, title, agentKey string) (*Conversation, error) {
	title = strings.TrimSpace(title)
	agentKey = strings.TrimSpace(agentKey)
	if agentKey == "" {
		agentKey = "assistant"
	}
	if userID <= 0 || utf8.RuneCountInString(title) > 200 || len(agentKey) > 64 {
		return nil, ErrInvalidInput
	}
	if title == "" {
		title = "新对话"
	}
	if f.agentCatalog != nil {
		definition, err := f.agentCatalog(ctx, userID, agentKey)
		if err != nil {
			return nil, err
		}
		if !definition.Enabled {
			return nil, ErrAgentDisabled
		}
	}
	if store, ok := f.store.(AgentConversationStore); ok {
		return store.CreateAgentConversation(ctx, userID, uuid.NewString(), title, agentKey)
	}
	if agentKey != "assistant" {
		return nil, ErrInvalidInput
	}
	return f.store.CreateConversation(ctx, userID, uuid.NewString(), title)
}

func (f *ConversationFacade) List(ctx context.Context, userID int) ([]Conversation, error) {
	if userID <= 0 {
		return nil, ErrInvalidInput
	}
	return f.store.ListConversations(ctx, userID)
}

func validConversationID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}

func (f *ConversationFacade) Send(ctx context.Context, userID int, conversationID, message string) (string, error) {
	message = strings.TrimSpace(message)
	if userID <= 0 || !validConversationID(conversationID) || message == "" || len(message) > 64*1024 {
		return "", ErrInvalidInput
	}
	return f.send(ctx, userID, conversationID, message, nil, message)
}

func (f *ConversationFacade) send(ctx context.Context, userID int, conversationID, message string, userActivity *airuntime.Activity, mentionSource string) (string, error) {
	if userID <= 0 || !validConversationID(conversationID) || message == "" {
		return "", ErrInvalidInput
	}
	if f.rootCtx == nil || f.rootCtx.Err() != nil || f.runner == nil || f.provider == nil {
		return "", ErrModelNotConfigured
	}
	agentKey := "assistant"
	if store, ok := f.store.(AgentConversationStore); ok {
		storedAgentKey, err := store.ConversationAgent(ctx, userID, conversationID)
		if err != nil {
			return "", err
		}
		if storedAgentKey != "" {
			agentKey = storedAgentKey
		}
	}
	reviewMode := false
	if marker, ok := f.store.(interface {
		IsFindingCaseReviewConversation(context.Context, int, string) (bool, error)
	}); ok {
		var err error
		reviewMode, err = marker.IsFindingCaseReviewConversation(ctx, userID, conversationID)
		if err != nil {
			return "", err
		}
		if reviewMode && (agentKey != "reporter" || AgentTriggerJobFromContext(ctx) == 0) {
			return "", ErrConversationBusy
		}
	}
	definition := airuntime.ChatAgentDefinition{
		Key: agentKey, Name: "助手", SettingsKey: "assistant", Enabled: true,
		SystemPrompt: "你是天穹攻击面管理平台的安全助手。请基于可用资产和证据回答，不要编造事实。",
	}
	if f.agentCatalog != nil {
		var err error
		definition, err = f.agentCatalog(ctx, userID, agentKey)
		if errors.Is(err, airuntime.ErrChatAgentNotFound) && agentKey != "assistant" {
			// A removed custom Agent may still be referenced by existing conversation
			// history. Let that history continue under the standard assistant.
			agentKey = "assistant"
			definition, err = f.agentCatalog(ctx, userID, agentKey)
		}
		if err != nil {
			return "", err
		}
	}
	settingsKey := definition.SettingsKey
	if settingsKey == "" {
		settingsKey = "assistant"
	}
	config := airuntime.DefaultAgentConfig(settingsKey)
	if f.settings != nil {
		var err error
		config, err = f.settings(ctx, userID, settingsKey)
		if err != nil {
			return "", err
		}
	}
	if definition.RuntimeConfigured {
		config.MaxTurns, config.RunSeconds = definition.MaxTurns, definition.RunSeconds
	} else {
		switch agentKey {
		case "goals", "auto", "pentest":
			config.MaxTurns = definition.MaxTurns
			config.RunSeconds = definition.RunSeconds
		case "mainagent", "planner", "worker", "assistant":
			// These ARTEX roles map to the current product's existing setting rows.
		default:
			config.MaxTurns = definition.MaxTurns
			config.RunSeconds = definition.RunSeconds
		}
	}
	if !definition.Enabled || (f.agentCatalog == nil && !config.Enabled) {
		return "", ErrAgentDisabled
	}
	var skills []skill.Skill
	if f.skills != nil && !reviewMode {
		var err error
		skillAgentKey := agentKey
		if skillAgentKey == "assistant" && f.agentCatalog != nil {
			skillAgentKey = "mainagent"
		}
		skills, err = f.skills(ctx, userID, skillAgentKey)
		if err != nil {
			return "", err
		}
	}
	var provider llm.Provider
	var model string
	var configured bool
	var err error
	profileID := definition.LLMProfileID
	if profileID == nil {
		if store, ok := f.store.(ConversationProfileStore); ok {
			profileID, err = store.ConversationProfile(ctx, userID, conversationID)
			if err != nil {
				return "", err
			}
		}
	}
	if profileID != nil {
		if f.profileProvider == nil {
			return "", ErrModelNotConfigured
		}
		provider, model, configured, err = f.profileProvider(ctx, userID, *profileID)
	} else {
		provider, model, configured, err = f.provider(ctx, userID)
	}
	if err != nil {
		return "", err
	}
	if !configured || provider == nil {
		return "", ErrModelNotConfigured
	}
	agentMessage := message
	// Only explicit user text may contain selected references. Attachments and
	// automated trigger payloads are untrusted data, not reference commands.
	if f.mentions != nil && AgentTriggerJobFromContext(ctx) == 0 {
		enriched, composeErr := f.mentions.Compose(ctx, userID, mentionSource)
		if composeErr != nil {
			return "", composeErr
		}
		agentMessage += strings.TrimPrefix(enriched, mentionSource)
	}
	runID := uuid.NewString()
	admissionCtx, release, admissionErr := f.governor.Acquire(f.rootCtx, userID, "chat", false)
	if admissionErr != nil {
		return "", admissionErr
	}
	transferred := false
	defer func() {
		if !transferred {
			release()
		}
	}()
	begin := func() error { return f.store.BeginRun(ctx, userID, conversationID, runID, f.instanceID, message) }
	if userActivity != nil {
		store, ok := f.store.(interface {
			BeginRunWithActivity(context.Context, int, string, string, string, airuntime.Activity) error
		})
		if !ok {
			return "", ErrInvalidInput
		}
		begin = func() error {
			return store.BeginRunWithActivity(ctx, userID, conversationID, runID, f.instanceID, *userActivity)
		}
	}
	if err := begin(); err != nil {
		return "", err
	}
	runCtx, cancel := context.WithCancel(admissionCtx)
	f.lifecycleMu.Lock()
	if f.closing || f.rootCtx.Err() != nil {
		f.lifecycleMu.Unlock()
		cancel()
		finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer finishCancel()
		_ = f.store.FinishRun(finishCtx, userID, conversationID, runID, f.instanceID, "failed")
		return "", ErrModelNotConfigured
	}
	f.cancelRun.Store(runID, cancel)
	f.workers.Add(1)
	f.lifecycleMu.Unlock()
	background := ""
	if AgentTriggerJobFromContext(ctx) == 0 {
		runCtx = context.WithValue(runCtx, manualChatTimingKey{}, true)
		background = mentionSource
	}
	transferred = true
	go func() {
		defer release()
		f.execute(runCtx, cancel, userID, conversationID, runID, agentKey, definition.SystemPrompt, agentMessage, background, model, provider, config, skills, airuntime.AgentWrapupPolicy(definition), definition.Options.InteractiveShell && !reviewMode, reviewMode)
	}()
	return runID, nil
}

func (f *ConversationFacade) SetProfile(ctx context.Context, userID int, conversationID string, profileID *string) error {
	if userID <= 0 || !validConversationID(conversationID) {
		return ErrInvalidInput
	}
	if profileID != nil && !validConversationID(*profileID) {
		return ErrInvalidInput
	}
	store, ok := f.store.(ConversationProfileStore)
	if !ok {
		return ErrInvalidInput
	}
	return store.SetConversationProfile(ctx, userID, conversationID, profileID)
}

func (f *ConversationFacade) UpdateMetadata(ctx context.Context, userID int, conversationID string, title *string, pinned *bool) error {
	if userID <= 0 || !validConversationID(conversationID) || (title == nil && pinned == nil) {
		return ErrInvalidInput
	}
	if title != nil {
		trimmed := strings.TrimSpace(*title)
		if trimmed == "" || utf8.RuneCountInString(trimmed) > 200 {
			return ErrInvalidInput
		}
		title = &trimmed
	}
	store, ok := f.store.(ConversationMetadataStore)
	if !ok {
		return ErrInvalidInput
	}
	return store.UpdateConversationMetadata(ctx, userID, conversationID, title, pinned)
}

func (f *ConversationFacade) execute(ctx context.Context, cancel context.CancelFunc, userID int, conversationID, runID, agentKey, systemPrompt, message, background, model string, provider llm.Provider, config airuntime.AgentConfig, skills []skill.Skill, wrapup agent.WrapupPolicy, interactive, reviewMode bool) {
	ctx = airuntime.WithProcessGovernor(ctx, f.governor, userID)
	if f.sideQuestions != nil {
		parent := sidequestion.Parent{ConversationKey: conversationID}
		ctx = sidequestion.WithPublisher(ctx, func(snapshot sidequestion.Snapshot) { f.sideQuestions.Publish(ctx, userID, parent, runID, snapshot) })
	}
	defer f.workers.Done()
	defer f.cancelRun.Delete(runID)
	defer cancel()
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				valid, err := f.store.Heartbeat(ctx, userID, conversationID, runID, f.instanceID)
				if err != nil || !valid {
					cancel()
					return
				}
			}
		}
	}()
	var capabilities []tool.CoreTool
	if f.tools != nil {
		capabilities = airuntime.FilterAgentTools(config, f.tools(userID))
	}
	if agentKey == "auto" && f.autoTools != nil {
		capabilities = append(capabilities, f.autoTools(userID)...)
	}
	capabilities = airuntime.AppendSkills(capabilities, skills)
	var activityMu sync.Mutex
	var activityErr error
	var terminalReason string
	prompt := systemPrompt
	if prompt == "" {
		prompt = "你是天穹攻击面管理平台的安全助手。请基于可用资产和证据回答，不要编造事实。"
	}
	manualChat, _ := ctx.Value(manualChatTimingKey{}).(bool)
	_, err := f.runner.Run(ctx, airuntime.ChatRun{
		ObserveToolTiming: manualChat,
		BudgetResolver:    f.chatBudget,
		HistoryRequired: func(ctx context.Context) (bool, error) {
			if store, ok := f.store.(interface {
				RequiresTranscript(context.Context, int, string, string) (bool, error)
			}); ok {
				return store.RequiresTranscript(ctx, userID, conversationID, runID)
			}
			return false, nil
		},
		LocalTools: f.localTools, InteractiveShell: interactive, FindingCaseReview: reviewMode, ToolLayer: f.toolLayer, ApprovalWrapper: f.approvalWrapper, TrafficResolver: f.trafficResolver,
		FindingTrafficEnabled: func(ctx context.Context) bool {
			return f.findingTrafficEnabled != nil && f.findingTrafficEnabled(ctx, userID)
		},
		TenantID: assetapp.DefaultTenantID, UserID: userID, SessionID: conversationID, ExecutionID: runID,
		AgentKey: agentKey, Message: message, UserBackground: background, Model: model, Provider: provider, Tools: capabilities,
		AssetSearchEnabled: &config.SearchAssetsEnabled,
		SystemPrompt:       prompt, AdditionalPrompt: config.AdditionalPrompt,
		Wrapup:   wrapup,
		MaxTurns: config.MaxTurns, MaxDuration: time.Duration(config.RunSeconds) * time.Second,
		MCPAugmenter: func(ctx context.Context, key string) ([]tool.CoreTool, agent.DeferredInfo, func()) {
			if f.mcp == nil || reviewMode {
				return nil, agent.DeferredInfo{}, nil
			}
			if key == "assistant" && f.agentCatalog != nil {
				key = "mainagent"
			}
			return f.mcp(ctx, userID, key)
		},
	}, func(event airuntime.Activity) {
		if event.Kind == "tool_started" {
			// A live timing hint is optional. The result also carries its timestamps.
			if event.ToolTiming != nil {
				if store, ok := f.store.(interface {
					MarkConversationToolStarted(context.Context, int, string, string, string, string, time.Time) error
				}); ok {
					markCtx, stop := context.WithTimeout(ctx, time.Second)
					_ = store.MarkConversationToolStarted(markCtx, userID, conversationID, runID, f.instanceID, event.ToolTiming.Occurrence, event.ToolTiming.StartedAt)
					stop()
				}
			}
			return
		}
		activityMu.Lock()
		if event.TerminalReason != "" {
			terminalReason = event.TerminalReason
		}
		activityMu.Unlock()
		if appendErr := f.store.AppendActivity(ctx, userID, conversationID, runID, f.instanceID, event); appendErr != nil {
			if ctx.Err() != nil {
				return
			}
			activityMu.Lock()
			if activityErr == nil {
				activityErr = appendErr
			}
			activityMu.Unlock()
			cancel()
		}
	})
	activityMu.Lock()
	recordingErr := activityErr
	activityMu.Unlock()
	if err != nil && recordingErr == nil && ctx.Err() == nil && agentKey == "info_gatherer" && f.failureRecovery != nil {
		recoveryCtx, stopRecovery := context.WithTimeout(ctx, 90*time.Second)
		message, recoveryErr := f.failureRecovery(recoveryCtx, userID, runID)
		stopRecovery()
		if recoveryErr != nil {
			message = "AI 对话未能继续；如已有阶段结果，记录仍保留在信息收集页面，但自动汇总未完成。"
		}
		if message != "" && ctx.Err() == nil {
			event := airuntime.Activity{Kind: "result", Summary: message, Detail: message, IsError: recoveryErr != nil}
			if appendErr := f.store.AppendActivity(ctx, userID, conversationID, runID, f.instanceID, event); appendErr != nil {
				activityMu.Lock()
				activityErr = appendErr
				activityMu.Unlock()
			}
		}
	}
	if f.sideQuestions != nil {
		f.sideQuestions.FlushSnapshots(userID, runID)
	}
	cancel()
	<-heartbeatDone
	activityMu.Lock()
	writeErr := activityErr
	activityMu.Unlock()
	if writeErr != nil {
		// Cancellation also prevents the harness terminal from being appended on
		// its original context. Record a bounded, safe failure marker separately;
		// AppendActivity still checks the owner, membership and live run lease.
		failureCtx, stopFailure := context.WithTimeout(context.Background(), 5*time.Second)
		failure := "执行记录保存失败，系统已停止本轮对话。已保存的结果仍保留；请查看系统日志确认保存失败原因。"
		_ = f.store.AppendActivity(failureCtx, userID, conversationID, runID, f.instanceID, airuntime.Activity{Kind: "result", IsError: true, Summary: failure, Detail: failure})
		stopFailure()
	}
	if errors.Is(err, agent.ErrRunBudget) && (terminalReason == "" || terminalReason == "model_error") {
		terminalReason = "run_budget"
	}
	status, message := conversationOutcome(terminalReason, err)
	if writeErr != nil {
		status, message = conversationOutcome("persistence_error", writeErr)
	}
	// A user stop or an expired lease is authoritative and will not be overwritten.
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer finishCancel()
	var finishErr error
	if store, ok := f.store.(ConversationOutcomeStore); ok {
		finishErr = store.FinishRunOutcome(finishCtx, userID, conversationID, runID, f.instanceID, status, terminalReason, message)
	} else {
		finishErr = f.store.FinishRun(finishCtx, userID, conversationID, runID, f.instanceID, status)
	}
	if finishErr != nil {
		pkg.Warn("AI conversation completion persistence failed", zap.String("ai.run_id", runID), zap.Error(finishErr))
	}
}

func (f *ConversationFacade) Activities(ctx context.Context, userID int, conversationID string, after int64) (*ActivityPage, error) {
	if userID <= 0 || !validConversationID(conversationID) || after < 0 {
		return nil, ErrInvalidInput
	}
	return f.store.ListActivities(ctx, userID, conversationID, after)
}

func (f *ConversationFacade) ActivityFeed(ctx context.Context, userID int, conversationID string, after int64) (*ActivityPage, error) {
	if userID <= 0 || !validConversationID(conversationID) || after < 0 {
		return nil, ErrInvalidInput
	}
	if store, ok := f.store.(interface {
		ListActivityMetadata(context.Context, int, string, int64) (*ActivityPage, error)
	}); ok {
		return store.ListActivityMetadata(ctx, userID, conversationID, after)
	}
	return f.store.ListActivities(ctx, userID, conversationID, after)
}

func (f *ConversationFacade) Stop(ctx context.Context, userID int, conversationID string) error {
	if userID <= 0 || !validConversationID(conversationID) {
		return ErrInvalidInput
	}
	runID, err := f.store.StopRun(ctx, userID, conversationID)
	if err != nil {
		return err
	}
	if cancel, ok := f.cancelRun.Load(runID); ok {
		cancel.(context.CancelFunc)()
	}
	return nil
}

func (f *ConversationFacade) ToolExecutions(ctx context.Context, userID int, query string, before int64) ([]ToolExecution, error) {
	query = strings.TrimSpace(query)
	if userID <= 0 || before < 0 || len(query) > 200 {
		return nil, ErrInvalidInput
	}
	return f.store.ListToolExecutions(ctx, userID, query, before, 51, false)
}

func (f *ConversationFacade) ToolExecution(ctx context.Context, userID int, id int64) (*ToolExecution, error) {
	if userID <= 0 || id <= 0 {
		return nil, ErrInvalidInput
	}
	items, err := f.store.ListToolExecutions(ctx, userID, "", id, 1, true)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, ErrConversationNotFound
	}
	return &items[0], nil
}

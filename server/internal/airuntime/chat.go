// Package airuntime adapts the ARTEX Agent runtime to the Tianqiong server.
package airuntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/artex/sidequestion"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
	"github.com/google/uuid"
)

// ChatRun contains only server-resolved identities and capabilities. Callers
// must authorize the user and serialize runs across server instances.
type ChatRun struct {
	ObserveToolTiming bool

	BudgetResolver        ModelRunBudgetResolver
	HistoryRequired       func(context.Context) (bool, error)
	LocalTools            bool
	InteractiveShell      bool
	FindingCaseReview     bool
	ToolLayer             ToolLayerResolver
	ApprovalWrapper       ApprovalToolWrapper
	TrafficResolver       ConversationTrafficResolver
	FindingTrafficEnabled func(context.Context) bool
	TenantID              string
	UserID                int
	SessionID             string
	ExecutionID           string
	AgentKey              string
	SystemPrompt          string
	AdditionalPrompt      string
	Message               string
	UserBackground        string
	Model                 string
	Provider              llm.Provider
	Tools                 []tool.CoreTool
	ContextWindow         int
	MaxTurns              int
	MaxDuration           time.Duration
	NonStreaming          bool
	AssetSearchEnabled    *bool
	MCPAugmenter          func(context.Context, string) ([]tool.CoreTool, agent.DeferredInfo, func())
	Wrapup                agent.WrapupPolicy
}

type Activity struct {
	ToolTiming       *harness.ToolTiming `json:"-"`
	TerminalReason   string
	Kind             string
	Tool             string
	ToolUseID        string
	IsError          bool
	Summary          string
	Detail           string
	InputTokens      *int
	OutputTokens     *int
	CacheReadTokens  *int
	CacheWriteTokens *int
}

type ChatRunner struct {
	root  string
	locks executionLocks
}

func NewChatRunner(root string) (*ChatRunner, error) {
	if root == "" || !filepath.IsAbs(root) {
		return nil, errors.New("AI data root must be an absolute path")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create AI data root: %w", err)
	}
	return &ChatRunner{root: root}, nil
}

func canonicalUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}

func (r *ChatRunner) lockConversation(ctx context.Context, key string) (func(), error) {
	return r.locks.acquire(ctx, key)
}

// Run preserves ARTEX ChatAgent, Norma session resume, compaction and activity
// capture. The host explicitly supplies tools; host shell/file/network tools are
// not implicitly granted when no isolated runner is available.
func (r *ChatRunner) Run(ctx context.Context, run ChatRun, emit func(Activity)) (string, error) {
	if r == nil || ctx == nil || run.Provider == nil || run.UserID <= 0 ||
		!canonicalUUID(run.TenantID) || !canonicalUUID(run.SessionID) || run.AgentKey == "" || run.Message == "" {
		return "", errors.New("invalid AI chat run")
	}
	if run.MaxTurns < 0 || run.MaxDuration < 0 {
		return "", errors.New("invalid AI run limits")
	}
	started := time.Now()
	if run.MaxDuration > 0 {
		var stop context.CancelFunc
		ctx, stop = context.WithTimeout(ctx, run.MaxDuration+30*time.Second)
		defer stop()
	}
	key := fmt.Sprintf("%s/%d/%s", run.TenantID, run.UserID, run.SessionID)
	unlock, lockErr := r.lockConversation(ctx, key)
	if lockErr != nil {
		return "", lockErr
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	ctx = withExecutionID(ctx, run.ExecutionID)
	ctx = withExecutionProgress(ctx, func(message string) {
		if emit != nil {
			emit(Activity{Kind: "progress", Summary: message, Detail: message})
		}
	})
	budgetCtx, budgetErr := withModelRunBudget(ctx, run.UserID, run.BudgetResolver)
	if budgetErr != nil {
		return "", budgetErr
	}
	ctx = budgetCtx
	ctx = WithLLMUsageScope(ctx, LLMUsageScope{UserID: run.UserID, ConversationID: run.SessionID, Worker: run.AgentKey})
	var capture *TrafficAccess
	if run.TrafficResolver != nil {
		var err error
		capture, err = run.TrafficResolver(ctx, run)
		if err != nil {
			return "", err
		}
		if capture != nil {
			ctx, err = withTrafficCapture(ctx, capture)
			if err != nil {
				if capture.Lease != nil {
					capture.Lease.Close()
				}
				return "", err
			}
			defer capture.Lease.Close()
		}
	}
	if run.AssetSearchEnabled != nil {
		ctx = withAssetSearchGrant(ctx, *run.AssetSearchEnabled)
	}
	if run.UserBackground != "" {
		ctx = intercept.WithReviewContext(ctx, "", intercept.ReviewBackground{Source: intercept.BackgroundUserMessage, Text: run.UserBackground})
	}
	ctx = sidequestion.WithParent(ctx, sidequestion.Parent{ConversationKey: run.SessionID})
	if run.FindingCaseReview {
		ctx = agent.WithFindingCaseReview(ctx)
	}
	ctx = agent.WithHostPolicy(ctx, agent.HostPolicy{StreamText: true, ExternalBackgroundTasks: run.LocalTools, AugmentTools: run.MCPAugmenter, FindingTrafficEnabled: run.FindingTrafficEnabled, DecorateTool: func(ctx context.Context, key string, item tool.CoreTool) tool.CoreTool {
		if run.ApprovalWrapper == nil {
			return item
		}
		return run.ApprovalWrapper(ctx, run.UserID, key, agent.RunInfoFrom(ctx), emit, item)
	}, Wrapup: func(key string) agent.WrapupPolicy {
		if key == run.AgentKey {
			return run.Wrapup
		}
		return agent.WrapupPolicy{}
	}})
	userRoot := filepath.Join(r.root, run.TenantID, fmt.Sprintf("user-%d", run.UserID))
	if err := os.MkdirAll(userRoot, 0o700); err != nil {
		return "", fmt.Errorf("create AI workspace: %w", err)
	}
	store := transcript.NewStore(filepath.Join(userRoot, "transcripts"))
	if run.HistoryRequired != nil {
		check, stop := context.WithTimeout(ctx, 3*time.Second)
		required, err := run.HistoryRequired(check)
		stop()
		if err != nil {
			return "", err
		}
		if required {
			if err = requireTranscript(store, run.SessionID); err != nil {
				return "", err
			}
		}
	}
	workRoot := filepath.Join(userRoot, "work")
	ctx = withWorkspace(ctx, workRoot, filepath.Join(workRoot, "sessions", run.SessionID))
	modelOptions := ModelOptions(run.Provider)
	window := run.ContextWindow
	if window == 0 {
		window = modelOptions.ContextWindow
	}
	chat := agent.NewChatAgent(run.Provider, run.Model, filepath.Join(userRoot, "work"), store, window)
	if run.ObserveToolTiming {
		chat.ObserveToolTiming()
	}
	tools := append([]tool.CoreTool{}, run.Tools...)
	tools = append(tools, scopedTrafficTools(capture)...)
	if run.LocalTools {
		local, err := NewNativeLocalTools(ctx, filepath.Join(userRoot, "work"))
		if err != nil {
			return "", err
		}
		defer local.Close()
		tools = append(local.Tools(run.InteractiveShell), tools...)
	}
	if run.ToolLayer != nil {
		var layerErr error
		tools, layerErr = run.ToolLayer(ctx, run.UserID, run.AgentKey, tools)
		if layerErr != nil {
			return "", layerErr
		}
	}
	tools = independentChatTools(tools)
	chat.SetBaseTools(tools)
	chat.SetWebFetchEnabled(false)
	prompt, err := RenderAgentPrompt(run.SystemPrompt, filepath.Join(userRoot, "work"))
	if err != nil {
		prompt = run.SystemPrompt
	}
	if run.AdditionalPrompt != "" {
		prompt += "\n\n【管理员补充要求】\n" + run.AdditionalPrompt
	}
	prompt += independentChatGuidance(tools)
	chat.SetSystemPrompt(prompt)
	chat.SetNonStreaming(func() bool { return run.NonStreaming || modelOptions.NonStreaming })
	chat.SetMaxTokens(func() int { return modelOptions.MaxTokens })
	chat.SetNoaEnabled(func() bool { return modelOptions.NoaCompaction })
	if run.MaxDuration > 0 {
		run.MaxDuration -= time.Since(started)
		if run.MaxDuration <= 0 {
			return "", context.DeadlineExceeded
		}
	}
	return chat.Chat(ctx, run.AgentKey, run.SessionID, run.Message, run.MaxTurns, run.MaxDuration, false, func(event db.Activity) {
		if emit != nil {
			emit(Activity{
				TerminalReason: event.TerminalReason, ToolTiming: event.ToolTiming,
				Kind: event.Kind, Tool: event.Tool, ToolUseID: event.ToolUseID, IsError: event.IsError,
				Summary: event.Summary, Detail: event.Detail, InputTokens: event.InputTokens,
				OutputTokens: event.OutputTokens, CacheReadTokens: event.CacheReadTokens, CacheWriteTokens: event.CacheWriteTokens,
			})
		}
	})
}

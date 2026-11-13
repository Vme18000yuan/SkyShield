package application

import (
	"context"
	"errors"
	"time"

	"github.com/yyhuni/lunafox/server/internal/airuntime"
)

var (
	ErrConversationNotFound = errors.New("conversation not found")
	ErrConversationBusy     = errors.New("conversation is already running")
	ErrModelNotConfigured   = errors.New("AI model is not configured")
	ErrInvalidInput         = errors.New("invalid AI conversation input")
	ErrRunLeaseLost         = errors.New("AI run lease was lost")
	ErrAgentDisabled        = errors.New("AI agent is disabled")
)

type Conversation struct {
	ID             string
	Title          string
	AgentKey       string
	LLMProfileID   *string
	Pinned         bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Running        bool
	ReviewReadOnly bool
}

type ConversationActivity struct {
	Worker           string
	InputTokens      *int
	OutputTokens     *int
	CacheReadTokens  *int
	CacheWriteTokens *int
	ID               int64
	RunID            string
	Kind             string
	Tool             string
	ToolUseID        string
	IsError          bool
	Summary          string
	Detail           string
	CreatedAt        time.Time
}

type ActivityPage struct {
	FinalCursor    int64
	TerminalReason string
	LastProgressAt *time.Time
	LiveText       string
	LiveKind       string
	HasMore        bool
	Cursor         int64
	Items          []ConversationActivity
	Running        bool
	RunStatus      string
	ErrorMessage   string
}

type ToolExecution struct {
	ID             int64
	ConversationID string
	Conversation   string
	RunID          string
	Tool           string
	Input          string
	Output         string
	Status         string
	CreatedAt      time.Time
}

type ConversationStore interface {
	CreateConversation(context.Context, int, string, string) (*Conversation, error)
	ListConversations(context.Context, int) ([]Conversation, error)
	BeginRun(context.Context, int, string, string, string, string) error
	AppendActivity(context.Context, int, string, string, string, airuntime.Activity) error
	Heartbeat(context.Context, int, string, string, string) (bool, error)
	FinishRun(context.Context, int, string, string, string, string) error
	StopRun(context.Context, int, string) (string, error)
	ListActivities(context.Context, int, string, int64) (*ActivityPage, error)
	ListToolExecutions(context.Context, int, string, int64, int, bool) ([]ToolExecution, error)
}

type AgentConversationStore interface {
	CreateAgentConversation(context.Context, int, string, string, string) (*Conversation, error)
	ConversationAgent(context.Context, int, string) (string, error)
}

type ConversationProfileStore interface {
	ConversationProfile(context.Context, int, string) (*string, error)
	SetConversationProfile(context.Context, int, string, *string) error
}

type ConversationMetadataStore interface {
	UpdateConversationMetadata(context.Context, int, string, *string, *bool) error
}

type ConversationHistoryStore interface {
	ReadConversationHistory(context.Context, int, string, int64, int) (*ActivityPage, error)
	ReadConversationActivityDetail(context.Context, int, string, int64) (string, error)
	DeleteOwnedConversations(context.Context, int, []string) ([]string, error)
}

type AgentCatalogResolver func(context.Context, int, string) (airuntime.ChatAgentDefinition, error)

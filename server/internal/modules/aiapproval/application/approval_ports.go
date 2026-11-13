package application

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
)

var (
	ErrInvalidInput         = errors.New("审批配置无效")
	ErrForbidden            = errors.New("无权管理或处理审批")
	ErrNotFound             = errors.New("审批或规则不存在")
	ErrAlreadyDecided       = errors.New("审批已处理或运行已结束")
	ErrRunInactive          = errors.New("AI 运行已停止或租约失效")
	ErrExecutionUnavailable = errors.New("无法唯一关联原始工具调用")
)

type Rule struct {
	ID               int64           `json:"id"`
	Name             string          `json:"name"`
	Enabled          bool            `json:"enabled"`
	Priority         int             `json:"priority"`
	MatchTarget      string          `json:"match_target"`
	MatchType        string          `json:"match_type"`
	Pattern          string          `json:"pattern"`
	Action           string          `json:"action"`
	Message          string          `json:"message"`
	TimeoutEnabled   bool            `json:"timeout_enabled"`
	TimeoutSeconds   int             `json:"timeout_seconds"`
	TimeoutAction    string          `json:"timeout_action"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	DecisionSource   string          `json:"-"`
	ModelInput       json.RawMessage `json:"-"`
	ModelInputDigest string          `json:"-"`
	ModelFallback    bool            `json:"-"`
	JudgeProfileID   *string         `json:"-"`
	ConfigDigest     string          `json:"-"`
}
type Approval struct {
	ID              int64           `json:"id"`
	RuleID          *int64          `json:"rule_id,omitempty"`
	ConversationID  *string         `json:"conversation_id,omitempty"`
	TaskID          *string         `json:"task_id,omitempty"`
	AgentName       string          `json:"agent_name"`
	ToolName        string          `json:"tool_name"`
	ToolInput       json.RawMessage `json:"tool_input"`
	Status          string          `json:"status"`
	DecisionSource  string          `json:"decision_source"`
	Reason          string          `json:"reason"`
	DecidedAt       *time.Time      `json:"decided_at,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	ConvTitle       string          `json:"conv_title"`
	ConvAgentKey    string          `json:"conv_agent_key"`
	TaskName        string          `json:"task_name"`
	RuleName        string          `json:"rule_name"`
	EffectiveAction string          `json:"effective_action,omitempty"`
	ExecutionStatus string          `json:"execution_status,omitempty"`
	ExecutionOutput string          `json:"execution_output,omitempty"`
	ToolUseID       string          `json:"tool_use_id,omitempty"`
	RunID           string          `json:"run_id,omitempty"`
	IntentID        *int64          `json:"-"`
	Audit           json.RawMessage `json:"audit,omitempty"`
}
type Call struct {
	UserID         int
	RunID          string
	ConversationID string
	TaskID         int64
	IntentID       int64
	AgentName      string
	ToolName       string
	ToolUseID      string
	Input          json.RawMessage
	TraceAudit     json.RawMessage
}
type ExecutionActivity struct {
	Seq       int64     `json:"seq"`
	IntentID  *string   `json:"intent_id,omitempty"`
	Worker    string    `json:"worker"`
	TS        time.Time `json:"ts"`
	Kind      string    `json:"kind"`
	Tool      string    `json:"tool,omitempty"`
	ToolUseID string    `json:"tool_use_id,omitempty"`
	IsError   bool      `json:"is_error,omitempty"`
	Summary   string    `json:"summary"`
	MainSeg   *int      `json:"main_seg,omitempty"`
}
type Execution struct {
	ConversationID *string             `json:"conversation_id"`
	TaskID         *string             `json:"task_id"`
	Session        string              `json:"session"`
	Seq            int64               `json:"seq"`
	Items          []ExecutionActivity `json:"items"`
}
type ExecutionContextPage struct {
	Items     []ExecutionActivity `json:"items"`
	HasBefore bool                `json:"has_before"`
	HasAfter  bool                `json:"has_after"`
}
type ExecutionContextStore interface {
	ReadExecutionContext(context.Context, int, int64, int64, int64, int) (ExecutionContextPage, error)
}
type JudgeConfig struct {
	Enabled                bool    `json:"enabled"`
	ProfileID              *string `json:"profile_id"`
	Prompt                 string  `json:"prompt"`
	TimeoutSeconds         int     `json:"timeout_seconds"`
	RetryCount             int     `json:"retry_count"`
	RetryIntervalMS        int     `json:"retry_interval_ms"`
	FailAction             string  `json:"fail_action"`
	AskTimeoutSeconds      int     `json:"ask_timeout_seconds"`
	AskTimeoutAction       string  `json:"ask_timeout_action"`
	IncompleteScriptAction string  `json:"incomplete_script_action"`
}
type JudgeDayUsage struct {
	Date         string `json:"date"`
	Calls        int64  `json:"calls"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
}
type JudgeUsage struct {
	Calls            int64           `json:"calls"`
	InputTokens      int64           `json:"input_tokens"`
	OutputTokens     int64           `json:"output_tokens"`
	CacheReadTokens  int64           `json:"cache_read_tokens"`
	CacheWriteTokens int64           `json:"cache_write_tokens"`
	Daily            []JudgeDayUsage `json:"daily"`
}
type ReviewCapture func(context.Context, int, string, json.RawMessage, *tool.ToolContext) (intercept.ReviewInput, error)
type Reviewer func(context.Context, int, *string, string, intercept.ReviewInput) (string, llm.Usage, bool, error)
type Filter struct {
	Status, Source, Query, From, To string
	Page, Size                      int
	TaskID                          int64
}
type Store interface {
	Seed(context.Context, []Rule) error
	ListRules(context.Context, int, bool) ([]Rule, error)
	SaveRule(context.Context, int, Rule, bool) (Rule, error)
	DeleteRule(context.Context, int, int64) error
	ToggleRule(context.Context, int, int64, bool) error
	EnabledTools(context.Context, int, bool) ([]string, error)
	SetEnabledTools(context.Context, int, []string) error
	ReadJudge(context.Context, int, bool) (JudgeConfig, error)
	SaveJudge(context.Context, int, JudgeConfig) error
	AddJudgeUsage(context.Context, int, *string, llm.Usage) error
	JudgeUsage(context.Context, int, int) (JudgeUsage, error)
	CreateApproval(context.Context, Call, Rule, string) (*Approval, error)
	ReadApproval(context.Context, int, int64) (*Approval, error)
	ReadExecution(context.Context, int, int64) (*Execution, error)
	ListApprovals(context.Context, int, Filter) ([]Approval, int64, error)
	DecideApproval(context.Context, int, int64, string) error
	ResolveTimeout(context.Context, Call, int64) (string, error)
	CancelApproval(context.Context, Call, int64) error
	FinishApproval(context.Context, Call, int64, bool, string, bool) error
}

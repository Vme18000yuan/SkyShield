package application

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapproval "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/application"
)

var (
	ErrTaskNotFound        = errors.New("AI task not found")
	ErrTaskControlConflict = errors.New("AI task state conflicts with the requested action")
	ErrTaskChatUnavailable = airuntime.ErrTaskChatUnavailable
	ErrTaskChatBusy        = airuntime.ErrTaskChatBusy
	ErrTaskChatDisabled    = airuntime.ErrTaskChatDisabled
	ErrTaskCatalogConflict = errors.New("AI task category or template name already exists")
)

type Task struct {
	ID                   int64
	TenantID             string
	OwnerUserID          int
	Name                 string
	Description          string
	Goal                 string
	Remark               string
	CategoryID           *int64
	CategoryName         string
	PinnedAt             *time.Time
	ExplorationID        int64
	TargetID             *int
	Status               string
	Queued               bool
	WorkerCount          int
	MaxTurns             int
	TimeoutSeconds       int
	PlanHeartbeatSeconds int
	BootstrapComplete    bool
	CoverageEnabled      bool
	ActiveRun            string
	LeaseOwner           *string
	LeaseEpoch           int64
	LeaseUntil           *time.Time
	FirstRunAt           *time.Time
	DeadlineAt           *time.Time
	ErrorMessage         string
	CreatedAt            time.Time
	UpdatedAt            time.Time
	CompletedAt          *time.Time
	LLMProfileIDs        []string `gorm:"-"`
	SourceTaskIDs        []int64  `gorm:"-"`
	ActiveLLMProfileID   *string
	LLMChainRevision     int64
	LLMFailoverState     string `gorm:"-"`
	LLMFailoverReason    string `gorm:"-"`
}

type CreateTaskInput struct {
	UserID               int
	Name                 string
	Description          string
	Goal                 string
	Remark               string
	CategoryID           *int64
	TargetID             int
	AssetIDs             []int64
	SourceTaskIDs        []int64
	LLMProfileIDs        []string
	WorkerCount          int
	MaxTurns             int
	TimeoutSeconds       int
	PlanHeartbeatSeconds int
	StartImmediately     bool
	SeedFirstIntent      bool
	CoverageEnabled      *bool
	InterceptRules       []aiapproval.AssetRule
	// Internal, trusted origin of an Auto tool call. HTTP task creation never sets these.
	ConversationRunID string
	ToolUseID         string
	ToolInputHash     string
}

type TaskLLMProfile struct {
	ProfileID   string     `json:"profileId"`
	Position    int        `json:"position"`
	Status      string     `json:"status"`
	LastError   string     `json:"lastError,omitempty"`
	ExhaustedAt *time.Time `json:"exhaustedAt,omitempty"`
}
type TaskLLMState struct {
	Profiles        []TaskLLMProfile `json:"profiles"`
	ActiveProfileID *string          `json:"activeProfileId"`
	Revision        int64            `json:"revision"`
	State           string           `json:"state"`
	Reason          string           `json:"reason"`
}
type TaskLLMTransition struct {
	PreviousProfileID string
	NextProfileID     *string
	Advanced          bool
	Stale             bool
	Exhausted         bool
}
type TaskLLMStore interface {
	TaskLLMState(context.Context, int, int64) (TaskLLMState, error)
	ReplaceTaskLLMChain(context.Context, int, int64, []string, *string) (TaskLLMState, error)
	ExhaustTaskLLMProfile(context.Context, airuntime.TaskExecution, string, int64, string) (TaskLLMTransition, error)
}

type FindingTrafficRecord struct {
	FindingID     int64
	NodeID        int64
	TaskID        int64
	Version       int64
	ReportVersion int64
	Bindings      []artexdb.FindingTrafficBinding
}
type FindingReportStore interface {
	ReadFindingTraffic(context.Context, int, int64) (FindingTrafficRecord, error)
	UpdateFindingReport(context.Context, int, string, string, int64, string, *int64) (bool, error)
}

type TaskTraceSearchStore interface {
	SearchTaskTrace(context.Context, int, int64, string, int) ([]TaskActivity, error)
}

type TaskGraphNode struct {
	FindingID     int64
	TrafficCount  int64
	SourceTaskID  int64
	Inherited     bool
	ID            int64
	Kind          string
	State         string
	DeleteReason  string
	DeletionState string
	Priority      int
	Payload       json.RawMessage
	CreatedAt     time.Time
}

type TaskGraphEdge struct {
	SourceID int64
	TargetID int64
	Relation string
}

type TaskGraph struct {
	Nodes     []TaskGraphNode
	Edges     []TaskGraphEdge
	Assets    []TaskGraphAsset
	Truncated bool
}

type TaskGraphAsset struct {
	NodeID    int64
	AssetID   int64
	AssetType string
	Label     string
}

type TaskActivity struct {
	ID        int64
	NodeID    *int64
	Worker    string
	Kind      string
	Tool      string
	ToolUseID string
	IsError   bool
	Summary   string
	Detail    string
	MainSeg   *int
	CreatedAt time.Time
}

type TaskToolExecution struct {
	ID        int64
	TaskID    int64
	TaskName  string
	RunID     string
	Tool      string
	Input     string
	Output    string
	Status    string
	CreatedAt time.Time
}

// FactCount is evidence of recorded facts, not proof that an asset was fully tested.
type TaskAsset struct {
	ID           int64
	AssetType    string
	Label        string
	Source       string
	FactCount    int64
	Inherited    bool
	SourceTaskID int64
}

type TaskStore interface {
	CreateTask(context.Context, CreateTaskInput) (*Task, error)
	ListTasks(context.Context, int, int64) ([]Task, error)
	GetTask(context.Context, int, int64) (*Task, error)
	ControlTask(context.Context, int, int64, string, bool) (*Task, error)
	ClaimTask(context.Context, string) (*Task, error)
	StampTaskStart(context.Context, *Task) (*Task, error)
	HeartbeatTask(context.Context, *Task) (bool, error)
	FinishTask(context.Context, *Task, string, string) error
	RecoverExpiredTasks(context.Context) (int64, error)
	ReadTaskGraph(context.Context, int, int64) (*TaskGraph, error)
	ReadTaskActivities(context.Context, int, int64, int64) ([]TaskActivity, error)
	ListTaskToolExecutions(context.Context, int, string, int64, int, bool) ([]TaskToolExecution, error)
	ListTaskAssets(context.Context, int, int64, int64) ([]TaskAsset, error)
}

type TaskRuntime interface {
	RunTask(context.Context, airuntime.TaskExecution, llm.Provider, string) (airuntime.TaskOutcome, error)
}

type TaskNodeStore interface {
	ReadTaskNode(context.Context, int, int64, int64) (*TaskGraphNode, error)
}

type TaskTraceStore interface {
	ReadTaskTrace(context.Context, int, int64, int64, []int64) ([]TaskActivity, error)
}

type TaskChatRuntime interface {
	ChatTask(context.Context, airuntime.TaskExecution, string) (string, error)
}

// Independent turns are admitted separately from planner/worker execution.
type TaskIndependentChatRuntime interface {
	ChatIndependent(context.Context, airuntime.TaskExecution, llm.Provider, string, string) (string, error)
}
type TaskMainChatLeaseStore interface {
	ClaimMainChat(context.Context, int, int64, string) (*Task, error)
	HeartbeatMainChat(context.Context, int, int64, string) (bool, error)
	ReleaseMainChat(context.Context, int, int64, string) error
	StopMainChat(context.Context, int, int64) (string, error)
	MainChatRunning(context.Context, int, int64) (bool, error)
	ProcessMainChatWakes(context.Context) error
}

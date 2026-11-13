package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapproval "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/application"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type taskApplication interface {
	Create(context.Context, aiapp.CreateTaskInput) (*aiapp.Task, error)
	List(context.Context, int, int64) ([]aiapp.Task, error)
	Get(context.Context, int, int64) (*aiapp.Task, error)
	Control(context.Context, int, int64, string) (*aiapp.Task, error)
	Graph(context.Context, int, int64) (*aiapp.TaskGraph, error)
	Activities(context.Context, int, int64, int64) ([]aiapp.TaskActivity, error)
	ToolExecutions(context.Context, int, string, int64) ([]aiapp.TaskToolExecution, error)
	ToolExecution(context.Context, int, int64) (*aiapp.TaskToolExecution, error)
	Assets(context.Context, int, int64, int64) ([]aiapp.TaskAsset, error)
	Chat(context.Context, int, int64, string) (string, error)
}

type TaskHandler struct {
	app               taskApplication
	intelligence      *aiapp.IntelligenceFacade
	credentials       *aiapp.CredentialProcessor
	traffic           *aiapp.TaskTrafficFacade
	globalTraffic     *aiapp.GlobalTrafficFacade
	evidence          *aiapp.FindingEvidenceFacade
	credentialTraffic *aiapp.CredentialTrafficDetailFacade
}

func NewTaskHandler(app taskApplication, intelligence ...*aiapp.IntelligenceFacade) *TaskHandler {
	h := &TaskHandler{app: app}
	if len(intelligence) > 0 {
		h.intelligence = intelligence[0]
	}
	return h
}

func (h *TaskHandler) SetCredentialProcessor(processor *aiapp.CredentialProcessor) {
	h.credentials = processor
}

func taskDTO(task aiapp.Task) gin.H {
	var category any
	if task.CategoryID != nil {
		category = strconv.FormatInt(*task.CategoryID, 10)
	}
	sources := make([]string, 0, len(task.SourceTaskIDs))
	for _, id := range task.SourceTaskIDs {
		sources = append(sources, strconv.FormatInt(id, 10))
	}
	return gin.H{"id": strconv.FormatInt(task.ID, 10), "name": httpdto.AITaskName(task.ID), "displayName": task.Name, "description": task.Description, "goal": task.Goal,
		"remark": task.Remark, "categoryId": category, "categoryName": task.CategoryName, "pinned": task.PinnedAt != nil, "pinnedAt": task.PinnedAt, "sourceTaskIds": sources, "coverageEnabled": task.CoverageEnabled,
		"targetId": task.TargetID, "status": task.Status, "queued": task.Queued, "workerCount": task.WorkerCount, "maxTurns": task.MaxTurns, "timeoutSeconds": task.TimeoutSeconds, "planHeartbeatSeconds": task.PlanHeartbeatSeconds,
		"firstRunAt": task.FirstRunAt, "deadlineAt": task.DeadlineAt, "errorMessage": task.ErrorMessage, "createdAt": task.CreatedAt.UTC(), "updatedAt": task.UpdatedAt.UTC(), "completedAt": task.CompletedAt,
		"llmProfileIds": task.LLMProfileIDs, "activeLlmProfileId": task.ActiveLLMProfileID, "llmFailoverState": task.LLMFailoverState, "llmFailoverReason": task.LLMFailoverReason}
}

func taskError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, aiapp.ErrTaskNotFound):
		httpdto.NotFound(c, "AI task not found")
	case errors.Is(err, aiapp.ErrTaskCatalogConflict):
		httpdto.Conflict(c, "分类或模板名称已存在")
	case errors.Is(err, aiapp.ErrEvidenceVersionConflict):
		httpdto.Conflict(c, "漏洞证据已更新，请刷新后重试")
	case errors.Is(err, aiapp.ErrTaskControlConflict):
		httpdto.Conflict(c, "AI task state conflicts with this action")
	case errors.Is(err, aiapp.ErrTaskChatUnavailable):
		httpdto.Conflict(c, "AI task MainAgent is unavailable")
	case errors.Is(err, aiapp.ErrTaskChatBusy):
		httpdto.Conflict(c, "AI task MainAgent is handling another message")
	case errors.Is(err, aiapp.ErrTaskChatDisabled):
		httpdto.Conflict(c, "AI task MainAgent is disabled in settings")
	case errors.Is(err, airuntime.ErrTaskModelChainExhausted):
		httpdto.Conflict(c, "任务模型链额度已耗尽，请更换或重置模型后恢复")
	case errors.Is(err, assetapp.ErrAssetWriteForbidden):
		httpdto.Forbidden(c, "Task execution permission required")
	case errors.Is(err, assetapp.ErrAssetTargetNotFound):
		httpdto.NotFound(c, "Active target not found")
	case errors.Is(err, assetapp.ErrInvalidAsset), errors.Is(err, assetapp.ErrAssetOutsideTarget):
		httpdto.BadRequest(c, "Asset does not belong to the selected task scope")
	case errors.Is(err, aiapp.ErrInvalidInput):
		httpdto.BadRequest(c, "Invalid AI task input")
	default:
		conversationError(c, err)
	}
}

func taskID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("task"), 10, 64)
	if err != nil || id <= 0 {
		httpdto.BadRequest(c, "Invalid task ID")
		return 0, false
	}
	return id, true
}

// Create creates a task and optionally admits it to the execution queue.
// POST /v1/aiTasks
func (h *TaskHandler) Create(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128*1024)
	var input struct {
		DisplayName          string                 `json:"displayName"`
		Description          string                 `json:"description"`
		Goal                 string                 `json:"goal"`
		Remark               string                 `json:"remark"`
		CategoryID           *string                `json:"categoryId"`
		TargetID             int                    `json:"targetId"`
		AssetIDs             []string               `json:"assetIds"`
		SourceTaskIDs        []string               `json:"sourceTaskIds"`
		LLMProfileIDs        []string               `json:"llmProfileIds"`
		WorkerCount          int                    `json:"workerCount"`
		MaxTurns             int                    `json:"maxTurns"`
		TimeoutSeconds       int                    `json:"timeoutSeconds"`
		PlanHeartbeatSeconds int                    `json:"planHeartbeatSeconds"`
		StartImmediately     *bool                  `json:"startImmediately"`
		SeedFirstIntent      bool                   `json:"seedFirstIntent"`
		CoverageEnabled      *bool                  `json:"coverageEnabled"`
		InterceptRules       []aiapproval.AssetRule `json:"interceptRules"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	var ids []int64
	for _, raw := range input.AssetIDs {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			httpdto.BadRequest(c, "Invalid asset ID")
			return
		}
		ids = append(ids, id)
	}
	var categoryID *int64
	if input.CategoryID != nil {
		id, err := strconv.ParseInt(*input.CategoryID, 10, 64)
		if err != nil || id <= 0 {
			httpdto.BadRequest(c, "Invalid category ID")
			return
		}
		categoryID = &id
	}
	var sourceIDs []int64
	for _, raw := range input.SourceTaskIDs {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			httpdto.BadRequest(c, "Invalid source task ID")
			return
		}
		sourceIDs = append(sourceIDs, id)
	}
	start := true
	if input.StartImmediately != nil {
		start = *input.StartImmediately
	}
	task, err := h.app.Create(c.Request.Context(), aiapp.CreateTaskInput{UserID: user, Name: input.DisplayName, Description: input.Description, Goal: input.Goal, Remark: input.Remark, CategoryID: categoryID, TargetID: input.TargetID,
		AssetIDs: ids, SourceTaskIDs: sourceIDs, LLMProfileIDs: input.LLMProfileIDs, WorkerCount: input.WorkerCount, MaxTurns: input.MaxTurns, TimeoutSeconds: input.TimeoutSeconds, PlanHeartbeatSeconds: input.PlanHeartbeatSeconds, StartImmediately: start, SeedFirstIntent: input.SeedFirstIntent, CoverageEnabled: input.CoverageEnabled, InterceptRules: input.InterceptRules})
	if err != nil {
		taskError(c, err)
		return
	}
	httpdto.Created(c, taskDTO(*task))
}

// List returns caller-owned tasks with cursor pagination.
// GET /v1/aiTasks
func (h *TaskHandler) List(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	var before int64
	if raw := c.Query("before"); raw != "" {
		var err error
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || before <= 0 {
			httpdto.BadRequest(c, "Invalid task cursor")
			return
		}
	}
	filter := aiapp.TaskListFilter{Query: c.Query("q"), Status: c.Query("status")}
	if raw := c.Query("category"); raw == "none" {
		filter.NoCategory = true
	} else if raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			httpdto.BadRequest(c, "Invalid category ID")
			return
		}
		filter.CategoryID = &value
	}
	var tasks []aiapp.Task
	var err error
	if filtered, ok := h.app.(interface {
		ListFiltered(context.Context, int, int64, aiapp.TaskListFilter) ([]aiapp.Task, error)
	}); ok {
		tasks, err = filtered.ListFiltered(c.Request.Context(), user, before, filter)
	} else if filter.Query == "" && filter.Status == "" && filter.CategoryID == nil && !filter.NoCategory {
		tasks, err = h.app.List(c.Request.Context(), user, before)
	} else {
		err = aiapp.ErrInvalidInput
	}
	if err != nil {
		taskError(c, err)
		return
	}
	next := ""
	if len(tasks) > 50 {
		tasks = tasks[:50]
		next = strconv.FormatInt(tasks[len(tasks)-1].ID, 10)
	}
	items := make([]gin.H, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, taskDTO(task))
	}
	httpdto.Success(c, gin.H{"tasks": items, "nextBefore": next})
}

// Metadata updates task name, remark, pin, or category without changing its run.
// PATCH /v1/aiTasks/:task
func (h *TaskHandler) Metadata(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	id, ok := taskID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32*1024)
	var input struct {
		Name       *string         `json:"displayName"`
		Remark     *string         `json:"remark"`
		Pinned     *bool           `json:"pinned"`
		CategoryID json.RawMessage `json:"categoryId"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	update := aiapp.TaskMetadataUpdate{Name: input.Name, Remark: input.Remark, Pinned: input.Pinned, SetCategoryID: input.CategoryID != nil}
	if input.CategoryID != nil && string(input.CategoryID) != "null" {
		var raw string
		if json.Unmarshal(input.CategoryID, &raw) != nil {
			httpdto.BadRequest(c, "Invalid category ID")
			return
		}
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			httpdto.BadRequest(c, "Invalid category ID")
			return
		}
		update.CategoryID = &value
	}
	app, ok := h.app.(interface {
		UpdateMetadata(context.Context, int, int64, aiapp.TaskMetadataUpdate) (*aiapp.Task, error)
	})
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	task, err := app.UpdateMetadata(c.Request.Context(), user, id, update)
	if err != nil {
		taskError(c, err)
		return
	}
	httpdto.Success(c, taskDTO(*task))
}

// Get returns one caller-owned task.
// GET /v1/aiTasks/:task
func (h *TaskHandler) Get(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	id, ok := taskID(c)
	if !ok {
		return
	}
	task, err := h.app.Get(c.Request.Context(), user, id)
	if err != nil {
		taskError(c, err)
		return
	}
	httpdto.Success(c, taskDTO(*task))
}

// Control changes one task's lifecycle.
// POST /v1/aiTasks/:task/actions
func (h *TaskHandler) Control(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	id, ok := taskID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4*1024)
	var input struct {
		Action string `json:"action"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	task, err := h.app.Control(c.Request.Context(), user, id, input.Action)
	if err != nil {
		taskError(c, err)
		return
	}
	httpdto.Success(c, taskDTO(*task))
}

// Graph returns a bounded exploration graph with an explicit truncation flag.
// GET /v1/aiTasks/:task/graph
func (h *TaskHandler) Graph(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	id, ok := taskID(c)
	if !ok {
		return
	}
	graph, err := h.app.Graph(c.Request.Context(), user, id)
	if err != nil {
		taskError(c, err)
		return
	}
	nodes := make([]gin.H, 0, len(graph.Nodes))
	edges := make([]gin.H, 0, len(graph.Edges))
	assets := make([]gin.H, 0, len(graph.Assets))
	for _, node := range graph.Nodes {
		item := gin.H{"id": strconv.FormatInt(node.ID, 10), "kind": node.Kind, "state": node.State, "priority": node.Priority, "payload": node.Payload, "createdAt": node.CreatedAt.UTC()}
		if node.DeleteReason != "" {
			item["deleteReason"] = node.DeleteReason
		}
		if node.DeletionState != "" {
			item["deletionState"] = node.DeletionState
		}
		if node.Inherited {
			item["inherited"] = true
			item["sourceTaskId"] = strconv.FormatInt(node.SourceTaskID, 10)
		}
		if node.FindingID > 0 {
			item["findingId"] = strconv.FormatInt(node.FindingID, 10)
			item["trafficCount"] = node.TrafficCount
		}
		nodes = append(nodes, item)
	}
	for _, edge := range graph.Edges {
		edges = append(edges, gin.H{"source": strconv.FormatInt(edge.SourceID, 10), "target": strconv.FormatInt(edge.TargetID, 10), "relation": edge.Relation})
	}
	for _, asset := range graph.Assets {
		assets = append(assets, gin.H{"nodeId": strconv.FormatInt(asset.NodeID, 10), "assetId": strconv.FormatInt(asset.AssetID, 10), "assetType": asset.AssetType, "label": asset.Label})
	}
	httpdto.Success(c, gin.H{"nodes": nodes, "edges": edges, "assets": assets, "truncated": graph.Truncated})
}

// Activities returns incremental task activities.
// GET /v1/aiTasks/:task/activities
func (h *TaskHandler) Activities(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	id, ok := taskID(c)
	if !ok {
		return
	}
	var after int64
	if raw := c.Query("after"); raw != "" {
		var err error
		after, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || after < 0 {
			httpdto.BadRequest(c, "Invalid activity cursor")
			return
		}
	}
	items, err := h.app.Activities(c.Request.Context(), user, id, after)
	if err != nil {
		taskError(c, err)
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, item := range items {
		var nodeID *string
		if item.NodeID != nil {
			v := strconv.FormatInt(*item.NodeID, 10)
			nodeID = &v
		}
		out = append(out, gin.H{"id": strconv.FormatInt(item.ID, 10), "nodeId": nodeID, "worker": item.Worker, "kind": item.Kind, "tool": item.Tool, "toolUseId": item.ToolUseID, "isError": item.IsError, "summary": item.Summary, "detail": item.Detail, "mainSeg": item.MainSeg, "createdAt": item.CreatedAt.UTC()})
	}
	httpdto.Success(c, gin.H{"activities": out})
}

func taskToolDTO(item aiapp.TaskToolExecution) gin.H {
	return gin.H{"id": strconv.FormatInt(item.ID, 10), "taskId": strconv.FormatInt(item.TaskID, 10), "conversation": item.TaskName, "runId": item.RunID, "tool": item.Tool, "input": item.Input, "output": item.Output, "status": item.Status, "createdAt": item.CreatedAt.UTC()}
}

// ToolExecutions returns calls made by the caller's AI tasks.
// GET /v1/aiTaskToolExecutions
func (h *TaskHandler) ToolExecutions(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	var before int64
	if raw := c.Query("before"); raw != "" {
		var err error
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || before <= 0 {
			httpdto.BadRequest(c, "Invalid cursor")
			return
		}
	}
	items, err := h.app.ToolExecutions(c.Request.Context(), user, c.Query("q"), before)
	if err != nil {
		taskError(c, err)
		return
	}
	next := ""
	if len(items) > 50 {
		items = items[:50]
		next = strconv.FormatInt(items[len(items)-1].ID, 10)
	}
	out := make([]gin.H, 0, len(items))
	for _, item := range items {
		out = append(out, taskToolDTO(item))
	}
	httpdto.Success(c, gin.H{"executions": out, "nextBefore": next})
}

// ToolExecution returns one caller-owned task tool call and its matching result.
// GET /v1/aiTaskToolExecutions/:execution
func (h *TaskHandler) ToolExecution(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("execution"), 10, 64)
	if err != nil || id <= 0 {
		httpdto.BadRequest(c, "Invalid execution ID")
		return
	}
	item, err := h.app.ToolExecution(c.Request.Context(), user, id)
	if err != nil {
		taskError(c, err)
		return
	}
	httpdto.Success(c, taskToolDTO(*item))
}

// Assets returns the task's canonical assets and confirmed fact references.
// GET /v1/aiTasks/:task/assets
func (h *TaskHandler) Assets(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	id, ok := taskID(c)
	if !ok {
		return
	}
	var after int64
	if raw := c.Query("after"); raw != "" {
		var err error
		after, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || after < 0 {
			httpdto.BadRequest(c, "Invalid asset cursor")
			return
		}
	}
	items, err := h.app.Assets(c.Request.Context(), user, id, after)
	if err != nil {
		taskError(c, err)
		return
	}
	next := ""
	if len(items) > 100 {
		items = items[:100]
		next = strconv.FormatInt(items[len(items)-1].ID, 10)
	}
	out := make([]gin.H, 0, len(items))
	for _, item := range items {
		entry := gin.H{"id": strconv.FormatInt(item.ID, 10), "assetType": item.AssetType, "label": item.Label, "source": item.Source, "factCount": item.FactCount}
		if item.Inherited {
			entry["inherited"] = true
			entry["sourceTaskId"] = strconv.FormatInt(item.SourceTaskID, 10)
		}
		out = append(out, entry)
	}
	httpdto.Success(c, gin.H{"assets": out, "nextAfter": next})
}

// Chat sends one operator message to the active task MainAgent.
// POST /v1/aiTasks/:task/messages
func (h *TaskHandler) Chat(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	id, ok := taskID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 68*1024)
	var input struct {
		Message string `json:"message"`
		Segment *int   `json:"segment"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	var reply string
	var err error
	if input.Segment != nil {
		app, ok := h.app.(interface {
			ChatInSegment(context.Context, int, int64, string, *int) (string, error)
		})
		if !ok {
			taskError(c, aiapp.ErrTaskNotFound)
			return
		}
		reply, err = app.ChatInSegment(c.Request.Context(), user, id, input.Message, input.Segment)
	} else {
		reply, err = h.app.Chat(c.Request.Context(), user, id, input.Message)
	}
	if err != nil {
		taskError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"reply": reply})
}

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type conversationApplication interface {
	Create(context.Context, int, string) (*aiapp.Conversation, error)
	List(context.Context, int) ([]aiapp.Conversation, error)
	Send(context.Context, int, string, string) (string, error)
	Activities(context.Context, int, string, int64) (*aiapp.ActivityPage, error)
	Stop(context.Context, int, string) error
	ToolExecutions(context.Context, int, string, int64) ([]aiapp.ToolExecution, error)
	ToolExecution(context.Context, int, int64) (*aiapp.ToolExecution, error)
}

type agentConversationApplication interface {
	CreateWithAgent(context.Context, int, string, string) (*aiapp.Conversation, error)
}

type conversationProfileApplication interface {
	SetProfile(context.Context, int, string, *string) error
}

type conversationMetadataApplication interface {
	UpdateMetadata(context.Context, int, string, *string, *bool) error
}

type ConversationHandler struct {
	app     conversationApplication
	side    *aiapp.SideQuestionFacade
	traffic *aiapp.ConversationTrafficFacade
}

func NewConversationHandler(app conversationApplication, side ...*aiapp.SideQuestionFacade) *ConversationHandler {
	h := &ConversationHandler{app: app}
	if len(side) > 0 {
		h.side = side[0]
	}
	return h
}

type conversationResponse struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	AgentKey       string    `json:"agentKey"`
	LLMProfileID   *string   `json:"llmProfileId"`
	Pinned         bool      `json:"pinned"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	Running        bool      `json:"running"`
	ReviewReadOnly bool      `json:"reviewReadOnly"`
}

func conversationDTO(item aiapp.Conversation) conversationResponse {
	return conversationResponse{ID: item.ID, Title: item.Title, AgentKey: item.AgentKey, LLMProfileID: item.LLMProfileID, Pinned: item.Pinned, CreatedAt: item.CreatedAt.UTC(), UpdatedAt: item.UpdatedAt.UTC(), Running: item.Running, ReviewReadOnly: item.ReviewReadOnly}
}

func (h *ConversationHandler) UpdateMetadata(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var req struct {
		Title  *string `json:"title"`
		Pinned *bool   `json:"pinned"`
	}
	if !httpdto.BindJSON(c, &req) {
		return
	}
	app, ok := h.app.(conversationMetadataApplication)
	if !ok {
		conversationError(c, aiapp.ErrInvalidInput)
		return
	}
	if err := app.UpdateMetadata(c.Request.Context(), user, c.Param("conversation"), req.Title, req.Pinned); err != nil {
		conversationError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"updated": true})
}

func (h *ConversationHandler) SetProfile(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var req struct {
		LLMProfileID json.RawMessage `json:"llmProfileId"`
	}
	if !httpdto.BindJSON(c, &req) {
		return
	}
	var profileID *string
	if req.LLMProfileID == nil || json.Unmarshal(req.LLMProfileID, &profileID) != nil {
		httpdto.BadRequest(c, "llmProfileId is required and must be a model ID or null")
		return
	}
	app, ok := h.app.(conversationProfileApplication)
	if !ok {
		conversationError(c, aiapp.ErrInvalidInput)
		return
	}
	if err := app.SetProfile(c.Request.Context(), user, c.Param("conversation"), profileID); err != nil {
		conversationError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"llmProfileId": profileID})
}

func conversationUser(c *gin.Context) (int, bool) {
	id, ok := middleware.GetUserID(c)
	if !ok {
		httpdto.Unauthorized(c, "Not authenticated")
	}
	return id, ok
}

func conversationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, airuntime.ErrExecutionCapacity):
		c.Header("Retry-After", "5")
		c.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error(), "message": err.Error()})
	case errors.Is(err, aiapp.ErrInvalidInput):
		httpdto.BadRequest(c, "Invalid conversation input")
	case errors.Is(err, aiapp.ErrConversationNotFound):
		httpdto.NotFound(c, "Conversation not found")
	case errors.Is(err, aiapp.ErrConversationBusy):
		httpdto.Conflict(c, "Conversation is already running")
	case errors.Is(err, aiapp.ErrAgentDisabled):
		httpdto.Conflict(c, "AI agent is disabled")
	case errors.Is(err, airuntime.ErrChatAgentNotFound):
		httpdto.NotFound(c, "AI agent not found")
	case errors.Is(err, settingsapp.ErrAgentCatalogNotFound):
		httpdto.NotFound(c, "AI agent not found")
	case errors.Is(err, settingsapp.ErrAgentCatalogInvalid):
		httpdto.BadRequest(c, "Invalid AI agent")
	case errors.Is(err, aiapp.ErrModelNotConfigured):
		httpdto.ErrorWithStatus(c, http.StatusServiceUnavailable, "AI_MODEL_NOT_CONFIGURED", "UNAVAILABLE", "AI model is not configured")
	case errors.Is(err, aiapp.ErrSnapshotUnavailable):
		httpdto.Conflict(c, err.Error())
	default:
		httpdto.InternalError(c, "AI conversation request failed")
	}
}

// List returns the caller's conversations.
// GET /v1/aiConversations
func (h *ConversationHandler) List(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	items, err := h.app.List(c.Request.Context(), user)
	if err != nil {
		conversationError(c, err)
		return
	}
	out := make([]conversationResponse, 0, len(items))
	for _, item := range items {
		out = append(out, conversationDTO(item))
	}
	httpdto.Success(c, gin.H{"conversations": out})
}

// Create starts an empty conversation owned by the caller.
// POST /v1/aiConversations
func (h *ConversationHandler) Create(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	var req struct {
		Title    string `json:"title"`
		AgentKey string `json:"agentKey"`
	}
	if !httpdto.BindJSON(c, &req) {
		return
	}
	var item *aiapp.Conversation
	var err error
	if req.AgentKey != "" {
		if app, ok := h.app.(agentConversationApplication); ok {
			item, err = app.CreateWithAgent(c.Request.Context(), user, req.Title, req.AgentKey)
		} else {
			err = aiapp.ErrInvalidInput
		}
	} else {
		item, err = h.app.Create(c.Request.Context(), user, req.Title)
	}
	if err != nil {
		conversationError(c, err)
		return
	}
	httpdto.Created(c, conversationDTO(*item))
}

// Send starts one asynchronous conversation turn.
// POST /v1/aiConversations/:conversation/messages
func (h *ConversationHandler) Send(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 72*1024)
	var req struct {
		Message     string                 `json:"message"`
		Attachments []aiapp.ChatAttachment `json:"attachments"`
	}
	if !httpdto.BindJSON(c, &req) {
		return
	}
	var run string
	var err error
	if len(req.Attachments) > 0 {
		app, ok := h.app.(interface {
			SendWithAttachments(context.Context, int, string, string, []aiapp.ChatAttachment) (string, error)
		})
		if !ok {
			conversationError(c, aiapp.ErrInvalidInput)
			return
		}
		run, err = app.SendWithAttachments(c.Request.Context(), user, c.Param("conversation"), req.Message, req.Attachments)
	} else {
		run, err = h.app.Send(c.Request.Context(), user, c.Param("conversation"), req.Message)
	}
	if err != nil {
		conversationError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"runId": run})
}

// Activities returns a caller-scoped incremental activity page.
// GET /v1/aiConversations/:conversation/activities
func (h *ConversationHandler) Activities(c *gin.Context) {
	user, ok := conversationUser(c)
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
	page, err := h.app.Activities(c.Request.Context(), user, c.Param("conversation"), after)
	if err != nil {
		conversationError(c, err)
		return
	}
	items := make([]gin.H, 0, len(page.Items))
	for _, a := range page.Items {
		items = append(items, gin.H{"id": strconv.FormatInt(a.ID, 10), "runId": a.RunID, "kind": a.Kind,
			"tool": a.Tool, "toolUseId": a.ToolUseID, "isError": a.IsError, "summary": a.Summary,
			"detail": a.Detail, "createdAt": a.CreatedAt.UTC()})
	}
	httpdto.Success(c, gin.H{"activities": items, "running": page.Running, "runStatus": page.RunStatus, "errorMessage": page.ErrorMessage})
}

// Stop requests cancellation of a caller-owned run.
// POST /v1/aiConversations/:conversation/stop
func (h *ConversationHandler) Stop(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	if err := h.app.Stop(c.Request.Context(), user, c.Param("conversation")); err != nil {
		conversationError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"stopRequested": true, "status": "stopping"})
}

func toolExecutionDTO(item aiapp.ToolExecution) gin.H {
	return gin.H{"id": strconv.FormatInt(item.ID, 10), "conversationId": item.ConversationID,
		"conversation": item.Conversation, "runId": item.RunID, "tool": item.Tool, "input": item.Input,
		"output": item.Output, "status": item.Status, "createdAt": item.CreatedAt.UTC()}
}

// ToolExecutions lists tool calls from the caller's AI conversations.
// GET /v1/aiToolExecutions
func (h *ConversationHandler) ToolExecutions(c *gin.Context) {
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
		conversationError(c, err)
		return
	}
	next := ""
	if len(items) > 50 {
		items = items[:50]
		next = strconv.FormatInt(items[len(items)-1].ID, 10)
	}
	out := make([]gin.H, 0, len(items))
	for _, item := range items {
		out = append(out, toolExecutionDTO(item))
	}
	httpdto.Success(c, gin.H{"executions": out, "nextBefore": next})
}

// ToolExecution returns full input and output for one caller-owned tool call.
// GET /v1/aiToolExecutions/:execution
func (h *ConversationHandler) ToolExecution(c *gin.Context) {
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
		conversationError(c, err)
		return
	}
	httpdto.Success(c, toolExecutionDTO(*item))
}

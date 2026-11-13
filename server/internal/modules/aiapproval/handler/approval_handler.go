package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	app "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type Handler struct{ service *app.Service }

func NewHandler(service *app.Service) *Handler { return &Handler{service: service} }
func user(c *gin.Context) (int, bool) {
	id, ok := middleware.GetUserID(c)
	if !ok {
		httpdto.Unauthorized(c, "Not authenticated")
	}
	return id, ok
}
func parseID(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 {
		httpdto.BadRequest(c, "Invalid ID")
		return 0, false
	}
	return id, true
}
func respondError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, app.ErrInvalidInput):
		httpdto.BadRequest(c, err.Error())
	case errors.Is(err, app.ErrForbidden):
		httpdto.Forbidden(c, err.Error())
	case errors.Is(err, app.ErrNotFound):
		httpdto.NotFound(c, err.Error())
	case errors.Is(err, app.ErrAlreadyDecided), errors.Is(err, app.ErrRunInactive), errors.Is(err, app.ErrExecutionUnavailable):
		httpdto.Conflict(c, err.Error())
	default:
		httpdto.InternalError(c, "审批请求失败")
	}
}
func (h *Handler) Rules(c *gin.Context) {
	id, ok := user(c)
	if !ok {
		return
	}
	if c.Request.Method == http.MethodGet {
		items, err := h.service.Rules(c.Request.Context(), id)
		if err != nil {
			respondError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"rules": items})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	var rule app.Rule
	if !httpdto.BindJSON(c, &rule) {
		return
	}
	item, err := h.service.SaveRule(c.Request.Context(), id, rule, true)
	if err != nil {
		respondError(c, err)
		return
	}
	httpdto.Success(c, item)
}
func (h *Handler) Rule(c *gin.Context) {
	id, ok := user(c)
	if !ok {
		return
	}
	ruleID, ok := parseID(c, "rule")
	if !ok {
		return
	}
	if c.Request.Method == http.MethodDelete {
		if err := h.service.DeleteRule(c.Request.Context(), id, ruleID); err != nil {
			respondError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"deleted": ruleID})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	var rule app.Rule
	if !httpdto.BindJSON(c, &rule) {
		return
	}
	rule.ID = ruleID
	item, err := h.service.SaveRule(c.Request.Context(), id, rule, false)
	if err != nil {
		respondError(c, err)
		return
	}
	httpdto.Success(c, item)
}
func (h *Handler) ToggleRule(c *gin.Context) {
	id, ok := user(c)
	if !ok {
		return
	}
	ruleID, ok := parseID(c, "rule")
	if !ok {
		return
	}
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if input.Enabled == nil {
		respondError(c, app.ErrInvalidInput)
		return
	}
	if err := h.service.ToggleRule(c.Request.Context(), id, ruleID, *input.Enabled); err != nil {
		respondError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"ok": true, "enabled": *input.Enabled})
}
func (h *Handler) Tools(c *gin.Context) {
	id, ok := user(c)
	if !ok {
		return
	}
	if c.Request.Method == http.MethodGet {
		items, err := h.service.EnabledTools(c.Request.Context(), id)
		if err != nil {
			respondError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"enabled_tools": items})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128*1024)
	var input struct {
		EnabledTools []string `json:"enabled_tools"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if input.EnabledTools == nil {
		respondError(c, app.ErrInvalidInput)
		return
	}
	if err := h.service.SetEnabledTools(c.Request.Context(), id, input.EnabledTools); err != nil {
		respondError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"ok": true})
}
func (h *Handler) Pending(c *gin.Context) {
	id, ok := user(c)
	if !ok {
		return
	}
	all := []app.Approval{}
	for page := 1; page <= 50; page++ {
		items, total, err := h.service.List(c.Request.Context(), id, app.Filter{Status: "pending", Page: page, Size: 100})
		if err != nil {
			respondError(c, err)
			return
		}
		all = append(all, items...)
		if len(items) < 100 || int64(len(all)) >= total {
			break
		}
	}
	httpdto.Success(c, gin.H{"pending": all})
}
func filter(c *gin.Context) (app.Filter, bool) {
	f := app.Filter{Status: c.Query("status"), Source: c.Query("decision_source"), Query: c.Query("q"), From: c.Query("from"), To: c.Query("to"), Page: 1, Size: 20}
	for key, dst := range map[string]*int{"page": &f.Page, "size": &f.Size} {
		if raw := c.Query(key); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil {
				httpdto.BadRequest(c, "Invalid pagination")
				return f, false
			}
			*dst = value
		}
	}
	if raw := c.Param("task"); raw != "" {
		var err error
		f.TaskID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || f.TaskID <= 0 {
			httpdto.BadRequest(c, "Invalid task ID")
			return f, false
		}
	}
	return f, true
}
func (h *Handler) List(c *gin.Context) {
	id, ok := user(c)
	if !ok {
		return
	}
	f, ok := filter(c)
	if !ok {
		return
	}
	items, total, err := h.service.List(c.Request.Context(), id, f)
	if err != nil {
		respondError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"items": items, "total": total})
}
func (h *Handler) Get(c *gin.Context) {
	id, ok := user(c)
	if !ok {
		return
	}
	approvalID, ok := parseID(c, "approval")
	if !ok {
		return
	}
	item, err := h.service.Get(c.Request.Context(), id, approvalID)
	if err != nil {
		respondError(c, err)
		return
	}
	httpdto.Success(c, item)
}
func (h *Handler) Decision(c *gin.Context) {
	id, ok := user(c)
	if !ok {
		return
	}
	approvalID, ok := parseID(c, "approval")
	if !ok {
		return
	}
	var input struct {
		Decision string `json:"decision"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if err := h.service.Decide(c.Request.Context(), id, approvalID, input.Decision); err != nil {
		respondError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"ok": true})
}
func (h *Handler) Execution(c *gin.Context) {
	id, ok := user(c)
	if !ok {
		return
	}
	approvalID, ok := parseID(c, "approval")
	if !ok {
		return
	}
	item, err := h.service.Execution(c.Request.Context(), id, approvalID)
	if err != nil {
		respondError(c, err)
		return
	}
	if requested := c.Query("conversation"); requested != "" && (item.ConversationID == nil || *item.ConversationID != requested) {
		httpdto.NotFound(c, "审批执行记录不存在")
		return
	}
	httpdto.Success(c, item)
}

func (h *Handler) ExecutionContext(c *gin.Context) {
	userID, ok := user(c)
	if !ok {
		return
	}
	approvalID, ok := parseID(c, "approval")
	if !ok {
		return
	}
	var before, after int64
	limit := 50
	var err error
	if raw := c.Query("before"); raw != "" {
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || before <= 0 {
			respondError(c, app.ErrInvalidInput)
			return
		}
	}
	if raw := c.Query("after"); raw != "" {
		after, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || after <= 0 {
			respondError(c, app.ErrInvalidInput)
			return
		}
	}
	if raw := c.Query("size"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil {
			respondError(c, app.ErrInvalidInput)
			return
		}
	}
	result, err := h.service.ExecutionContext(c.Request.Context(), userID, approvalID, before, after, limit)
	if err != nil {
		respondError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, result)
}

func (h *Handler) Judge(c *gin.Context) {
	id, ok := user(c)
	if !ok {
		return
	}
	if c.Request.Method == http.MethodGet {
		cfg, err := h.service.JudgeConfig(c.Request.Context(), id)
		if err != nil {
			respondError(c, err)
			return
		}
		httpdto.Success(c, cfg)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 192*1024)
	var cfg app.JudgeConfig
	if !httpdto.BindJSON(c, &cfg) {
		return
	}
	if err := h.service.SaveJudge(c.Request.Context(), id, cfg); err != nil {
		respondError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"ok": true})
}
func (h *Handler) JudgeUsage(c *gin.Context) {
	id, ok := user(c)
	if !ok {
		return
	}
	days := 30
	if raw := c.Query("days"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			respondError(c, app.ErrInvalidInput)
			return
		}
		days = value
	}
	usage, err := h.service.JudgeUsage(c.Request.Context(), id, days)
	if err != nil {
		respondError(c, err)
		return
	}
	httpdto.Success(c, usage)
}

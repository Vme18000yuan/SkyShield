package handler

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

func (h *LLMProfileHandler) SetUsage(facade *settingsapp.LLMUsageFacade) { h.usage = facade }
func usageError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, settingsapp.ErrForbidden):
		httpdto.Forbidden(c, "Account is not an active member")
	case errors.Is(err, settingsapp.ErrInvalidUsageQuery):
		httpdto.BadRequest(c, "Invalid model usage query")
	case errors.Is(err, settingsapp.ErrUsageTaskNotFound):
		httpdto.NotFound(c, "AI task not found")
	default:
		httpdto.InternalError(c, "Could not load model usage")
	}
}
func (h *LLMProfileHandler) Usage(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	if h.usage == nil {
		usageError(c, settingsapp.ErrInvalidUsageQuery)
		return
	}
	days := 30
	if raw := c.Query("days"); raw != "" {
		var err error
		days, err = strconv.Atoi(raw)
		if err != nil {
			usageError(c, settingsapp.ErrInvalidUsageQuery)
			return
		}
	}
	result, err := h.usage.Summary(c.Request.Context(), user, days)
	if err != nil {
		usageError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, result)
}
func (h *LLMProfileHandler) TaskUsage(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	task, err := strconv.ParseInt(c.Param("task"), 10, 64)
	if h.usage == nil || err != nil || task <= 0 {
		usageError(c, settingsapp.ErrInvalidUsageQuery)
		return
	}
	models, err := h.usage.TaskModels(c.Request.Context(), user, task)
	if err != nil {
		usageError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, gin.H{"models": models})
}

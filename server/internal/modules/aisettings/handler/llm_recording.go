package handler

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

func (h *LLMProfileHandler) SetRecordings(facade *settingsapp.LLMRecordingFacade) { h.records = facade }
func (h *LLMProfileHandler) RecordingTasks(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	if h.records == nil {
		usageError(c, settingsapp.ErrInvalidUsageQuery)
		return
	}
	result, err := h.records.Tasks(c.Request.Context(), user, c.Query("q"))
	if err != nil {
		usageError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, result)
}
func (h *LLMProfileHandler) Recordings(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	if h.records == nil {
		usageError(c, settingsapp.ErrInvalidUsageQuery)
		return
	}
	filter := settingsapp.LLMRecordingFilter{Size: 25, Model: strings.TrimSpace(c.Query("model")), Session: strings.TrimSpace(c.Query("session"))}
	for _, field := range []struct {
		name string
		dest *int
	}{{"page", &filter.Page}, {"size", &filter.Size}} {
		if raw := c.Query(field.name); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil {
				usageError(c, settingsapp.ErrInvalidUsageQuery)
				return
			}
			*field.dest = value
		}
	}
	if raw := c.Query("task"); raw != "" {
		var err error
		filter.TaskID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || filter.TaskID <= 0 {
			usageError(c, settingsapp.ErrInvalidUsageQuery)
			return
		}
	}
	if c.Request.Method == "DELETE" {
		deleted, err := h.records.DeleteTask(c.Request.Context(), user, filter.TaskID)
		if err != nil {
			usageError(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		httpdto.Success(c, gin.H{"deleted": deleted})
		return
	}
	result, err := h.records.List(c.Request.Context(), user, filter)
	if err != nil {
		usageError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, result)
}
func (h *LLMProfileHandler) Recording(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	if h.records == nil {
		usageError(c, settingsapp.ErrInvalidUsageQuery)
		return
	}
	result, err := h.records.Detail(c.Request.Context(), user, c.Param("record"))
	if err != nil {
		usageError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, result)
}

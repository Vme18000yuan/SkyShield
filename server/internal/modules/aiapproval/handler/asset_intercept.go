package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	app "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

func assetRuleTask(c *gin.Context) (int64, bool) {
	if c.Param("task") == "" {
		return 0, true
	}
	return parseID(c, "task")
}

func (h *Handler) AssetRules(c *gin.Context) {
	userID, ok := user(c)
	if !ok {
		return
	}
	taskID, ok := assetRuleTask(c)
	if !ok {
		return
	}
	if c.Request.Method == http.MethodGet {
		items, err := h.service.AssetRules(c.Request.Context(), userID, taskID)
		if err != nil {
			respondError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"rules": items})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	var input app.AssetRule
	if !httpdto.BindJSON(c, &input) {
		return
	}
	item, err := h.service.SaveAssetRule(c.Request.Context(), userID, taskID, input, true)
	if err != nil {
		respondError(c, err)
		return
	}
	httpdto.Success(c, item)
}

func (h *Handler) AssetRule(c *gin.Context) {
	userID, ok := user(c)
	if !ok {
		return
	}
	taskID, ok := assetRuleTask(c)
	if !ok {
		return
	}
	id, ok := parseID(c, "rule")
	if !ok {
		return
	}
	if c.Request.Method == http.MethodDelete {
		if err := h.service.DeleteAssetRule(c.Request.Context(), userID, taskID, id); err != nil {
			respondError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"ok": true})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	var input app.AssetRule
	if !httpdto.BindJSON(c, &input) {
		return
	}
	input.ID = id
	item, err := h.service.SaveAssetRule(c.Request.Context(), userID, taskID, input, false)
	if err != nil {
		respondError(c, err)
		return
	}
	httpdto.Success(c, item)
}

func (h *Handler) ToggleAssetRule(c *gin.Context) {
	userID, ok := user(c)
	if !ok {
		return
	}
	taskID, ok := assetRuleTask(c)
	if !ok {
		return
	}
	id, ok := parseID(c, "rule")
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
	if err := h.service.ToggleAssetRule(c.Request.Context(), userID, taskID, id, *input.Enabled); err != nil {
		respondError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"ok": true, "enabled": *input.Enabled})
}

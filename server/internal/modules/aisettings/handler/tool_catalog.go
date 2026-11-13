package handler

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Autumn-27/norma/tool"
	"github.com/gin-gonic/gin"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
	"net/http"
)

type toolCatalogApplication interface {
	List(context.Context, int) ([]settings.ToolRecord, error)
	Save(context.Context, int, string, settings.ToolRecord, bool) (settings.ToolRecord, error)
	Delete(context.Context, int, string) error
	Reset(context.Context, int, string) error
	Test(context.Context, int, string, json.RawMessage, map[string]any) (tool.Result, error)
}
type ToolCatalogHandler struct{ app toolCatalogApplication }

func (h *ToolCatalogHandler) Test(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
	var input struct {
		Kind   string          `json:"kind"`
		Exec   json.RawMessage `json:"exec"`
		Params map[string]any  `json:"params"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	result, err := h.app.Test(c.Request.Context(), user, input.Kind, input.Exec, input.Params)
	if err != nil {
		toolCatalogError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"output": result.Flatten(), "is_error": result.IsError})
}

func NewToolCatalogHandler(app toolCatalogApplication) *ToolCatalogHandler {
	return &ToolCatalogHandler{app: app}
}
func toolCatalogError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, settings.ErrToolCatalogInvalid):
		httpdto.BadRequest(c, err.Error())
	case errors.Is(err, settings.ErrToolCatalogNotFound):
		httpdto.NotFound(c, err.Error())
	default:
		profileError(c, err)
	}
}
func (h *ToolCatalogHandler) List(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	items, err := h.app.List(c.Request.Context(), user)
	if err != nil {
		toolCatalogError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"tools": items})
}
func (h *ToolCatalogHandler) Save(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
	var input settings.ToolRecord
	if !httpdto.BindJSON(c, &input) {
		return
	}
	create := c.Request.Method == http.MethodPost
	key := c.Param("tool")
	if create {
		key = input.Key
	}
	item, err := h.app.Save(c.Request.Context(), user, key, input, create)
	if err != nil {
		toolCatalogError(c, err)
		return
	}
	httpdto.Success(c, item)
}
func (h *ToolCatalogHandler) Reset(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	if err := h.app.Reset(c.Request.Context(), user, c.Param("tool")); err != nil {
		toolCatalogError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"ok": true})
}
func (h *ToolCatalogHandler) Delete(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	if err := h.app.Delete(c.Request.Context(), user, c.Param("tool")); err != nil {
		toolCatalogError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"deleted": 1})
}

package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type toolSettings interface {
	List(context.Context, int) ([]settingsapp.ToolVisibility, error)
	Save(context.Context, int, settingsapp.ToolVisibility) error
}
type ToolSettingHandler struct{ settings toolSettings }

func NewToolSettingHandler(settings toolSettings) *ToolSettingHandler {
	return &ToolSettingHandler{settings: settings}
}
func toolSettingDTO(item settingsapp.ToolVisibility) gin.H {
	return gin.H{"name": httpdto.AIToolSettingName(item.AgentKey, item.ToolKey), "agentKey": item.AgentKey, "toolKey": item.ToolKey, "enabled": item.Enabled}
}

// List returns administrator-controlled platform tool visibility.
// GET /v1/aiToolSettings
func (h *ToolSettingHandler) List(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	items, err := h.settings.List(c.Request.Context(), user)
	if err != nil {
		profileError(c, err)
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, item := range items {
		out = append(out, toolSettingDTO(item))
	}
	httpdto.Success(c, gin.H{"tools": out})
}

// Update sets one Agent's access to one platform tool.
// PUT /v1/aiToolSettings/:agent/:tool
func (h *ToolSettingHandler) Update(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if input.Enabled == nil {
		httpdto.BadRequest(c, "Tool visibility is required")
		return
	}
	item := settingsapp.ToolVisibility{AgentKey: c.Param("agent"), ToolKey: c.Param("tool"), Enabled: *input.Enabled}
	if err := h.settings.Save(c.Request.Context(), user, item); err != nil {
		if err == settingsapp.ErrInvalidProfile {
			httpdto.BadRequest(c, "Invalid agent or tool")
		} else {
			profileError(c, err)
		}
		return
	}
	httpdto.Success(c, toolSettingDTO(item))
}

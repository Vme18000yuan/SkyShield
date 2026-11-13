package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type agentSettings interface {
	List(context.Context, int) ([]airuntime.AgentConfig, error)
	Save(context.Context, int, airuntime.AgentConfig) error
}
type AgentSettingHandler struct{ settings agentSettings }

func NewAgentSettingHandler(settings agentSettings) *AgentSettingHandler {
	return &AgentSettingHandler{settings: settings}
}
func agentSettingDTO(c airuntime.AgentConfig) gin.H {
	return gin.H{"name": httpdto.AIAgentSettingName(c.Key), "key": c.Key, "enabled": c.Enabled, "maxTurns": c.MaxTurns, "runSeconds": c.RunSeconds, "additionalPrompt": c.AdditionalPrompt}
}

// List exposes built-in Agent configuration to administrators.
// GET /v1/aiAgentSettings
func (h *AgentSettingHandler) List(c *gin.Context) {
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
		out = append(out, agentSettingDTO(item))
	}
	httpdto.Success(c, gin.H{"agents": out})
}

// Update changes the configuration used by subsequent Agent runs.
// PUT /v1/aiAgentSettings/:agent
func (h *AgentSettingHandler) Update(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 40*1024)
	var input struct {
		Enabled          *bool  `json:"enabled"`
		MaxTurns         int    `json:"maxTurns"`
		RunSeconds       int    `json:"runSeconds"`
		AdditionalPrompt string `json:"additionalPrompt"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if input.Enabled == nil {
		httpdto.BadRequest(c, "Agent enabled state is required")
		return
	}
	config := airuntime.AgentConfig{Key: c.Param("agent"), Enabled: *input.Enabled, MaxTurns: input.MaxTurns, RunSeconds: input.RunSeconds, AdditionalPrompt: input.AdditionalPrompt}
	if err := h.settings.Save(c.Request.Context(), user, config); err != nil {
		if err == settingsapp.ErrInvalidProfile {
			httpdto.BadRequest(c, "Invalid agent configuration")
		} else {
			profileError(c, err)
		}
		return
	}
	httpdto.Success(c, agentSettingDTO(config))
}

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Autumn-27/artex/agent"
	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type agentCatalogApplication interface {
	List(context.Context, int) ([]airuntime.ChatAgentDefinition, error)
	Get(context.Context, int, string) (*airuntime.ChatAgentDefinition, error)
	Create(context.Context, int, airuntime.ChatAgentDefinition) (*airuntime.ChatAgentDefinition, error)
	Update(context.Context, int, string, airuntime.ChatAgentDefinition) (*airuntime.ChatAgentDefinition, error)
	Delete(context.Context, int, string) error
	PromptVersions(context.Context, int, string) ([]settingsapp.AgentPromptVersion, error)
	PreviewPrompt(context.Context, int, string, string) (string, error)
}

type AgentCatalogHandler struct{ agents agentCatalogApplication }

func NewAgentCatalogHandler(agents agentCatalogApplication) *AgentCatalogHandler {
	if agents == nil {
		panic("AI agent catalog application is required")
	}
	return &AgentCatalogHandler{agents: agents}
}

func agentCatalogDTO(item airuntime.ChatAgentDefinition, includePrompt bool) gin.H {
	result := gin.H{
		"key": item.Key, "name": item.Name, "description": item.Description, "role": item.Role, "builtin": item.Builtin,
		"enabled": item.Enabled, "maxTurns": item.MaxTurns, "runSeconds": item.RunSeconds,
		"llmProfileId": item.LLMProfileID,
		"wrapupPrompt": item.WrapupPrompt, "wrapupMaxTurns": item.WrapupMaxTurns,
		"taskTimeoutPrompt": item.TaskTimeoutPrompt, "taskTimeoutMaxTurns": item.TaskTimeoutMaxTurns,
		"options": item.Options, "mcpCount": item.MCPCount, "skillCount": item.SkillCount, "toolCount": item.ToolCount,
	}
	if includePrompt {
		result["systemPrompt"] = item.SystemPrompt
		result["defaultWrapupPrompt"] = agent.WrapupDefault(item.Key)
		result["defaultWrapupMaxTurns"] = agent.WrapupTurnsDefault(item.Key)
		result["defaultTaskTimeoutPrompt"] = agent.TaskTimeoutWrapupDefault(item.Key)
		if builtin, ok := airuntime.BuiltinChatAgent(item.Key); ok {
			result["defaultSystemPrompt"] = builtin.PromptTemplate
		}
	}
	return result
}

func (handler *AgentCatalogHandler) List(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	items, err := handler.agents.List(c.Request.Context(), user)
	if err != nil {
		agentCatalogError(c, err)
		return
	}
	output := make([]gin.H, 0, len(items))
	for _, item := range items {
		output = append(output, agentCatalogDTO(item, false))
	}
	httpdto.Success(c, gin.H{"agents": output})
}

func (handler *AgentCatalogHandler) Get(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	item, err := handler.agents.Get(c.Request.Context(), user, c.Param("agent"))
	if err != nil {
		agentCatalogError(c, err)
		return
	}
	httpdto.Success(c, agentCatalogDTO(*item, true))
}

func (handler *AgentCatalogHandler) Create(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 384*1024)
	var input struct {
		Key          string `json:"key"`
		Name         string `json:"name"`
		Description  string `json:"description"`
		SystemPrompt string `json:"systemPrompt"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	item, err := handler.agents.Create(c.Request.Context(), user, airuntime.ChatAgentDefinition{
		Key: input.Key, Name: input.Name, Description: input.Description, SystemPrompt: input.SystemPrompt,
	})
	if err != nil {
		agentCatalogError(c, err)
		return
	}
	httpdto.Created(c, agentCatalogDTO(*item, true))
}

func (handler *AgentCatalogHandler) Update(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 384*1024)
	var input struct {
		Name                string          `json:"name"`
		Description         string          `json:"description"`
		SystemPrompt        string          `json:"systemPrompt"`
		Enabled             *bool           `json:"enabled"`
		MaxTurns            *int            `json:"maxTurns"`
		RunSeconds          *int            `json:"runSeconds"`
		LLMProfileID        json.RawMessage `json:"llmProfileId"`
		WrapupPrompt        *string         `json:"wrapupPrompt"`
		WrapupMaxTurns      *int            `json:"wrapupMaxTurns"`
		TaskTimeoutPrompt   *string         `json:"taskTimeoutPrompt"`
		TaskTimeoutMaxTurns *int            `json:"taskTimeoutMaxTurns"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if input.Enabled == nil || input.MaxTurns == nil || input.RunSeconds == nil {
		httpdto.BadRequest(c, "Enabled, maxTurns, and runSeconds are required")
		return
	}
	definition := airuntime.ChatAgentDefinition{
		Name: input.Name, Description: input.Description, SystemPrompt: input.SystemPrompt,
		Enabled: *input.Enabled, MaxTurns: *input.MaxTurns, RunSeconds: *input.RunSeconds,
	}
	if input.LLMProfileID != nil {
		definition.UpdateLLMBinding = true
		if err := json.Unmarshal(input.LLMProfileID, &definition.LLMProfileID); err != nil {
			httpdto.BadRequest(c, "Invalid llmProfileId")
			return
		}
	}
	if input.WrapupPrompt != nil || input.WrapupMaxTurns != nil || input.TaskTimeoutPrompt != nil || input.TaskTimeoutMaxTurns != nil {
		if input.WrapupPrompt == nil || input.WrapupMaxTurns == nil || input.TaskTimeoutPrompt == nil || input.TaskTimeoutMaxTurns == nil {
			httpdto.BadRequest(c, "All wrapup fields are required together")
			return
		}
		definition.UpdateWrapup = true
		definition.WrapupPrompt, definition.WrapupMaxTurns = *input.WrapupPrompt, *input.WrapupMaxTurns
		definition.TaskTimeoutPrompt, definition.TaskTimeoutMaxTurns = *input.TaskTimeoutPrompt, *input.TaskTimeoutMaxTurns
	}
	item, err := handler.agents.Update(c.Request.Context(), user, c.Param("agent"), definition)
	if err != nil {
		agentCatalogError(c, err)
		return
	}
	httpdto.Success(c, agentCatalogDTO(*item, true))
}

func (handler *AgentCatalogHandler) Delete(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	if err := handler.agents.Delete(c.Request.Context(), user, c.Param("agent")); err != nil {
		agentCatalogError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"deleted": true})
}

func (handler *AgentCatalogHandler) PromptVersions(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	items, err := handler.agents.PromptVersions(c.Request.Context(), user, c.Param("agent"))
	if err != nil {
		agentCatalogError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"versions": items})
}

func (handler *AgentCatalogHandler) PreviewPrompt(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 96*1024)
	var input struct {
		Template string `json:"template"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	rendered, err := handler.agents.PreviewPrompt(c.Request.Context(), user, c.Param("agent"), input.Template)
	if err != nil {
		agentCatalogError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"rendered": rendered})
}

func agentCatalogError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, settingsapp.ErrForbidden):
		httpdto.Forbidden(c, "AI agent catalog management requires an active administrator")
	case errors.Is(err, settingsapp.ErrAgentCatalogInvalid):
		httpdto.BadRequest(c, "Invalid AI agent definition")
	case errors.Is(err, settingsapp.ErrAgentCatalogNotFound):
		httpdto.NotFound(c, "AI agent not found")
	case errors.Is(err, settingsapp.ErrAgentCatalogConflict):
		httpdto.Conflict(c, "AI agent key already exists")
	case errors.Is(err, settingsapp.ErrAgentCatalogBuiltin):
		httpdto.BadRequest(c, "Built-in AI agents cannot be changed or deleted")
	default:
		profileError(c, err)
	}
}

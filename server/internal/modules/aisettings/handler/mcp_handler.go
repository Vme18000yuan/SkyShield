package handler

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"net/http"

	"github.com/gin-gonic/gin"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type mcpSettings interface {
	List(context.Context, int) ([]settingsapp.MCPServer, error)
	Save(context.Context, int, string, settingsapp.MCPInput) (*settingsapp.MCPServer, error)
	Delete(context.Context, int, string) error
	Test(context.Context, int, string) ([]settingsapp.MCPTool, error)
}
type MCPHandler struct{ settings mcpSettings }

func NewMCPHandler(settings mcpSettings) *MCPHandler { return &MCPHandler{settings: settings} }
func mcpDTO(item settingsapp.MCPServer) gin.H {
	names := []string{}
	for _, item := range item.Tools {
		names = append(names, item.Name)
	}
	return gin.H{"transport": item.Transport, "command": item.Command, "args": item.Args, "hasEnv": item.HasEnv, "tools": names, "id": item.ID, "name": httpdto.AIMCPServerName(item.ID), "displayName": item.Name, "endpoint": item.Endpoint, "enabled": item.Enabled, "insecure": item.Insecure, "hasHeaders": item.HasHeaders, "agents": item.Agents, "createdAt": item.CreatedAt, "updatedAt": item.UpdatedAt}
}
func mcpError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, settingsapp.ErrInvalidProfile):
		httpdto.BadRequest(c, "Invalid MCP configuration")
	case errors.Is(err, settingsapp.ErrProfileNotFound):
		httpdto.NotFound(c, "MCP server not found")
	case errors.Is(err, settingsapp.ErrProfileConflict):
		httpdto.Conflict(c, "MCP server name already exists")
	case errors.Is(err, settingsapp.ErrMCPUnavailable):
		httpdto.ErrorWithStatus(c, http.StatusBadGateway, "MCP_UNAVAILABLE", "UNAVAILABLE", "MCP server connection failed")
	default:
		profileError(c, err)
	}
}

// List returns remote MCP configuration to administrators without credentials.
// GET /v1/aiMcpServers
func (h *MCPHandler) List(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	items, err := h.settings.List(c.Request.Context(), user)
	if err != nil {
		mcpError(c, err)
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, item := range items {
		out = append(out, mcpDTO(item))
	}
	httpdto.Success(c, gin.H{"servers": out})
}
func (h *MCPHandler) save(c *gin.Context, id string) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 300*1024)
	var input struct {
		Transport    string            `json:"transport"`
		Command      string            `json:"command"`
		Args         []string          `json:"args"`
		Env          json.RawMessage   `json:"env"`
		DisplayName  string            `json:"displayName"`
		Endpoint     string            `json:"endpoint"`
		Enabled      *bool             `json:"enabled"`
		Insecure     bool              `json:"insecure"`
		Headers      map[string]string `json:"headers"`
		ClearHeaders bool              `json:"clearHeaders"`
		Agents       *[]string         `json:"agents"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if input.Enabled == nil {
		httpdto.BadRequest(c, "MCP enabled state is required")
		return
	}
	var env map[string]string
	if input.Env != nil && json.Unmarshal(input.Env, &env) != nil {
		mcpError(c, settingsapp.ErrInvalidProfile)
		return
	}
	agents := []string{}
	if input.Agents != nil {
		agents = *input.Agents
	}
	item, err := h.settings.Save(c.Request.Context(), user, id, settingsapp.MCPInput{Transport: input.Transport, Command: input.Command, Args: input.Args, Env: env, ReplaceEnv: input.Env != nil, Name: input.DisplayName, Endpoint: input.Endpoint, Enabled: *input.Enabled, Insecure: input.Insecure, Headers: input.Headers, ClearHeaders: input.ClearHeaders, Agents: agents, PreserveAgents: input.Agents == nil})
	if err != nil {
		mcpError(c, err)
		return
	}
	if id == "" {
		httpdto.Created(c, mcpDTO(*item))
	} else {
		httpdto.Success(c, mcpDTO(*item))
	}
}

// Create stores a remote MCP server.
// POST /v1/aiMcpServers
func (h *MCPHandler) Create(c *gin.Context) { h.save(c, "") }

// Update changes a remote MCP server while retaining omitted credentials.
// PUT /v1/aiMcpServers/:server
func (h *MCPHandler) Update(c *gin.Context) { h.save(c, c.Param("server")) }

// Delete removes a remote MCP server and its Agent bindings.
// DELETE /v1/aiMcpServers/:server
func (h *MCPHandler) Delete(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	if err := h.settings.Delete(c.Request.Context(), user, c.Param("server")); err != nil {
		mcpError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"deleted": true})
}

// Test performs a bounded MCP handshake and lists remote tools.
// POST /v1/aiMcpServers/:server/test
func (h *MCPHandler) Test(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	items, err := h.settings.Test(c.Request.Context(), user, c.Param("server"))
	if err != nil {
		mcpError(c, err)
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, item := range items {
		out = append(out, gin.H{"name": item.Name, "description": item.Description})
	}
	httpdto.Success(c, gin.H{"tools": out})
}

type mcpControlApplication interface {
	Details(context.Context, int, string) (airuntime.MCPConfig, error)
	CachedTools(context.Context, int, string) ([]settingsapp.MCPTool, error)
	Visibility(context.Context, int, string) ([]string, error)
	SetVisible(context.Context, int, string, string, bool) error
}

func (h *MCPHandler) Details(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	control, ok := h.settings.(mcpControlApplication)
	if !ok {
		mcpError(c, settingsapp.ErrInvalidProfile)
		return
	}
	config, err := control.Details(c.Request.Context(), user, c.Param("server"))
	if err != nil {
		mcpError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"env": config.Env})
}
func (h *MCPHandler) Tools(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	control, ok := h.settings.(mcpControlApplication)
	if !ok {
		mcpError(c, settingsapp.ErrInvalidProfile)
		return
	}
	items, err := control.CachedTools(c.Request.Context(), user, c.Param("server"))
	if err != nil {
		mcpError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"tools": items})
}
func (h *MCPHandler) Visibility(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	control, ok := h.settings.(mcpControlApplication)
	if !ok {
		mcpError(c, settingsapp.ErrInvalidProfile)
		return
	}
	if c.Request.Method == http.MethodGet {
		items, err := control.Visibility(c.Request.Context(), user, c.Param("server"))
		if err != nil {
			mcpError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"agents": items})
		return
	}
	var input struct {
		Agent   string `json:"agent"`
		Visible bool   `json:"visible"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if err := control.SetVisible(c.Request.Context(), user, c.Param("server"), input.Agent, input.Visible); err != nil {
		mcpError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"ok": true})
}

func (h *MCPHandler) Enabled(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	app, ok := h.settings.(interface {
		SetEnabled(context.Context, int, string, bool) error
	})
	if !ok {
		mcpError(c, settingsapp.ErrInvalidProfile)
		return
	}
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if input.Enabled == nil {
		mcpError(c, settingsapp.ErrInvalidProfile)
		return
	}
	if err := app.SetEnabled(c.Request.Context(), user, c.Param("server"), *input.Enabled); err != nil {
		mcpError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"ok": true})
}

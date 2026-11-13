package handler

import (
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/gin-gonic/gin"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
	"net/http"
	"strconv"
)

type AgentControlHandler struct{ control *settings.AgentControlFacade }

func NewAgentControlHandler(control *settings.AgentControlFacade) *AgentControlHandler {
	return &AgentControlHandler{control: control}
}
func controlAgentKey(c *gin.Context) string {
	key := c.Param("agent")
	if key == "assistant" {
		return "mainagent"
	}
	return key
}
func (h *AgentControlHandler) Details(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	detail, err := h.control.Details(c.Request.Context(), user, controlAgentKey(c))
	if err != nil {
		agentCatalogError(c, err)
		return
	}
	httpdto.Success(c, detail)
}
func (h *AgentControlHandler) Config(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	var input settings.AgentConfigPatch
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if err := h.control.PatchConfig(c.Request.Context(), user, controlAgentKey(c), input); err != nil {
		agentCatalogError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"ok": true})
}
func (h *AgentControlHandler) SavePrompt(c *gin.Context)  { h.prompt(c, false) }
func (h *AgentControlHandler) ResetPrompt(c *gin.Context) { h.prompt(c, true) }
func (h *AgentControlHandler) prompt(c *gin.Context, reset bool) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 96*1024)
	var input struct {
		Template string `json:"template"`
	}
	if !reset && !httpdto.BindJSON(c, &input) {
		return
	}
	version, err := h.control.SavePrompt(c.Request.Context(), user, controlAgentKey(c), input.Template, reset)
	if err != nil {
		agentCatalogError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"version": version})
}
func (h *AgentControlHandler) Preview(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 96*1024)
	var input struct {
		Template string            `json:"template"`
		Sample   map[string]string `json:"sample"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	rendered, err := h.control.Preview(c.Request.Context(), user, controlAgentKey(c), input.Template, input.Sample)
	if err != nil {
		agentCatalogError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"rendered": rendered})
}
func (h *AgentControlHandler) Wrapup(c *gin.Context)          { h.wrapup(c, false, false) }
func (h *AgentControlHandler) ResetWrapup(c *gin.Context)     { h.wrapup(c, false, true) }
func (h *AgentControlHandler) TaskWrapup(c *gin.Context)      { h.wrapup(c, true, false) }
func (h *AgentControlHandler) ResetTaskWrapup(c *gin.Context) { h.wrapup(c, true, true) }
func (h *AgentControlHandler) wrapup(c *gin.Context, timeout, reset bool) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 96*1024)
	var input struct {
		Prompt string `json:"prompt"`
		Turns  int    `json:"max_turns"`
	}
	if !reset && !httpdto.BindJSON(c, &input) {
		return
	}
	if err := h.control.SaveWrapup(c.Request.Context(), user, controlAgentKey(c), timeout, input.Prompt, input.Turns); err != nil {
		agentCatalogError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"ok": true})
}
func (h *AgentControlHandler) Visibility(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	value, err := h.control.Visibility(c.Request.Context(), user, controlAgentKey(c))
	if err != nil {
		agentCatalogError(c, err)
		return
	}
	httpdto.Success(c, value)
}
func (h *AgentControlHandler) SaveVisibility(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128*1024)
	var input settings.AgentVisibility
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if err := h.control.SetVisibility(c.Request.Context(), user, controlAgentKey(c), input); err != nil {
		agentCatalogError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"ok": true})
}
func (h *AgentControlHandler) Triggers(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	rows, err := h.control.Triggers(c.Request.Context(), user, controlAgentKey(c))
	if err != nil {
		agentCatalogError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"triggers": rows})
}
func (h *AgentControlHandler) CreateTrigger(c *gin.Context) { h.trigger(c, true) }
func (h *AgentControlHandler) UpdateTrigger(c *gin.Context) { h.trigger(c, false) }
func (h *AgentControlHandler) trigger(c *gin.Context, create bool) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256*1024)
	var input artexdb.AgentTrigger
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if !create {
		id, err := strconv.ParseInt(c.Param("trigger"), 10, 64)
		if err != nil || id <= 0 {
			agentCatalogError(c, settings.ErrAgentCatalogInvalid)
			return
		}
		input.ID = id
	}
	saved, err := h.control.SaveTrigger(c.Request.Context(), user, controlAgentKey(c), input, create)
	if err != nil {
		agentCatalogError(c, err)
		return
	}
	httpdto.Success(c, saved)
}
func (h *AgentControlHandler) DeleteTrigger(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("trigger"), 10, 64)
	if err != nil || id <= 0 {
		agentCatalogError(c, settings.ErrAgentCatalogInvalid)
		return
	}
	if err := h.control.DeleteTrigger(c.Request.Context(), user, id); err != nil {
		agentCatalogError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"deleted": id})
}

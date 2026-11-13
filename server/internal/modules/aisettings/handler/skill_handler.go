package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type skills interface {
	List(context.Context, int) ([]settingsapp.SkillRecord, error)
	Save(context.Context, int, string, settingsapp.SkillRecord) (*settingsapp.SkillRecord, error)
	Delete(context.Context, int, string) error
}
type SkillHandler struct{ skills skills }

func NewSkillHandler(skills skills) *SkillHandler { return &SkillHandler{skills: skills} }
func skillDTO(item settingsapp.SkillRecord) gin.H {
	return gin.H{"id": item.ID, "name": httpdto.AISkillName(item.ID), "displayName": item.Name, "description": item.Description, "instructions": item.Instructions, "enabled": item.Enabled, "agents": item.Agents, "builtin": item.BuiltinKey != "", "createdAt": item.CreatedAt, "updatedAt": item.UpdatedAt}
}
func skillError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, settingsapp.ErrBuiltinSkillProtected):
		httpdto.BadRequest(c, "Built-in skills cannot be deleted or have their source content changed")
	case errors.Is(err, settingsapp.ErrInvalidProfile):
		httpdto.BadRequest(c, "Invalid skill configuration")
	case errors.Is(err, settingsapp.ErrProfileNotFound):
		httpdto.NotFound(c, "Skill not found")
	case errors.Is(err, settingsapp.ErrProfileConflict):
		httpdto.Conflict(c, "Skill name already exists")
	default:
		profileError(c, err)
	}
}

// List returns text skills to administrators.
// GET /v1/aiSkills
func (h *SkillHandler) List(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	items, err := h.skills.List(c.Request.Context(), user)
	if err != nil {
		skillError(c, err)
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, item := range items {
		out = append(out, skillDTO(item))
	}
	httpdto.Success(c, gin.H{"skills": out})
}
func (h *SkillHandler) save(c *gin.Context, id string) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 80*1024)
	var input struct {
		DisplayName  string   `json:"displayName"`
		Description  string   `json:"description"`
		Instructions string   `json:"instructions"`
		Enabled      *bool    `json:"enabled"`
		Agents       []string `json:"agents"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if input.Enabled == nil {
		httpdto.BadRequest(c, "Skill enabled state is required")
		return
	}
	item, err := h.skills.Save(c.Request.Context(), user, id, settingsapp.SkillRecord{Name: input.DisplayName, Description: input.Description, Instructions: input.Instructions, Enabled: *input.Enabled, Agents: input.Agents})
	if err != nil {
		skillError(c, err)
		return
	}
	if id == "" {
		httpdto.Created(c, skillDTO(*item))
	} else {
		httpdto.Success(c, skillDTO(*item))
	}
}

// Create creates a text instruction skill.
// POST /v1/aiSkills
func (h *SkillHandler) Create(c *gin.Context) { h.save(c, "") }

// Update modifies a text instruction skill.
// PUT /v1/aiSkills/:skill
func (h *SkillHandler) Update(c *gin.Context) { h.save(c, c.Param("skill")) }

// Delete deletes a skill and its Agent bindings.
// DELETE /v1/aiSkills/:skill
func (h *SkillHandler) Delete(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	if err := h.skills.Delete(c.Request.Context(), user, c.Param("skill")); err != nil {
		skillError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"deleted": true})
}

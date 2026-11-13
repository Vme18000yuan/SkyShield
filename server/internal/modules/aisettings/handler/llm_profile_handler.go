package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type llmProfiles interface {
	List(context.Context, int) ([]settingsapp.LLMProfile, error)
	ListForMember(context.Context, int) ([]settingsapp.LLMProfile, error)
	Save(context.Context, int, string, settingsapp.ProfileInput) (*settingsapp.LLMProfile, error)
	Delete(context.Context, int, string) error
	Test(context.Context, int, string) (time.Duration, string, error)
}

func (h *LLMProfileHandler) ListModels(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	profiles, err := h.profiles.ListForMember(c.Request.Context(), user)
	if err != nil {
		profileError(c, err)
		return
	}
	items := make([]gin.H, 0, len(profiles))
	for _, p := range profiles {
		items = append(items, gin.H{"id": p.ID, "displayName": p.Name, "model": p.Model, "provider": p.Provider, "isDefault": p.IsDefault})
	}
	httpdto.Success(c, gin.H{"models": items})
}

type LLMProfileHandler struct {
	profiles llmProfiles
	pool     settingsapp.LLMPoolController
	usage    *settingsapp.LLMUsageFacade
	records  *settingsapp.LLMRecordingFacade
}

func NewLLMProfileHandler(profiles llmProfiles, pools ...settingsapp.LLMPoolController) *LLMProfileHandler {
	h := &LLMProfileHandler{profiles: profiles}
	if len(pools) > 0 {
		h.pool = pools[0]
	}
	return h
}

type llmProfileResponse struct {
	Options     settingsapp.LLMOptions `json:"options"`
	APIKeyHint  string                 `json:"apiKeyHint"`
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	DisplayName string                 `json:"displayName"`
	Provider    string                 `json:"provider"`
	Model       string                 `json:"model"`
	BaseURL     string                 `json:"baseUrl"`
	IsDefault   bool                   `json:"isDefault"`
	HasAPIKey   bool                   `json:"hasApiKey"`
	CreatedAt   time.Time              `json:"createdAt"`
	UpdatedAt   time.Time              `json:"updatedAt"`
}

func profileDTO(profile settingsapp.LLMProfile) llmProfileResponse {
	return llmProfileResponse{ID: profile.ID, Name: httpdto.AILlmProfileName(profile.ID), DisplayName: profile.Name, Provider: profile.Provider, Model: profile.Model,
		BaseURL: profile.BaseURL, IsDefault: profile.IsDefault, HasAPIKey: profile.HasAPIKey || len(profile.EncryptedKey) > 0, Options: profile.Options, APIKeyHint: profile.APIKeyHint, CreatedAt: profile.CreatedAt.UTC(), UpdatedAt: profile.UpdatedAt.UTC()}
}

func settingsUser(c *gin.Context) (int, bool) {
	user, ok := middleware.GetUserID(c)
	if !ok {
		httpdto.Unauthorized(c, "Not authenticated")
	}
	return user, ok
}

func profileError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, settingsapp.ErrForbidden):
		httpdto.Forbidden(c, "Administrator permission required")
	case errors.Is(err, settingsapp.ErrInvalidProfile):
		httpdto.BadRequest(c, "Invalid model configuration")
	case errors.Is(err, settingsapp.ErrProfileNotFound):
		httpdto.NotFound(c, "Model configuration not found")
	case errors.Is(err, settingsapp.ErrProfileConflict):
		httpdto.Conflict(c, "Model configuration name already exists")
	case errors.Is(err, settingsapp.ErrProfileActive):
		httpdto.Conflict(c, "当前激活的 LLM 配置不能删除，请先激活其他配置")
	case errors.Is(err, airuntime.ErrEncryptionKeyUnavailable):
		httpdto.ErrorWithStatus(c, http.StatusServiceUnavailable, "AI_ENCRYPTION_KEY_REQUIRED", "UNAVAILABLE", "AI encryption key is not configured")
	default:
		httpdto.InternalError(c, "Failed to manage model configuration")
	}
}

// List returns model settings only to tenant administrators.
// GET /v1/aiLlmProfiles
func (h *LLMProfileHandler) List(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	profiles, err := h.profiles.List(c.Request.Context(), user)
	if err != nil {
		profileError(c, err)
		return
	}
	items := make([]llmProfileResponse, 0, len(profiles))
	for _, p := range profiles {
		items = append(items, profileDTO(p))
	}
	httpdto.Success(c, gin.H{"profiles": items})
}

// Create saves an encrypted model credential.
// POST /v1/aiLlmProfiles
func (h *LLMProfileHandler) Create(c *gin.Context) {
	h.save(c, "")
}

// Update changes a model profile; an empty key retains its existing credential.
// PUT /v1/aiLlmProfiles/:profile
func (h *LLMProfileHandler) Update(c *gin.Context) {
	h.save(c, c.Param("profile"))
}

func (h *LLMProfileHandler) save(c *gin.Context, id string) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	var input settingsapp.ProfileInput
	if !httpdto.BindJSON(c, &input) {
		return
	}
	profile, err := h.profiles.Save(c.Request.Context(), user, id, input)
	if err != nil {
		profileError(c, err)
		return
	}
	if id == "" {
		httpdto.Created(c, profileDTO(*profile))
		return
	}
	httpdto.Success(c, profileDTO(*profile))
}

// Delete removes one model configuration.
// DELETE /v1/aiLlmProfiles/:profile
func (h *LLMProfileHandler) Delete(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	if err := h.profiles.Delete(c.Request.Context(), user, c.Param("profile")); err != nil {
		profileError(c, err)
		return
	}
	httpdto.NoContent(c)
}

// Test performs a bounded model connectivity check for an administrator.
// POST /v1/aiLlmProfiles/:profile/test
func (h *LLMProfileHandler) Test(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	latency, reply, err := h.profiles.Test(c.Request.Context(), user, c.Param("profile"))
	if err != nil {
		profileError(c, err)
		return
	}
	if len([]rune(reply)) > 500 {
		reply = string([]rune(reply)[:500])
	}
	httpdto.Success(c, gin.H{"latencyMs": latency.Milliseconds(), "reply": reply})
}

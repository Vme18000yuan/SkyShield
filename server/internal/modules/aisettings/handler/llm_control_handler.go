package handler

import (
	"context"
	"errors"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/gin-gonic/gin"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
	"net/http"
	"time"
)

type llmControlAPI interface {
	PatchRuntimeSettings(context.Context, int, settingsapp.LLMRuntimePatch) (settingsapp.LLMRuntimeSettings, error)
	RuntimeSettings(context.Context, int) (settingsapp.LLMRuntimeSettings, error)
	SaveRuntimeSettings(context.Context, int, settingsapp.LLMRuntimeSettings) error
	Activate(context.Context, int, string) error
	TestDraft(context.Context, int, settingsapp.ModelProbeInput) (time.Duration, string, string, error)
	FetchModels(context.Context, int, settingsapp.ModelProbeInput) ([]string, error)
}

func (h *LLMProfileHandler) control(c *gin.Context) (llmControlAPI, int, bool) {
	user, ok := settingsUser(c)
	if !ok {
		return nil, 0, false
	}
	control, ok := h.profiles.(llmControlAPI)
	if !ok {
		httpdto.InternalError(c, "LLM controls are unavailable")
		return nil, 0, false
	}
	return control, user, true
}
func (h *LLMProfileHandler) Activate(c *gin.Context) {
	control, user, ok := h.control(c)
	if !ok {
		return
	}
	if err := control.Activate(c.Request.Context(), user, c.Param("profile")); err != nil {
		profileError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"ok": true})
}
func (h *LLMProfileHandler) Runtime(c *gin.Context) {
	control, user, ok := h.control(c)
	if !ok {
		return
	}
	state, err := control.RuntimeSettings(c.Request.Context(), user)
	if err != nil {
		profileError(c, err)
		return
	}
	httpdto.Success(c, state)
}
func (h *LLMProfileHandler) UpdateRuntime(c *gin.Context) {
	control, user, ok := h.control(c)
	if !ok {
		return
	}
	var input settingsapp.LLMRuntimePatch
	if !httpdto.BindJSON(c, &input) {
		return
	}
	state, err := control.PatchRuntimeSettings(c.Request.Context(), user, input)
	if err != nil {
		profileError(c, err)
		return
	}
	httpdto.Success(c, state)
}
func (h *LLMProfileHandler) RetryPolicy(c *gin.Context) {
	control, user, ok := h.control(c)
	if !ok {
		return
	}
	state, err := control.RuntimeSettings(c.Request.Context(), user)
	if err != nil {
		profileError(c, err)
		return
	}
	httpdto.Success(c, state.Retry)
}
func (h *LLMProfileHandler) SaveRetryPolicy(c *gin.Context) {
	control, user, ok := h.control(c)
	if !ok {
		return
	}
	var retry artexdb.LLMRetryPolicy
	if !httpdto.BindJSON(c, &retry) {
		return
	}
	state, err := control.PatchRuntimeSettings(c.Request.Context(), user, settingsapp.LLMRuntimePatch{Retry: &retry})
	if err != nil {
		profileError(c, err)
		return
	}
	httpdto.Success(c, state.Retry)
}
func (h *LLMProfileHandler) Pool(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	if h.pool == nil {
		httpdto.InternalError(c, "LLM pool is unavailable")
		return
	}
	state, err := h.pool.PoolStatus(c.Request.Context(), user)
	if err != nil {
		profileError(c, err)
		return
	}
	httpdto.Success(c, state)
}
func (h *LLMProfileHandler) ResetPool(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	var input struct {
		ID string `json:"id"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if h.pool == nil {
		httpdto.InternalError(c, "LLM pool is unavailable")
		return
	}
	state, err := h.pool.ResetPool(c.Request.Context(), user, input.ID)
	if err != nil {
		profileError(c, err)
		return
	}
	httpdto.Success(c, state)
}
func (h *LLMProfileHandler) TestDraft(c *gin.Context) {
	control, user, ok := h.control(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	var input settingsapp.ModelProbeInput
	if !httpdto.BindJSON(c, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
	defer cancel()
	latency, reply, model, err := control.TestDraft(ctx, user, input)
	if errors.Is(err, settingsapp.ErrForbidden) {
		profileError(c, err)
		return
	}
	if err != nil {
		httpdto.Success(c, gin.H{"ok": false, "error": err.Error()})
		return
	}
	if len([]rune(reply)) > 500 {
		reply = string([]rune(reply)[:500])
	}
	httpdto.Success(c, gin.H{"ok": true, "latency_ms": latency.Milliseconds(), "model": model, "reply": reply})
}
func (h *LLMProfileHandler) FetchModels(c *gin.Context) {
	control, user, ok := h.control(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	var input settingsapp.ModelProbeInput
	if !httpdto.BindJSON(c, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
	defer cancel()
	models, err := control.FetchModels(ctx, user, input)
	if errors.Is(err, settingsapp.ErrForbidden) {
		profileError(c, err)
		return
	}
	if err != nil {
		httpdto.Success(c, gin.H{"ok": false, "error": err.Error()})
		return
	}
	httpdto.Success(c, gin.H{"ok": true, "models": models})
}

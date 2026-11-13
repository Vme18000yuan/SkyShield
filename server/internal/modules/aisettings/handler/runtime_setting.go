package handler

import (
	"github.com/gin-gonic/gin"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
	"net/http"
)

type RuntimeSettingHandler struct {
	app *settings.RuntimeSettingFacade
}

func NewRuntimeSettingHandler(app *settings.RuntimeSettingFacade) *RuntimeSettingHandler {
	return &RuntimeSettingHandler{app: app}
}
func (h *RuntimeSettingHandler) Get(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	value, err := h.app.Get(c.Request.Context(), user)
	if err != nil {
		profileError(c, err)
		return
	}
	httpdto.Success(c, value)
}
func (h *RuntimeSettingHandler) Patch(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256*1024)
	var input map[string]any
	if !httpdto.BindJSON(c, &input) {
		return
	}
	value, err := h.app.Patch(c.Request.Context(), user, input)
	if err != nil {
		profileError(c, err)
		return
	}
	httpdto.Success(c, value)
}

func (h *RuntimeSettingHandler) DetectPython(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	path, err := h.app.DetectPython(c.Request.Context(), user)
	if err != nil {
		profileError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"python_interpreter": path})
}
func (h *RuntimeSettingHandler) ProbeWebSearch(c *gin.Context) {
	user, ok := settingsUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	var input settings.WebSearchProbeInput
	if !httpdto.BindJSON(c, &input) {
		return
	}
	result, err := h.app.ProbeWebSearch(c.Request.Context(), user, input)
	if err != nil {
		profileError(c, err)
		return
	}
	httpdto.Success(c, result)
}

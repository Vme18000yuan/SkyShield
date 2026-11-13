package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
	systemapp "github.com/yyhuni/lunafox/server/internal/modules/system/application"
	"github.com/yyhuni/lunafox/server/internal/modules/system/dto"
)

type httpProxySettingsApplication interface {
	Get(context.Context, int) (systemapp.HTTPProxySettingsView, error)
	Update(context.Context, int, bool, string) (systemapp.HTTPProxySettingsView, error)
	Clear(context.Context, int) (systemapp.HTTPProxySettingsView, error)
}

type HTTPProxySettingsHandler struct{ app httpProxySettingsApplication }

func NewHTTPProxySettingsHandler(app httpProxySettingsApplication) *HTTPProxySettingsHandler {
	return &HTTPProxySettingsHandler{app: app}
}

// Get returns the enabled state and credential-free proxy address.
// GET /v1/admin/system/httpProxy/current
func (handler *HTTPProxySettingsHandler) Get(c *gin.Context) {
	userID, ok := httpProxyUser(c)
	if !ok {
		return
	}
	view, err := handler.app.Get(c.Request.Context(), userID)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	httpdto.Success(c, dto.HTTPProxySettingsOutput(view))
}

// Update changes the Server-wide HTTP proxy. An empty proxyUrl preserves a
// saved address, which allows operators to toggle the proxy without resending
// credentials.
// PUT /v1/admin/system/httpProxy/current
func (handler *HTTPProxySettingsHandler) Update(c *gin.Context) {
	userID, ok := httpProxyUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var input struct {
		Enabled  *bool  `json:"enabled"`
		ProxyURL string `json:"proxyUrl"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if input.Enabled == nil {
		httpdto.BadRequest(c, "The enabled field is required")
		return
	}
	view, err := handler.app.Update(c.Request.Context(), userID, *input.Enabled, input.ProxyURL)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	httpdto.Success(c, dto.HTTPProxySettingsOutput(view))
}

// Clear disables the override and deletes the saved proxy credentials.
// DELETE /v1/admin/system/httpProxy/current
func (handler *HTTPProxySettingsHandler) Clear(c *gin.Context) {
	userID, ok := httpProxyUser(c)
	if !ok {
		return
	}
	view, err := handler.app.Clear(c.Request.Context(), userID)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	httpdto.Success(c, dto.HTTPProxySettingsOutput(view))
}

func httpProxyUser(c *gin.Context) (int, bool) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		httpdto.Unauthorized(c, "Not authenticated")
	}
	return userID, ok
}

func (handler *HTTPProxySettingsHandler) writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, systemapp.ErrHTTPProxyForbidden):
		httpdto.Forbidden(c, "System HTTP proxy settings require an active superuser")
	case errors.Is(err, systemapp.ErrHTTPProxyInvalidInput):
		httpdto.BadRequest(c, "Enter a valid HTTP or HTTPS proxy URL")
	case errors.Is(err, systemapp.ErrHTTPProxyURLRequired):
		httpdto.ErrorWithStatus(c, http.StatusBadRequest, "PROXY_URL_REQUIRED", "FAILED_PRECONDITION", "Set a proxy URL before enabling the proxy")
	case errors.Is(err, airuntime.ErrEncryptionKeyUnavailable):
		httpdto.ErrorWithStatus(c, http.StatusServiceUnavailable, "CREDENTIAL_ENCRYPTION_KEY_REQUIRED", "UNAVAILABLE", "Credential encryption key is not configured")
	default:
		httpdto.InternalError(c, "System HTTP proxy settings operation failed")
	}
}

package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
	spaceapp "github.com/yyhuni/lunafox/server/internal/modules/spacesearch/application"
)

type fofaApplication interface {
	Status(context.Context, int) (*spaceapp.SettingsView, error)
	Save(context.Context, int, string, string) (*spaceapp.SettingsView, error)
	Clear(context.Context, int) error
	Test(context.Context, int) error
	Search(context.Context, int, spaceapp.SearchInput) (*spaceapp.SearchPage, error)
	Import(context.Context, spaceapp.ImportInput) (*spaceapp.ImportResult, error)
}
type FofaHandler struct{ app fofaApplication }

func NewFofaHandler(app fofaApplication) *FofaHandler { return &FofaHandler{app: app} }
func fofaUser(c *gin.Context) (int, bool) {
	user, ok := middleware.GetUserID(c)
	if !ok {
		httpdto.Unauthorized(c, "Not authenticated")
	}
	return user, ok
}
func fofaError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, spaceapp.ErrForbidden):
		httpdto.Forbidden(c, "Network search permission required")
	case errors.Is(err, spaceapp.ErrInvalidInput):
		httpdto.BadRequest(c, "Invalid FOFA query or import input")
	case errors.Is(err, spaceapp.ErrNotConfigured):
		httpdto.ErrorWithStatus(c, http.StatusBadRequest, "FOFA_NOT_CONFIGURED", "FAILED_PRECONDITION", "Configure FOFA email and API key first")
	case errors.Is(err, spaceapp.ErrSearchNotFound):
		httpdto.NotFound(c, "FOFA search snapshot not found or expired; run the query again")
	case errors.Is(err, spaceapp.ErrDestination):
		httpdto.NotFound(c, "Active destination organization or target not found")
	case errors.Is(err, airuntime.ErrEncryptionKeyUnavailable):
		httpdto.ErrorWithStatus(c, http.StatusServiceUnavailable, "CREDENTIAL_ENCRYPTION_KEY_REQUIRED", "UNAVAILABLE", "Credential encryption key is not configured")
	case errors.Is(err, spaceapp.ErrProvider):
		httpdto.ErrorWithStatus(c, http.StatusBadGateway, "FOFA_REQUEST_FAILED", "UNAVAILABLE", err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		httpdto.ErrorWithStatus(c, http.StatusGatewayTimeout, "FOFA_TIMEOUT", "DEADLINE_EXCEEDED", "FOFA operation timed out")
	default:
		httpdto.InternalError(c, "Network search operation failed")
	}
}
func settingsDTO(view *spaceapp.SettingsView) gin.H {
	return gin.H{"name": httpdto.FofaSettingName(), "email": view.Email, "configured": view.Configured, "canManage": view.CanManage, "canSearch": view.CanSearch, "updatedAt": view.UpdatedAt}
}

// Status returns configuration availability without credentials.
// GET /v1/fofaSettings/current
func (h *FofaHandler) Status(c *gin.Context) {
	user, ok := fofaUser(c)
	if !ok {
		return
	}
	view, err := h.app.Status(c.Request.Context(), user)
	if err != nil {
		fofaError(c, err)
		return
	}
	httpdto.Success(c, settingsDTO(view))
}

// Save replaces the email; an empty key preserves the saved API key.
// PUT /v1/fofaSettings/current
func (h *FofaHandler) Save(c *gin.Context) {
	user, ok := fofaUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8*1024)
	var input struct {
		Email  string `json:"email"`
		APIKey string `json:"apiKey"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	view, err := h.app.Save(c.Request.Context(), user, input.Email, input.APIKey)
	if err != nil {
		fofaError(c, err)
		return
	}
	httpdto.Success(c, settingsDTO(view))
}

// Clear removes the saved FOFA credential.
// DELETE /v1/fofaSettings/current
func (h *FofaHandler) Clear(c *gin.Context) {
	user, ok := fofaUser(c)
	if !ok {
		return
	}
	if err := h.app.Clear(c.Request.Context(), user); err != nil {
		fofaError(c, err)
		return
	}
	httpdto.NoContent(c)
}

// Test verifies the saved credential through the FOFA account endpoint.
// POST /v1/fofaSettings/current/test
func (h *FofaHandler) Test(c *gin.Context) {
	user, ok := fofaUser(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	if err := h.app.Test(ctx, user); err != nil {
		fofaError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"valid": true})
}

// Search performs one explicit provider query and stores an importable snapshot.
// POST /v1/fofaSearches
func (h *FofaHandler) Search(c *gin.Context) {
	user, ok := fofaUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	var input spaceapp.SearchInput
	if !httpdto.BindJSON(c, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 40*time.Second)
	defer cancel()
	page, err := h.app.Search(ctx, user, input)
	if err != nil {
		fofaError(c, err)
		return
	}
	httpdto.Created(c, gin.H{"id": page.ID, "name": httpdto.FofaSearchName(page.ID), "query": page.Query, "page": page.Page, "pageSize": page.PageSize, "full": page.Full, "total": page.Total, "results": page.Results, "hasNext": page.HasNext, "createdAt": page.CreatedAt, "expiresAt": page.ExpiresAt})
}

// Import syncs only server-saved rows from the caller's unexpired search.
// POST /v1/fofaSearches/:search/import
func (h *FofaHandler) Import(c *gin.Context) {
	user, ok := fofaUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	var input struct {
		Rows           []int `json:"rows"`
		OrganizationID int   `json:"organizationId"`
		TargetID       int   `json:"targetId"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	result, err := h.app.Import(ctx, spaceapp.ImportInput{UserID: user, SearchID: c.Param("search"), Rows: input.Rows, OrganizationID: input.OrganizationID, TargetID: input.TargetID})
	if err != nil {
		fofaError(c, err)
		return
	}
	httpdto.Success(c, result)
}

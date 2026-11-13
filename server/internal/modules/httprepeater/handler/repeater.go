package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
	app "github.com/yyhuni/lunafox/server/internal/modules/httprepeater/application"
)

type RepeaterApplication interface {
	Send(context.Context, int, app.Input) (app.Result, error)
}
type Handler struct{ app RepeaterApplication }

func New(app RepeaterApplication) *Handler { return &Handler{app: app} }

// POST /v1/httpRepeater:send. The target's HTTP status stays inside the result;
// a target 401 must never trigger the platform client's authentication retry.
func (h *Handler) Send(c *gin.Context) {
	user, ok := middleware.GetUserID(c)
	if !ok {
		httpdto.Unauthorized(c, "Not authenticated")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4*app.MaxRequestBytes)
	var input app.Input
	if !httpdto.BindJSON(c, &input) {
		return
	}
	result, err := h.app.Send(c.Request.Context(), user, input)
	var invalid *app.InvalidInput
	switch {
	case errors.As(err, &invalid):
		httpdto.BadRequest(c, invalid.Message)
	case errors.Is(err, app.ErrForbidden):
		httpdto.Forbidden(c, "REPEATER_PERMISSION_REQUIRED")
	case errors.Is(err, app.ErrBlocked):
		httpdto.Forbidden(c, "REPEATER_DESTINATION_BLOCKED")
	case errors.Is(err, app.ErrDuplicate):
		httpdto.ErrorWithStatus(c, 409, "REPEATER_ALREADY_SENT", "ALREADY_EXISTS", "This attempt was accepted already; use a new request ID to send again")
	case errors.Is(err, app.ErrBusy):
		httpdto.ErrorWithStatus(c, 429, "REPEATER_BUSY", "RESOURCE_EXHAUSTED", "Too many replay requests; wait for the current request to finish")
	case err != nil:
		httpdto.InternalError(c, "Repeater unavailable")
	default:
		c.Header("Cache-Control", "no-store")
		httpdto.Success(c, result)
	}
}

package handler

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	monitor "github.com/yyhuni/lunafox/server/internal/modules/assetmonitor/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type Reader interface {
	Read(context.Context, int, []int) (*monitor.Snapshot, error)
}
type Handler struct{ reader Reader }

func NewHandler(reader Reader) *Handler { return &Handler{reader: reader} }

// Current returns a consistent selected-organization snapshot.
// GET /v1/assetMonitors/current
func (h *Handler) Current(c *gin.Context) {
	user, ok := middleware.GetUserID(c)
	if !ok {
		httpdto.Unauthorized(c, "Not authenticated")
		return
	}
	raw := c.Query("organizationIds")
	if len(raw) > 600 {
		httpdto.BadRequest(c, "Invalid organization selection")
		return
	}
	parts := strings.Split(raw, ",")
	ids := make([]int, 0, len(parts))
	for _, part := range parts {
		id, err := strconv.Atoi(part)
		if err != nil || id <= 0 {
			httpdto.BadRequest(c, "Select one or more organizations")
			return
		}
		ids = append(ids, id)
	}
	snapshot, err := h.reader.Read(c.Request.Context(), user, ids)
	if err != nil {
		switch {
		case errors.Is(err, monitor.ErrInvalidSelection):
			httpdto.BadRequest(c, "Invalid organization selection")
		case errors.Is(err, monitor.ErrForbidden):
			httpdto.Forbidden(c, "Active tenant membership required")
		case errors.Is(err, monitor.ErrOrganizationNotFound):
			httpdto.NotFound(c, "A selected organization no longer exists")
		default:
			httpdto.InternalError(c, "Failed to load asset monitor")
		}
		return
	}
	httpdto.Success(c, snapshot)
}

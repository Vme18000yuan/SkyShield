package handler

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

// GET /v1/organizations/:organization/assetOverview
func (h *AssetEntityHandler) OrganizationOverview(c *gin.Context) {
	user, ok := middleware.GetUserID(c)
	if !ok {
		httpdto.Unauthorized(c, "Not authenticated")
		return
	}
	org, err := strconv.Atoi(c.Param("organization"))
	if err != nil || org < 1 || org > 2147483647 {
		httpdto.BadRequest(c, "Invalid organization")
		return
	}
	hide, ok := httpdto.OptionalQueryBool(c, "hideCdn")
	if !ok {
		return
	}
	result, err := h.reader.OrganizationOverview(c.Request.Context(), user, org, hide)
	switch {
	case err == nil:
		httpdto.Success(c, result)
	case errors.Is(err, assetapp.ErrOrganizationNotFound):
		httpdto.NotFound(c, "Organization not found")
	case errors.Is(err, assetapp.ErrForbidden):
		httpdto.Forbidden(c, "Tenant membership required")
	default:
		httpdto.InternalError(c, "Failed to load organization assets")
	}
}

package handler

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
	"strconv"
)

type rootCoverageReader interface {
	RootDomainCoverage(context.Context, int, int64, int) (assetapp.RootCoverageGraph, error)
	RootDomainCoverageRefs(context.Context, int, int64, int, int64) (assetapp.RootCoverageRefs, error)
}

// GET /v1/assetEntities/:id/coverageGraph
func (h *AssetEntityHandler) RootDomainCoverage(c *gin.Context) { h.rootCoverage(c, false) }

// GET /v1/assetEntities/:id/coverageRefs
func (h *AssetEntityHandler) RootDomainCoverageRefs(c *gin.Context) { h.rootCoverage(c, true) }
func (h *AssetEntityHandler) rootCoverage(c *gin.Context, refs bool) {
	user, ok := middleware.GetUserID(c)
	if !ok {
		httpdto.Unauthorized(c, "Not authenticated")
		return
	}
	root, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || root <= 0 {
		httpdto.BadRequest(c, "Invalid root domain")
		return
	}
	org, ok := parseOptionalInt(c, "organizationId")
	if !ok {
		httpdto.BadRequest(c, "Invalid organizationId")
		return
	}
	reader, ok := h.reader.(rootCoverageReader)
	if !ok {
		httpdto.InternalError(c, "Coverage unavailable")
		return
	}
	var result any
	if refs {
		id, e := strconv.ParseInt(c.Query("assetId"), 10, 64)
		if e != nil || id <= 0 {
			httpdto.BadRequest(c, "Invalid assetId")
			return
		}
		result, err = reader.RootDomainCoverageRefs(c.Request.Context(), user, root, org, id)
	} else {
		result, err = reader.RootDomainCoverage(c.Request.Context(), user, root, org)
	}
	if err != nil {
		switch {
		case errors.Is(err, assetapp.ErrInvalidQuery):
			httpdto.BadRequest(c, "Invalid coverage query")
		case errors.Is(err, assetapp.ErrForbidden):
			httpdto.Forbidden(c, "Tenant membership required")
		case errors.Is(err, assetapp.ErrRootDomainNotFound):
			httpdto.NotFound(c, "Root domain or asset not found")
		default:
			httpdto.InternalError(c, "Failed to load root coverage")
		}
		return
	}
	httpdto.Success(c, result)
}

package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

func assetIntelligenceID(c *gin.Context) (int, int64, bool) {
	user, ok := conversationUser(c)
	if !ok {
		return 0, 0, false
	}
	asset, err := strconv.ParseInt(c.Param("asset"), 10, 64)
	if err != nil || asset <= 0 {
		httpdto.BadRequest(c, "Invalid asset ID")
		return 0, 0, false
	}
	return user, asset, true
}
func (h *TaskHandler) AssetIntelligence(c *gin.Context) {
	user, asset, ok := assetIntelligenceID(c)
	if !ok {
		return
	}
	if h.intelligence == nil {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	filter := aiapp.IntelligenceFilter{Kind: c.Query("kind"), Status: c.Query("status"), Query: c.Query("q")}
	var err error
	if raw := c.Query("limit"); raw != "" {
		filter.Limit, err = strconv.Atoi(raw)
		if err != nil {
			taskError(c, aiapp.ErrInvalidInput)
			return
		}
	}
	if raw := c.Query("offset"); raw != "" {
		filter.Offset, err = strconv.Atoi(raw)
		if err != nil {
			taskError(c, aiapp.ErrInvalidInput)
			return
		}
	}
	if c.Param("intelligence") != "" {
		id, ok := parseCatalogID(c, "intelligence")
		if !ok {
			return
		}
		filter.ID = id
		filter.Limit = 1
	}
	page, err := h.intelligence.ListAsset(c.Request.Context(), user, asset, filter)
	if err != nil {
		taskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	if filter.ID > 0 {
		if len(page.Items) != 1 {
			taskError(c, aiapp.ErrTaskNotFound)
			return
		}
		httpdto.Success(c, page.Items[0])
		return
	}
	httpdto.Success(c, page)
}
func (h *TaskHandler) AssetCredentialTraffic(c *gin.Context) {
	user, asset, ok := assetIntelligenceID(c)
	if !ok {
		return
	}
	intel, ok := parseCatalogID(c, "intelligence")
	if !ok {
		return
	}
	if h.credentialTraffic == nil {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	out, err := h.credentialTraffic.DetailAsset(c.Request.Context(), user, asset, intel, c.Param("traffic"))
	if err != nil {
		taskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

package router

import (
	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/modules/spacesearch/handler"
)

func RegisterSpaceSearchRoutes(protected *gin.RouterGroup, h *handler.FofaHandler) {
	protected.GET("/fofaSettings/current", h.Status)
	protected.PUT("/fofaSettings/current", h.Save)
	protected.DELETE("/fofaSettings/current", h.Clear)
	protected.POST("/fofaSettings/current/test", h.Test)
	protected.POST("/fofaSearches", h.Search)
	protected.POST("/fofaSearches/:search/import", h.Import)
}

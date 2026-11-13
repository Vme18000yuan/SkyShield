package router

import (
	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/modules/assetregistry/handler"
)

func RegisterAssetRegistryRoutes(protected *gin.RouterGroup, h *handler.AssetEntityHandler, writers ...*handler.AssetRegistrationHandler) {
	protected.GET("/assetEntities", h.List)
	protected.GET("/assetInventory", h.ListInventory)
	protected.GET("/assetEntities/:id/rootDomainOverview", h.RootDomainOverview)
	protected.GET("/assetEntities/:id/coverageGraph", h.RootDomainCoverage)
	protected.GET("/assetEntities/:id/coverageRefs", h.RootDomainCoverageRefs)
	protected.GET("/assetEntities/:id/applicationOverview", h.ApplicationOverview)
	protected.GET("/assetPorts", h.ListPorts)
	if len(writers) > 0 && writers[0] != nil {
		protected.POST("/assetEntities:batchCreate", writers[0].BatchCreate)
	}
}

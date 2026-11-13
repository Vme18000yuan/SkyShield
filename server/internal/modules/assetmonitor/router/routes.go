package router

import (
	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/modules/assetmonitor/handler"
)

func RegisterRoutes(protected *gin.RouterGroup, h *handler.Handler) {
	protected.GET("/assetMonitors/current", h.Current)
}

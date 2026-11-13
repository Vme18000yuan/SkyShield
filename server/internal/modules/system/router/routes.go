package router

import (
	"github.com/gin-gonic/gin"
	systemhandler "github.com/yyhuni/lunafox/server/internal/modules/system/handler"
)

func RegisterSystemRoutes(
	protected *gin.RouterGroup,
	serverLogHandler *systemhandler.ServerLogHandler,
	runtimeMetricsHandler *systemhandler.RuntimeMetricsHandler,
) {
	admin := protected.Group("/admin/system")
	{
		admin.GET("/logEntries", serverLogHandler.List)
		admin.GET("/runtimeMetrics/current", runtimeMetricsHandler.Current)
	}
}

func RegisterHTTPProxySettingsRoutes(protected *gin.RouterGroup, settingsHandler *systemhandler.HTTPProxySettingsHandler) {
	if protected == nil || settingsHandler == nil {
		panic("HTTP proxy settings route dependencies are required")
	}
	admin := protected.Group("/admin/system")
	admin.GET("/httpProxy/current", settingsHandler.Get)
	admin.PUT("/httpProxy/current", settingsHandler.Update)
	admin.DELETE("/httpProxy/current", settingsHandler.Clear)
}

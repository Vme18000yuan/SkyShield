package router

import (
	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/modules/aiapproval/handler"
)

func Register(protected *gin.RouterGroup, h *handler.Handler) {
	for _, path := range []string{"/aiAssetInterceptRules", "/aiTasks/:task/assetInterceptRules"} {
		protected.GET(path, h.AssetRules)
		protected.POST(path, h.AssetRules)
		protected.PUT(path+"/:rule", h.AssetRule)
		protected.DELETE(path+"/:rule", h.AssetRule)
		protected.POST(path+"/:rule/toggle", h.ToggleAssetRule)
	}
	protected.GET("/aiInterceptRules", h.Rules)
	protected.POST("/aiInterceptRules", h.Rules)
	protected.PUT("/aiInterceptRules/:rule", h.Rule)
	protected.DELETE("/aiInterceptRules/:rule", h.Rule)
	protected.POST("/aiInterceptRules/:rule/toggle", h.ToggleRule)
	protected.GET("/aiInterceptJudge", h.Judge)
	protected.PUT("/aiInterceptJudge", h.Judge)
	protected.GET("/aiInterceptJudge/usage", h.JudgeUsage)
	protected.GET("/aiInterceptTools", h.Tools)
	protected.PUT("/aiInterceptTools", h.Tools)
	protected.GET("/aiApprovals/pending", h.Pending)
	protected.GET("/aiApprovals", h.List)
	protected.GET("/aiApprovals/:approval", h.Get)
	protected.GET("/aiApprovals/:approval/detail", h.Get)
	protected.GET("/aiApprovals/:approval/execution", h.Execution)
	protected.GET("/aiApprovals/:approval/execution/context", h.ExecutionContext)
	protected.POST("/aiApprovals/:approval/decision", h.Decision)
	protected.GET("/aiTasks/:task/approvals", h.List)
}

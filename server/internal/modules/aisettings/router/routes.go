package router

import (
	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/modules/aisettings/handler"
)

func RegisterAISettingsRoutes(protected *gin.RouterGroup, h *handler.LLMProfileHandler) {
	protected.GET("/aiModelUsage", h.Usage)
	protected.GET("/aiModelUsage/tasks/:task", h.TaskUsage)
	protected.GET("/aiLlmRecords", h.Recordings)
	protected.DELETE("/aiLlmRecords", h.Recordings)
	protected.GET("/aiLlmRecords/:record", h.Recording)
	protected.GET("/aiLlmRecordTasks", h.RecordingTasks)
	protected.GET("/aiLlmProfiles", h.List)
	protected.GET("/aiModels", h.ListModels)
	protected.POST("/aiLlmProfiles", h.Create)
	protected.PUT("/aiLlmProfiles/:profile", h.Update)
	protected.DELETE("/aiLlmProfiles/:profile", h.Delete)
	protected.POST("/aiLlmProfiles/:profile/test", h.Test)
	protected.POST("/aiLlmProfiles/:profile/activate", h.Activate)
	protected.POST("/aiLlmProfileTests", h.TestDraft)
	protected.POST("/aiLlmModelCatalog", h.FetchModels)
	protected.GET("/aiLlmRuntime", h.Runtime)
	protected.PATCH("/aiLlmRuntime", h.UpdateRuntime)
	protected.GET("/aiLlmRuntime/retryPolicy", h.RetryPolicy)
	protected.PUT("/aiLlmRuntime/retryPolicy", h.SaveRetryPolicy)
	protected.GET("/aiLlmRuntime/pool", h.Pool)
	protected.POST("/aiLlmRuntime/pool/reset", h.ResetPool)
}

func RegisterAIAgentSettingRoutes(protected *gin.RouterGroup, h *handler.AgentSettingHandler) {
	protected.GET("/aiAgentSettings", h.List)
	protected.PUT("/aiAgentSettings/:agent", h.Update)
}

func RegisterAIAgentCatalogRoutes(protected *gin.RouterGroup, h *handler.AgentCatalogHandler) {
	protected.GET("/aiAgents", h.List)
	protected.POST("/aiAgents", h.Create)
	protected.GET("/aiAgents/:agent", h.Get)
	protected.PUT("/aiAgents/:agent", h.Update)
	protected.DELETE("/aiAgents/:agent", h.Delete)
	protected.GET("/aiAgents/:agent/promptVersions", h.PromptVersions)
	protected.POST("/aiAgents/:agent/promptPreview", h.PreviewPrompt)
}

func RegisterAIAgentControlRoutes(protected *gin.RouterGroup, h *handler.AgentControlHandler) {
	protected.GET("/aiAgents/:agent/details", h.Details)
	protected.PUT("/aiAgents/:agent/config", h.Config)
	protected.PUT("/aiAgents/:agent/prompt", h.SavePrompt)
	protected.POST("/aiAgents/:agent/prompt/reset", h.ResetPrompt)
	protected.POST("/aiAgents/:agent/prompt/preview", h.Preview)
	protected.PUT("/aiAgents/:agent/wrapup", h.Wrapup)
	protected.POST("/aiAgents/:agent/wrapup/reset", h.ResetWrapup)
	protected.PUT("/aiAgents/:agent/wrapup/task-timeout", h.TaskWrapup)
	protected.POST("/aiAgents/:agent/wrapup/task-timeout/reset", h.ResetTaskWrapup)
	protected.GET("/aiAgents/:agent/visibility", h.Visibility)
	protected.PUT("/aiAgents/:agent/visibility", h.SaveVisibility)
	protected.GET("/aiAgents/:agent/triggers", h.Triggers)
	protected.POST("/aiAgents/:agent/triggers", h.CreateTrigger)
	protected.PATCH("/aiAgentTriggers/:trigger", h.UpdateTrigger)
	protected.DELETE("/aiAgentTriggers/:trigger", h.DeleteTrigger)
}

func RegisterAIToolSettingRoutes(protected *gin.RouterGroup, h *handler.ToolSettingHandler) {
	protected.GET("/aiToolSettings", h.List)
	protected.PUT("/aiToolSettings/:agent/:tool", h.Update)
}

func RegisterAIToolCatalogRoutes(protected *gin.RouterGroup, h *handler.ToolCatalogHandler) {
	protected.GET("/aiTools", h.List)
	protected.POST("/aiTools", h.Save)
	protected.POST("/aiToolTests", h.Test)
	protected.PUT("/aiTools/:tool", h.Save)
	protected.DELETE("/aiTools/:tool", h.Delete)
	protected.POST("/aiTools/:tool/reset", h.Reset)
}

func RegisterAIRuntimeSettingRoutes(protected *gin.RouterGroup, h *handler.RuntimeSettingHandler) {
	protected.GET("/aiRuntimeSettings", h.Get)
	protected.PATCH("/aiRuntimeSettings", h.Patch)
	protected.PUT("/aiRuntimeSettings", h.Patch)
	protected.POST("/aiRuntimeSettings/pythonDetection", h.DetectPython)
	protected.POST("/aiWebSearchTests", h.ProbeWebSearch)
}

func RegisterAISkillRoutes(protected *gin.RouterGroup, h *handler.SkillHandler) {
	protected.GET("/aiSkills", h.List)
	protected.POST("/aiSkills", h.Create)
	protected.PUT("/aiSkills/:skill", h.Update)
	protected.DELETE("/aiSkills/:skill", h.Delete)
}

func RegisterAISkillPackageRoutes(protected *gin.RouterGroup, h *handler.SkillPackageHandler) {
	protected.GET("/aiSkillPackages", h.List)
	protected.POST("/aiSkillPackages", h.Create)
	protected.POST("/aiSkillArchives", h.Upload)
	protected.GET("/aiMissingSkills", h.Missing)
	protected.DELETE("/aiSkillPackages/:skill", h.Delete)
	protected.PUT("/aiSkillPackages/:skill/metadata", h.Metadata)
	protected.POST("/aiSkillPackages/:skill/directories", h.Directory)
	protected.GET("/aiSkillPackages/:skill/files", h.Files)
	protected.GET("/aiSkillPackages/:skill/files/*file", h.File)
	protected.PUT("/aiSkillPackages/:skill/files/*file", h.File)
	protected.DELETE("/aiSkillPackages/:skill/files/*file", h.File)
	protected.GET("/aiSkillPackages/:skill/visibility", h.Visibility)
	protected.POST("/aiSkillPackages/:skill/visibility", h.Visibility)
	protected.GET("/aiSkillPackages/:skill/usage", h.Usage)
}

func RegisterAIMCPRoutes(protected *gin.RouterGroup, h *handler.MCPHandler) {
	protected.GET("/aiMcpServers", h.List)
	protected.POST("/aiMcpServers", h.Create)
	protected.PUT("/aiMcpServers/:server", h.Update)
	protected.DELETE("/aiMcpServers/:server", h.Delete)
	protected.POST("/aiMcpServers/:server/test", h.Test)
	protected.PATCH("/aiMcpServers/:server", h.Enabled)
	protected.GET("/aiMcpServers/:server/details", h.Details)
	protected.GET("/aiMcpServers/:server/tools", h.Tools)
	protected.POST("/aiMcpServers/:server/refresh", h.Test)
	protected.GET("/aiMcpServers/:server/visibility", h.Visibility)
	protected.POST("/aiMcpServers/:server/visibility", h.Visibility)
}

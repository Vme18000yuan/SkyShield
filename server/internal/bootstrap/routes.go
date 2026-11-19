package bootstrap

import (
	"github.com/gin-gonic/gin"
	agentrouter "github.com/yyhuni/lunafox/server/internal/modules/agent/router"
	aiapprovalrouter "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/router"
	aisettingsrouter "github.com/yyhuni/lunafox/server/internal/modules/aisettings/router"
	aiworkbenchrouter "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/router"
	assetrouter "github.com/yyhuni/lunafox/server/internal/modules/asset/router"
	assetmonitorrouter "github.com/yyhuni/lunafox/server/internal/modules/assetmonitor/router"
	assetregistryrouter "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/router"
	blacklistrouter "github.com/yyhuni/lunafox/server/internal/modules/blacklist/router"
	catalogrouter "github.com/yyhuni/lunafox/server/internal/modules/catalog/router"
	fingerprintrouter "github.com/yyhuni/lunafox/server/internal/modules/fingerprint/router"
	repeaterrouter "github.com/yyhuni/lunafox/server/internal/modules/httprepeater/router"
	identityrouter "github.com/yyhuni/lunafox/server/internal/modules/identity/router"
	loginvisualrouter "github.com/yyhuni/lunafox/server/internal/modules/loginvisual/router"
	notificationrouter "github.com/yyhuni/lunafox/server/internal/modules/notification/router"
	nucleipocrouter "github.com/yyhuni/lunafox/server/internal/modules/nucleipoc/router"
	scanrouter "github.com/yyhuni/lunafox/server/internal/modules/scan/router"
	scheduledscanrouter "github.com/yyhuni/lunafox/server/internal/modules/scheduledscan/router"
	securityrouter "github.com/yyhuni/lunafox/server/internal/modules/security/router"
	spacesearchrouter "github.com/yyhuni/lunafox/server/internal/modules/spacesearch/router"
	systemrouter "github.com/yyhuni/lunafox/server/internal/modules/system/router"
	upgraderouter "github.com/yyhuni/lunafox/server/internal/modules/upgrade/router"
	"github.com/yyhuni/lunafox/server/internal/productupdate"
)

func registerRoutes(engine *gin.Engine, d *deps, jwtMiddleware gin.HandlerFunc) {
	if d.oastCollector != nil {
		engine.Any("/oast/*callback", gin.WrapH(d.oastCollector))
	}
	// MCP shares the Server lifecycle and public edge with REST, but uses its
	// own static-key admission and protocol handler instead of JWT routing.
	if d.mcpClients != nil {
		engine.Any("/mcp", gin.WrapH(d.mcpClients.Handler(d.mcpHandler)))
		engine.Any("/mcp/clients", gin.WrapH(d.mcpClients.Handler(nil)))
	} else {
		engine.Any("/mcp", gin.WrapH(d.mcpHandler))
	}

	assetrouter.RegisterHealthRoutes(engine, d.healthHandler)

	api := engine.Group("/v1")
	protected := api.Group("")
	protected.Use(jwtMiddleware)
	if d.updateMaintenanceFile != "" {
		protected.Use(productupdate.Maintenance(d.updateMaintenanceFile))
	}
	if d.oastService != nil {
		d.oastService.RegisterRoutes(protected)
	}
	if d.mcpClients != nil {
		d.mcpClients.RegisterManagement(protected)
	}
	if d.httpRepeaterHandler != nil {
		repeaterrouter.Register(protected, d.httpRepeaterHandler)
	}
	if d.aiVulnerabilityService != nil {
		d.aiVulnerabilityService.RegisterRoutes(protected)
	}
	if d.infoGatherHandler != nil {
		d.infoGatherHandler.Register(protected)
	}
	if d.assetMonitorHandler != nil {
		assetmonitorrouter.RegisterRoutes(protected, d.assetMonitorHandler)
	}
	if d.spaceSearchHandler != nil {
		spacesearchrouter.RegisterSpaceSearchRoutes(protected, d.spaceSearchHandler)
	}

	identityrouter.RegisterIdentityRoutes(api, protected, d.authHandler, d.userHandler, d.orgHandler)
	if d.assetRegistryHandler != nil {
		assetregistryrouter.RegisterAssetRegistryRoutes(protected, d.assetRegistryHandler, d.assetRegistrationHandler)
	}
	if d.aiApprovalHandler != nil {
		aiapprovalrouter.Register(protected, d.aiApprovalHandler)
	}
	if d.aiWorkbenchHandler != nil {
		aiworkbenchrouter.RegisterAIWorkbenchRoutes(protected, d.aiWorkbenchHandler)
	}
	if d.aiTaskHandler != nil {
		aiworkbenchrouter.RegisterAITaskRoutes(protected, d.aiTaskHandler)
	}
	if d.aiNotificationHandler != nil {
		d.aiNotificationHandler.Register(protected)
	}
	if d.aiScheduleHandler != nil {
		aiworkbenchrouter.RegisterAIScheduleRoutes(protected, d.aiScheduleHandler)
	}
	if d.aiMCPHandler != nil {
		aisettingsrouter.RegisterAIMCPRoutes(protected, d.aiMCPHandler)
	}
	if d.aiSkillHandler != nil {
		aisettingsrouter.RegisterAISkillRoutes(protected, d.aiSkillHandler)
	}
	if d.aiSkillPackageHandler != nil {
		aisettingsrouter.RegisterAISkillPackageRoutes(protected, d.aiSkillPackageHandler)
	}
	if d.aiToolSettingHandler != nil {
		aisettingsrouter.RegisterAIToolSettingRoutes(protected, d.aiToolSettingHandler)
	}
	if d.aiToolCatalogHandler != nil {
		aisettingsrouter.RegisterAIToolCatalogRoutes(protected, d.aiToolCatalogHandler)
	}
	if d.aiRuntimeSettingHandler != nil {
		aisettingsrouter.RegisterAIRuntimeSettingRoutes(protected, d.aiRuntimeSettingHandler)
	}
	if d.aiAgentSettingHandler != nil {
		aisettingsrouter.RegisterAIAgentSettingRoutes(protected, d.aiAgentSettingHandler)
	}
	if d.aiAgentCatalogHandler != nil {
		aisettingsrouter.RegisterAIAgentCatalogRoutes(protected, d.aiAgentCatalogHandler)
	}
	if d.aiAgentControlHandler != nil {
		aisettingsrouter.RegisterAIAgentControlRoutes(protected, d.aiAgentControlHandler)
	}
	if d.aiSettingsHandler != nil {
		aisettingsrouter.RegisterAISettingsRoutes(protected, d.aiSettingsHandler)
	}
	catalogrouter.RegisterCatalogRoutes(
		protected,
		d.wordlistHandler,
		d.targetHandler,
		d.engineCatalogHandler,
		d.scanWorkflowCatalogHandler,
		d.subfinderAPIKeySettingsHandler,
	)
	blacklistrouter.RegisterBlacklistPolicyRoutes(protected, d.blacklistPolicyHandler)
	assetrouter.RegisterAssetRoutes(
		api,
		protected,
		d.screenshotHandler,
		d.screenshotSnapshotHandler,
		d.websiteHandler,
		d.subdomainHandler,
		d.endpointHandler,
		d.directoryHandler,
		d.hostPortHandler,
		d.globalAssetSearchHandler,
		d.globalAssetExportHandler,
		d.assetStatisticsHandler,
		d.endpointSnapshotHandler,
		d.hostPortSnapshotHandler,
		d.websiteSnapshotHandler,
		d.subdomainSnapshotHandler,
		d.directorySnapshotHandler,
		d.vulnerabilitySnapshotHandler,
	)
	assetrouter.RegisterSystemRoutes(protected, d.healthHandler)
	fingerprintrouter.RegisterFingerprintRoutes(protected, d.fingerprintHandler)
	scanrouter.RegisterScanRoutes(protected, d.scanHandler, d.taskProgressLogHandler)
	scheduledscanrouter.RegisterScheduledScanRoutes(protected, d.scheduledScanHandler)
	securityrouter.RegisterSecurityRoutes(protected, d.vulnerabilityHandler)
	if d.notificationInboxHandler != nil || d.notificationSSEHandler != nil {
		notificationrouter.RegisterNotificationRoutes(protected, d.notificationInboxHandler, nil, d.notificationSSEHandler)
	}
	if d.loginVisualHandler != nil {
		loginvisualrouter.RegisterLoginVisualRoutes(api, protected, d.loginVisualHandler)
	}
	if d.nucleiPocHandler != nil {
		nucleipocrouter.RegisterNucleiPOCRoutes(protected, d.nucleiPocHandler)
	}
	systemrouter.RegisterSystemRoutes(protected, d.serverLogHandler, d.runtimeMetricsHandler)
	if d.httpProxySettingsHandler != nil {
		systemrouter.RegisterHTTPProxySettingsRoutes(protected, d.httpProxySettingsHandler)
	}
	if d.upgradeHandler != nil {
		upgraderouter.RegisterUpgradeRoutes(protected, d.upgradeHandler)
	}
	agentrouter.RegisterAgentRoutes(api, protected, d.agentHandler, d.agentLogHandler, d.agentClusterSummaryHandler, d.agentLocationMapHandler, d.agentLicenseHandler)
}

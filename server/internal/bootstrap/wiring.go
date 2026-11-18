package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	artexagent "github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/artex/notify"
	"github.com/Autumn-27/artex/sidequestion"
	"github.com/Autumn-27/artex/traffic"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
	"github.com/yyhuni/lunafox/contracts/sharedstorage"
	"github.com/yyhuni/lunafox/server/internal/agentlicense"
	"github.com/yyhuni/lunafox/server/internal/ainotification"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/aitraffic"
	"github.com/yyhuni/lunafox/server/internal/auth"
	agentwiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/agent"
	aiapprovalwiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/aiapproval"
	aisettingswiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/aisettings"
	aiworkbenchwiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/aiworkbench"
	assetwiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/asset"
	assetregistrywiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/assetregistry"
	blacklistwiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/blacklist"
	catalogwiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/catalog"
	identitywiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/identity"
	loginvisualwiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/loginvisual"
	nucleipocwiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/nucleipoc"
	// Nuclei POC source sync is intentionally wired as an isolated catalog
	// module; it does not enter Agent, Worker, or Scan execution paths.
	"github.com/yyhuni/lunafox/server/internal/aivuln"
	notificationwiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/notification"
	resultingestwiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/resultingest"
	scanwiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/scan"
	securitywiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/security"
	snapshotwiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/snapshot"
	spacesearchwiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/spacesearch"
	targetcleanupwiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/targetcleanup"
	taskprogresslogwiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/taskprogresslog"
	"github.com/yyhuni/lunafox/server/internal/config"
	"github.com/yyhuni/lunafox/server/internal/geolocation"
	agentcontrol "github.com/yyhuni/lunafox/server/internal/grpc/agentcontrol"
	"github.com/yyhuni/lunafox/server/internal/httpoutbound"
	"github.com/yyhuni/lunafox/server/internal/infogather"
	"github.com/yyhuni/lunafox/server/internal/job"
	mcpadapters "github.com/yyhuni/lunafox/server/internal/mcp/adapters"
	mcpexternal "github.com/yyhuni/lunafox/server/internal/mcp/external"
	mcptools "github.com/yyhuni/lunafox/server/internal/mcp/tools"
	mcptransport "github.com/yyhuni/lunafox/server/internal/mcp/transport"
	agentservice "github.com/yyhuni/lunafox/server/internal/modules/agent/application"
	agentdomain "github.com/yyhuni/lunafox/server/internal/modules/agent/domain"
	agenthandler "github.com/yyhuni/lunafox/server/internal/modules/agent/handler"
	agentinfra "github.com/yyhuni/lunafox/server/internal/modules/agent/infrastructure"
	agentrepo "github.com/yyhuni/lunafox/server/internal/modules/agent/repository"
	aiapprovalapp "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/application"
	aiapprovalhandler "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/handler"
	aisettingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	aisettingshandler "github.com/yyhuni/lunafox/server/internal/modules/aisettings/handler"
	aisettingsinfra "github.com/yyhuni/lunafox/server/internal/modules/aisettings/infrastructure"
	aiworkbenchapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	aiworkbenchhandler "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/handler"
	aiworkbenchinfra "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/infrastructure"
	assetservice "github.com/yyhuni/lunafox/server/internal/modules/asset/application"
	assethandler "github.com/yyhuni/lunafox/server/internal/modules/asset/handler"
	directoryhandler "github.com/yyhuni/lunafox/server/internal/modules/asset/handler/directory"
	endpointhandler "github.com/yyhuni/lunafox/server/internal/modules/asset/handler/endpoint"
	hostporthandler "github.com/yyhuni/lunafox/server/internal/modules/asset/handler/host_port"
	screenshothandler "github.com/yyhuni/lunafox/server/internal/modules/asset/handler/screenshot"
	searchhandler "github.com/yyhuni/lunafox/server/internal/modules/asset/handler/search"
	subdomainhandler "github.com/yyhuni/lunafox/server/internal/modules/asset/handler/subdomain"
	websitehandler "github.com/yyhuni/lunafox/server/internal/modules/asset/handler/website"
	assetrepo "github.com/yyhuni/lunafox/server/internal/modules/asset/repository"
	assetmonitorapp "github.com/yyhuni/lunafox/server/internal/modules/assetmonitor/application"
	assetmonitorhandler "github.com/yyhuni/lunafox/server/internal/modules/assetmonitor/handler"
	assetmonitorinfra "github.com/yyhuni/lunafox/server/internal/modules/assetmonitor/infrastructure"
	assetmonitorrepo "github.com/yyhuni/lunafox/server/internal/modules/assetmonitor/repository"
	assetregistryapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	assetregistryhandler "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/handler"
	blacklistapp "github.com/yyhuni/lunafox/server/internal/modules/blacklist/application"
	blacklisthandler "github.com/yyhuni/lunafox/server/internal/modules/blacklist/handler"
	blacklistrepo "github.com/yyhuni/lunafox/server/internal/modules/blacklist/repository"
	catalogservice "github.com/yyhuni/lunafox/server/internal/modules/catalog/application"
	cataloghandler "github.com/yyhuni/lunafox/server/internal/modules/catalog/handler"
	catalogrepo "github.com/yyhuni/lunafox/server/internal/modules/catalog/repository"
	fingerprintapp "github.com/yyhuni/lunafox/server/internal/modules/fingerprint/application"
	fingerprinthandler "github.com/yyhuni/lunafox/server/internal/modules/fingerprint/handler"
	fingerprintrepo "github.com/yyhuni/lunafox/server/internal/modules/fingerprint/repository"
	repeaterapp "github.com/yyhuni/lunafox/server/internal/modules/httprepeater/application"
	repeaterhandler "github.com/yyhuni/lunafox/server/internal/modules/httprepeater/handler"
	repeaterinfra "github.com/yyhuni/lunafox/server/internal/modules/httprepeater/infrastructure"
	repeaterrepo "github.com/yyhuni/lunafox/server/internal/modules/httprepeater/repository"
	identityservice "github.com/yyhuni/lunafox/server/internal/modules/identity/application"
	identityhandler "github.com/yyhuni/lunafox/server/internal/modules/identity/handler"
	identityinfra "github.com/yyhuni/lunafox/server/internal/modules/identity/infrastructure"
	identityrepo "github.com/yyhuni/lunafox/server/internal/modules/identity/repository"
	loginvisualhandler "github.com/yyhuni/lunafox/server/internal/modules/loginvisual/handler"
	notificationdomain "github.com/yyhuni/lunafox/server/internal/modules/notification/domain"
	notificationhandler "github.com/yyhuni/lunafox/server/internal/modules/notification/handler"
	notificationrepo "github.com/yyhuni/lunafox/server/internal/modules/notification/repository"
	nucleipocapp "github.com/yyhuni/lunafox/server/internal/modules/nucleipoc/application"
	nucleipocHandler "github.com/yyhuni/lunafox/server/internal/modules/nucleipoc/handler"
	nucleipocRepo "github.com/yyhuni/lunafox/server/internal/modules/nucleipoc/repository"
	resultingestapp "github.com/yyhuni/lunafox/server/internal/modules/resultingest/application"
	scanapp "github.com/yyhuni/lunafox/server/internal/modules/scan/application"
	scanhandler "github.com/yyhuni/lunafox/server/internal/modules/scan/handler"
	scaninfra "github.com/yyhuni/lunafox/server/internal/modules/scan/infrastructure"
	scanrepo "github.com/yyhuni/lunafox/server/internal/modules/scan/repository"
	scheduledscanapp "github.com/yyhuni/lunafox/server/internal/modules/scheduledscan/application"
	scheduledscanhandler "github.com/yyhuni/lunafox/server/internal/modules/scheduledscan/handler"
	scheduledscanrepo "github.com/yyhuni/lunafox/server/internal/modules/scheduledscan/repository"
	securityservice "github.com/yyhuni/lunafox/server/internal/modules/security/application"
	securityhandler "github.com/yyhuni/lunafox/server/internal/modules/security/handler"
	securityrepo "github.com/yyhuni/lunafox/server/internal/modules/security/repository"
	snapshotservice "github.com/yyhuni/lunafox/server/internal/modules/snapshot/application"
	snapshothandler "github.com/yyhuni/lunafox/server/internal/modules/snapshot/handler"
	snapshotrepo "github.com/yyhuni/lunafox/server/internal/modules/snapshot/repository"
	spacesearchapp "github.com/yyhuni/lunafox/server/internal/modules/spacesearch/application"
	spacesearchhandler "github.com/yyhuni/lunafox/server/internal/modules/spacesearch/handler"
	spacesearchinfra "github.com/yyhuni/lunafox/server/internal/modules/spacesearch/infrastructure"
	systemservice "github.com/yyhuni/lunafox/server/internal/modules/system/application"
	systemdomain "github.com/yyhuni/lunafox/server/internal/modules/system/domain"
	systemhandler "github.com/yyhuni/lunafox/server/internal/modules/system/handler"
	systeminfra "github.com/yyhuni/lunafox/server/internal/modules/system/infrastructure"
	systemrepo "github.com/yyhuni/lunafox/server/internal/modules/system/repository"
	targetcleanupapp "github.com/yyhuni/lunafox/server/internal/modules/targetcleanup/application"
	targetcleanupinfra "github.com/yyhuni/lunafox/server/internal/modules/targetcleanup/infrastructure"
	targetcleanuprepo "github.com/yyhuni/lunafox/server/internal/modules/targetcleanup/repository"
	upgradeapp "github.com/yyhuni/lunafox/server/internal/modules/upgrade/application"
	upgradedomain "github.com/yyhuni/lunafox/server/internal/modules/upgrade/domain"
	upgradehandler "github.com/yyhuni/lunafox/server/internal/modules/upgrade/handler"
	upgradeinfra "github.com/yyhuni/lunafox/server/internal/modules/upgrade/infrastructure"
	upgraderepo "github.com/yyhuni/lunafox/server/internal/modules/upgrade/repository"
	"github.com/yyhuni/lunafox/server/internal/productupdate"
	"gorm.io/gorm"
)

type deps struct {
	httpRepeaterHandler            *repeaterhandler.Handler
	infoGatherHandler              *infogather.Handler
	aiVulnerabilityService         *aivuln.Service
	assetMonitorHandler            *assetmonitorhandler.Handler
	assetMonitor                   *assetmonitorapp.Facade
	spaceSearchHandler             *spacesearchhandler.FofaHandler
	assetRegistryHandler           *assetregistryhandler.AssetEntityHandler
	assetRegistrationHandler       *assetregistryhandler.AssetRegistrationHandler
	assetRegistryBackfill          *assetregistryapp.BackfillController
	assetRootProjection            *assetregistryapp.RootDomainController
	assetApplicationProjection     *assetregistryapp.ApplicationProjectionController
	aiSettingsHandler              *aisettingshandler.LLMProfileHandler
	aiAgentSettingHandler          *aisettingshandler.AgentSettingHandler
	aiAgentCatalogHandler          *aisettingshandler.AgentCatalogHandler
	aiAgentControlHandler          *aisettingshandler.AgentControlHandler
	aiToolSettingHandler           *aisettingshandler.ToolSettingHandler
	aiToolCatalogHandler           *aisettingshandler.ToolCatalogHandler
	aiRuntimeSettingHandler        *aisettingshandler.RuntimeSettingHandler
	aiSkillHandler                 *aisettingshandler.SkillHandler
	aiSkillPackageHandler          *aisettingshandler.SkillPackageHandler
	aiMCPHandler                   *aisettingshandler.MCPHandler
	aiApprovalHandler              *aiapprovalhandler.Handler
	aiWorkbenchHandler             *aiworkbenchhandler.ConversationHandler
	aiWorkbench                    *aiworkbenchapp.ConversationFacade
	aiTraffic                      *aitraffic.Manager
	aiTaskHandler                  *aiworkbenchhandler.TaskHandler
	aiNotificationHandler          *ainotification.Handler
	aiNotificationWorker           managedBackgroundJob
	aiTaskController               *aiworkbenchapp.TaskController
	aiCredentialProcessor          *aiworkbenchapp.CredentialProcessor
	aiScheduleHandler              *aiworkbenchhandler.ScheduleHandler
	aiScheduleController           *aiworkbenchapp.ScheduleController
	aiAgentTriggerController       *aiworkbenchapp.AgentTriggerController
	tokenVersionReader             auth.TokenVersionReader
	mcpKeyService                  *identityservice.MCPKeyLifecycleService
	mcpHandler                     *mcptransport.Handler
	mcpClients                     *mcpexternal.Service
	healthHandler                  *assethandler.HealthHandler
	authHandler                    *identityhandler.AuthHandler
	userHandler                    *identityhandler.UserHandler
	orgHandler                     *identityhandler.OrganizationHandler
	targetHandler                  *cataloghandler.TargetHandler
	engineCatalogHandler           *cataloghandler.EngineCatalogHandler
	scanWorkflowCatalogHandler     *cataloghandler.ScanWorkflowManagementHandler
	wordlistHandler                *cataloghandler.WordlistHandler
	subfinderAPIKeySettingsHandler *cataloghandler.SubfinderAPIKeySettingsHandler
	blacklistPolicyHandler         *blacklisthandler.BlacklistPolicyHandler
	websiteHandler                 *websitehandler.WebsiteHandler
	subdomainHandler               *subdomainhandler.SubdomainHandler
	endpointHandler                *endpointhandler.EndpointHandler
	directoryHandler               *directoryhandler.DirectoryHandler
	hostPortHandler                *hostporthandler.HostPortHandler
	screenshotHandler              *screenshothandler.ScreenshotHandler
	globalAssetSearchHandler       *searchhandler.GlobalAssetSearchHandler
	globalAssetExportHandler       *searchhandler.GlobalAssetExportHandler
	assetStatisticsHandler         *assethandler.AssetStatisticsHandler
	vulnerabilityHandler           *securityhandler.VulnerabilityHandler
	scanHandler                    *scanhandler.ScanHandler
	scheduledScanHandler           *scheduledscanhandler.ScheduledScanHandler
	taskProgressLogHandler         *scanhandler.TaskProgressLogHandler
	fingerprintHandler             *fingerprinthandler.FingerprintHandler
	fingerprintArtifacts           *fingerprintapp.FingerprintArtifactService
	nucleiPocRepo                  *nucleipocRepo.NucleiPOCRepository

	agentHandler                *agenthandler.AgentHandler
	agentLogHandler             *agenthandler.AgentLogHandler
	agentClusterSummaryHandler  *agenthandler.AgentClusterSummaryHandler
	agentLicenseHandler         *agenthandler.AgentLicenseHandler
	agentLocationMapHandler     *agenthandler.AgentLocationMapHandler
	serverLogHandler            *systemhandler.ServerLogHandler
	runtimeMetricsHandler       *systemhandler.RuntimeMetricsHandler
	httpProxySettingsHandler    *systemhandler.HTTPProxySettingsHandler
	runtimeMetricsJob           runtimeMetricsJob
	scanHistoryRetentionJob     runtimeMetricsJob
	registrationTokenCleanupJob runtimeMetricsJob
	scheduledScanController     managedBackgroundJob
	occurrenceRetentionJob      managedBackgroundJob
	targetCleanupRunner         managedBackgroundJob
	notificationOutboxWorker    managedBackgroundJob
	notificationRetentionJob    managedBackgroundJob
	notificationInboxHandler    *notificationhandler.NotificationInboxHandler
	notificationSSEHandler      *notificationhandler.NotificationSSEHandler
	loginVisualHandler          *loginvisualhandler.LoginVisualHandler
	nucleiPocHandler            *nucleipocHandler.NucleiPOCHandler
	nucleiPocRunner             *nucleipocapp.SyncRunner
	nucleiPocRetentionJob       *nucleipocapp.RetentionJob

	websiteSnapshotHandler       *snapshothandler.WebsiteSnapshotHandler
	subdomainSnapshotHandler     *snapshothandler.SubdomainSnapshotHandler
	endpointSnapshotHandler      *snapshothandler.EndpointSnapshotHandler
	directorySnapshotHandler     *snapshothandler.DirectorySnapshotHandler
	hostPortSnapshotHandler      *snapshothandler.HostPortSnapshotHandler
	screenshotSnapshotHandler    *snapshothandler.ScreenshotSnapshotHandler
	vulnerabilitySnapshotHandler *snapshothandler.VulnerabilitySnapshotHandler

	agentRepo    agentdomain.AgentRepository
	scanRepo     *scanrepo.ScanRepository
	scanTaskRepo scanrepo.ScanTaskRepository
	scanTaskSvc  *scanapp.ScanTaskFacade

	agentControlService     *agentservice.AgentControlLifecycleService
	agentTaskService        *agentservice.AgentTaskService
	agentLocationObserver   *agentservice.AgentLocationObserver
	serverLocationReader    *systemservice.ServerLocationReader
	serverLocationScheduler *systemservice.ServerLocationScheduler
	geolocationCoordinator  *geolocation.Coordinator
	upgradeHandler          *upgradehandler.UpgradeHandler
	productUpdateHandler    *productupdate.Handler
	upgradeRecoveryJob      managedBackgroundJob

	executionProviderConfigSource *catalogservice.ExecutionProviderConfigSource
	executionWordlistSource       *catalogservice.ExecutionWordlistSource
	subdomainSnapshotRepo         *snapshotrepo.SubdomainSnapshotRepository
	hostPortSnapshotRepo          *snapshotrepo.HostPortSnapshotRepository
	websiteSnapshotRepo           *snapshotrepo.WebsiteSnapshotRepository
	endpointSnapshotRepo          *snapshotrepo.EndpointSnapshotRepository
	subdomainRepo                 *assetrepo.SubdomainRepository
	hostPortRepo                  *assetrepo.HostPortRepository
	websiteRepo                   *assetrepo.WebsiteRepository
	endpointRepo                  *assetrepo.EndpointRepository
	subdomainSnapshotService      *snapshotservice.SubdomainSnapshotFacade
	websiteSnapshotService        *snapshotservice.WebsiteSnapshotFacade
	endpointSnapshotService       *snapshotservice.EndpointSnapshotFacade
	hostPortSnapshotService       *snapshotservice.HostPortSnapshotFacade
	screenshotSnapshotService     *snapshotservice.ScreenshotSnapshotFacade
	resultIngestService           *resultingestapp.ResultIngestFacade
	runtimeStreamRegistry         *agentcontrol.AgentStreamRegistry
	taskProgressLogService        scanapp.TaskProgressLogApplicationService
}

type repositoryBundle struct {
	userRepo                      *identityrepo.UserRepository
	mcpKeyRepo                    *identityrepo.MCPKeyRepository
	orgRepo                       *identityrepo.OrganizationRepository
	targetRepo                    *catalogrepo.TargetRepository
	blacklistPolicyRepo           *blacklistrepo.PolicyRepository
	engineRepo                    *catalogrepo.EngineRepository
	wordlistRepo                  *catalogrepo.WordlistRepository
	scanWorkflowRepo              *catalogrepo.ScanWorkflowRepository
	websiteRepo                   *assetrepo.WebsiteRepository
	subdomainRepo                 *assetrepo.SubdomainRepository
	endpointRepo                  *assetrepo.EndpointRepository
	directoryRepo                 *assetrepo.DirectoryRepository
	hostPortRepo                  *assetrepo.HostPortRepository
	assetStatisticsRepo           *assetrepo.AssetStatisticsRepository
	screenshotRepo                *assetrepo.ScreenshotRepository
	vulnerabilityRepo             securityrepo.VulnerabilityRepository
	scanRepo                      *scanrepo.ScanRepository
	scheduledScanRepo             *scheduledscanrepo.ScheduledScanRepository
	targetCleanupRepo             *targetcleanuprepo.TargetCleanupRepository
	taskProgressLogRepo           *scanrepo.TaskProgressLogRepository
	subfinderProviderSettingsRepo *catalogrepo.SubfinderProviderSettingsRepository
	websiteSnapshotRepo           *snapshotrepo.WebsiteSnapshotRepository
	subdomainSnapshotRepo         *snapshotrepo.SubdomainSnapshotRepository
	endpointSnapshotRepo          *snapshotrepo.EndpointSnapshotRepository
	directorySnapshotRepo         *snapshotrepo.DirectorySnapshotRepository
	hostPortSnapshotRepo          *snapshotrepo.HostPortSnapshotRepository
	screenshotSnapshotRepo        *snapshotrepo.ScreenshotSnapshotRepository
	vulnerabilitySnapshotRepo     *snapshotrepo.VulnerabilitySnapshotRepository
	agentRepo                     agentdomain.AgentOperationalRepository
	agentLicenseRepo              *agentrepo.AgentLicenseRepository
	agentLocationRepo             agentdomain.AgentLocationRepository
	registrationTokenRepo         agentdomain.RegistrationTokenRepository
	serverLocationRepo            systemdomain.ServerLocationRepository
	scanTaskRepo                  scanrepo.ScanTaskRepository
	fingerprintRepo               *fingerprintrepo.FingerprintRepository
	notificationProducer          *notificationrepo.ProducerWriter
	nucleiPocRepo                 *nucleipocRepo.NucleiPOCRepository
}

type identityModuleHandlers struct {
	authHandler        *identityhandler.AuthHandler
	userHandler        *identityhandler.UserHandler
	orgHandler         *identityhandler.OrganizationHandler
	organizationSvc    *identityservice.OrganizationFacade
	tokenVersionReader auth.TokenVersionReader
	mcpKeyService      *identityservice.MCPKeyLifecycleService
}

type catalogModuleHandlers struct {
	targetHandler                  *cataloghandler.TargetHandler
	engineCatalogHandler           *cataloghandler.EngineCatalogHandler
	scanWorkflowCatalogHandler     *cataloghandler.ScanWorkflowManagementHandler
	wordlistHandler                *cataloghandler.WordlistHandler
	subfinderAPIKeySettingsHandler *cataloghandler.SubfinderAPIKeySettingsHandler

	wordlistService               *catalogservice.WordlistFacade
	targetSvc                     *catalogservice.TargetFacade
	scanWorkflowService           *catalogservice.ScanWorkflowManagementService
	scanWorkflowProfileService    *catalogservice.ScanWorkflowProfileService
	engineCatalogService          *catalogservice.EngineCatalogFacade
	executionProviderConfigSource *catalogservice.ExecutionProviderConfigSource
	executionWordlistSource       *catalogservice.ExecutionWordlistSource
}

type blacklistModuleHandlers struct {
	blacklistPolicyHandler *blacklisthandler.BlacklistPolicyHandler
	blacklistPolicyService blacklistapp.BlacklistPolicyApplicationService
}

type assetModuleWiring struct {
	websiteHandler           *websitehandler.WebsiteHandler
	subdomainHandler         *subdomainhandler.SubdomainHandler
	endpointHandler          *endpointhandler.EndpointHandler
	directoryHandler         *directoryhandler.DirectoryHandler
	hostPortHandler          *hostporthandler.HostPortHandler
	screenshotHandler        *screenshothandler.ScreenshotHandler
	globalAssetSearchHandler *searchhandler.GlobalAssetSearchHandler
	globalAssetExportHandler *searchhandler.GlobalAssetExportHandler
	assetStatisticsHandler   *assethandler.AssetStatisticsHandler

	websiteSvc    *assetservice.WebsiteFacade
	subdomainSvc  *assetservice.SubdomainFacade
	endpointSvc   *assetservice.EndpointFacade
	directorySvc  *assetservice.DirectoryFacade
	hostPortSvc   *assetservice.HostPortFacade
	screenshotSvc *assetservice.ScreenshotFacade
}

type fingerprintModuleWiring struct {
	handler   *fingerprinthandler.FingerprintHandler
	artifacts *fingerprintapp.FingerprintArtifactService
}

type securityModuleWiring struct {
	vulnerabilityHandler *securityhandler.VulnerabilityHandler
	vulnerabilitySvc     *securityservice.VulnerabilityFacade
}

type scanModuleWiring struct {
	scanHandler             *scanhandler.ScanHandler
	scheduledScanHandler    *scheduledscanhandler.ScheduledScanHandler
	taskProgressLogHandler  *scanhandler.TaskProgressLogHandler
	scanTaskBridge          agentservice.ScanTaskBridgePort
	scanTaskSvc             *scanapp.ScanTaskFacade
	taskProgressLogService  scanapp.TaskProgressLogApplicationService
	scanSvc                 *scanapp.ScanFacade
	scheduledScanController *scheduledscanapp.SchedulerController
	occurrenceRetentionJob  managedBackgroundJob
}

type agentModuleHandlers struct {
	agentHandler               *agenthandler.AgentHandler
	agentLogHandler            *agenthandler.AgentLogHandler
	agentClusterSummaryHandler *agenthandler.AgentClusterSummaryHandler
	agentLicenseHandler        *agenthandler.AgentLicenseHandler
	agentLocationMapHandler    *agenthandler.AgentLocationMapHandler

	agentControlService         *agentservice.AgentControlLifecycleService
	agentTaskService            *agentservice.AgentTaskService
	agentService                *agentservice.AgentFacade
	lokiLogQueryService         *agentservice.LokiLogQueryService
	agentLocationObserver       *agentservice.AgentLocationObserver
	registrationTokenCleanupJob runtimeMetricsJob
}

type systemModuleHandlers struct {
	serverLogHandler         *systemhandler.ServerLogHandler
	serverLogService         *systemservice.ServerLogService
	runtimeMetricsHandler    *systemhandler.RuntimeMetricsHandler
	httpProxySettingsHandler *systemhandler.HTTPProxySettingsHandler
	runtimeMetricsJob        runtimeMetricsJob
	serverLocationReader     *systemservice.ServerLocationReader
	serverLocationScheduler  *systemservice.ServerLocationScheduler
}

type runtimeMetricsJob interface {
	Start(ctx context.Context)
}

type managedBackgroundJob interface {
	Start(ctx context.Context)
	Done() <-chan struct{}
}

type snapshotModuleHandlers struct {
	websiteSnapshotHandler       *snapshothandler.WebsiteSnapshotHandler
	subdomainSnapshotHandler     *snapshothandler.SubdomainSnapshotHandler
	endpointSnapshotHandler      *snapshothandler.EndpointSnapshotHandler
	directorySnapshotHandler     *snapshothandler.DirectorySnapshotHandler
	hostPortSnapshotHandler      *snapshothandler.HostPortSnapshotHandler
	screenshotSnapshotHandler    *snapshothandler.ScreenshotSnapshotHandler
	vulnerabilitySnapshotHandler *snapshothandler.VulnerabilitySnapshotHandler
	subdomainSnapshotService     *snapshotservice.SubdomainSnapshotFacade
	websiteSnapshotService       *snapshotservice.WebsiteSnapshotFacade
	endpointSnapshotService      *snapshotservice.EndpointSnapshotFacade
	directorySnapshotService     *snapshotservice.DirectorySnapshotFacade
	hostPortSnapshotService      *snapshotservice.HostPortSnapshotFacade
	screenshotSnapshotService    *snapshotservice.ScreenshotSnapshotFacade
	vulnerabilitySnapshotService *snapshotservice.VulnerabilitySnapshotFacade
}

func buildDependencies(infra *infra, cfg *config.Config) (*deps, error) {
	// Fail before assembling services if the shipped author trust root is invalid.
	if _, err := agentlicense.NewDefaultVerifier(); err != nil {
		return nil, fmt.Errorf("configure Agent license verifier: %w", err)
	}
	repos := newRepositoryBundle(infra, cfg)
	notificationWorkerOwner, err := notificationwiring.NewNotificationWorkerOwner()
	if err != nil {
		return nil, fmt.Errorf("create notification worker owner: %w", err)
	}
	notification, err := notificationwiring.NewNotificationModule(infra.db, notificationWorkerOwner)
	if err != nil {
		return nil, fmt.Errorf("wire notification module: %w", err)
	}
	loginVisual, err := loginvisualwiring.NewLoginVisualModule(infra.db, cfg.Storage.LoginVisualsBasePath)
	if err != nil {
		return nil, fmt.Errorf("wire login visual module: %w", err)
	}
	streamRegistry := agentcontrol.NewAgentStreamRegistry()
	grpcPublisher := agentcontrol.NewAgentControlEventPublisher(streamRegistry)

	identity := wireIdentityModule(repos, infra)
	catalog := wireCatalogModule(repos, cfg, infra)
	blacklist, err := wireBlacklistModule(repos)
	if err != nil {
		return nil, fmt.Errorf("wire blacklist module: %w", err)
	}
	asset := wireAssetModule(repos, infra, identity.tokenVersionReader)
	fingerprint, err := wireFingerprintModule(repos, cfg)
	if err != nil {
		return nil, fmt.Errorf("wire fingerprint module: %w", err)
	}
	security := wireSecurityModule(repos)
	scan, err := wireScanModule(repos, infra, cfg, grpcPublisher, catalog.wordlistService, blacklist.blacklistPolicyService)
	if err != nil {
		return nil, fmt.Errorf("wire scan module: %w", err)
	}
	targetCleanupService, err := targetcleanupwiring.NewTargetcleanupApplicationService(
		repos.targetCleanupRepo,
		targetcleanupwiring.NewTargetcleanupScheduleCleanerAdapter(repos.scheduledScanRepo),
		targetcleanupwiring.NewTargetcleanupScanCancellerAdapter(repos.scanRepo),
		targetcleanupwiring.NewTargetcleanupTaskCancelPublisherAdapter(grpcPublisher),
	)
	if err != nil {
		return nil, fmt.Errorf("wire Target cleanup module: %w", err)
	}
	targetCleanupRunner, err := targetcleanupapp.NewTargetCleanupRunner(
		repos.targetCleanupRepo,
		targetCleanupService,
		targetcleanupapp.TargetCleanupRunOptions{
			AssetBatchSize:  cfg.TargetCleanup.BatchSize,
			MaxAssetBatches: cfg.TargetCleanup.MaxBatchesPerRun,
			MaxRunDuration:  cfg.TargetCleanup.MaxRunDuration,
		},
		targetcleanupinfra.NewTargetCleanupRunObserver(),
	)
	if err != nil {
		return nil, fmt.Errorf("initialize Target cleanup Runner: %w", err)
	}
	locationCoordinator, err := geolocation.NewCoordinator(geolocation.NewFreeIPAPIClient(nil))
	if err != nil {
		return nil, fmt.Errorf("initialize geolocation coordinator: %w", err)
	}
	system, err := wireSystemModule(repos, infra, cfg, locationCoordinator)
	if err != nil {
		shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = locationCoordinator.Shutdown(shutdownContext)
		return nil, fmt.Errorf("wire system module: %w", err)
	}
	serverLocationReader, err := agentwiring.NewAgentServerLocationReaderAdapter(system.serverLocationReader)
	if err != nil {
		shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = locationCoordinator.Shutdown(shutdownContext)
		return nil, fmt.Errorf("wire Agent Server location reader: %w", err)
	}
	agent, err := wireAgentModule(repos, infra, cfg, scan.scanTaskBridge, grpcPublisher, locationCoordinator, serverLocationReader)
	if err != nil {
		shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = locationCoordinator.Shutdown(shutdownContext)
		return nil, fmt.Errorf("wire Agent module: %w", err)
	}
	snapshot := wireSnapshotModule(infra.db, repos, asset, security)
	scanHistoryRetentionJob := job.NewScanHistoryRetentionJob(
		snapshotrepo.NewScanHistoryPartitionLifecycle(infra.db),
		snapshotrepo.ScanHistoryRetentionMode(cfg.ScanHistoryRetention.Mode),
		cfg.ScanHistoryRetention.Interval,
		cfg.ScanHistoryRetention.Retention,
		snapshotrepo.ScanHistoryRetentionRunOptions{
			TaskDeleteBatchSize:          cfg.ScanHistoryRetention.TaskDeleteBatchSize,
			MaxTaskDeleteBatchesPerRange: cfg.ScanHistoryRetention.MaxTaskDeleteBatchesPerRange,
			MaxRangesPerRun:              cfg.ScanHistoryRetention.MaxRangesPerRun,
			MaxRunDuration:               cfg.ScanHistoryRetention.MaxRunDuration,
		},
	)
	resultIngestSummary := resultingestwiring.NewResultingestScanResultSummaryUpdaterAdapter(repos.scanRepo)
	resultIngestCoordinator := resultingestwiring.NewResultIngestMaterializationCoordinator(infra.db)
	resultIngest := resultingestapp.NewResultIngestFacade(resultingestapp.ResultIngestFacadeDependencies{Subdomains: snapshot.subdomainSnapshotService, ScanSummary: resultIngestSummary, HostPorts: snapshot.hostPortSnapshotService, Websites: snapshot.websiteSnapshotService, WebsiteTechnologies: asset.websiteSvc, Endpoints: snapshot.endpointSnapshotService, Directories: snapshot.directorySnapshotService, Screenshots: snapshot.screenshotSnapshotService, Vulnerabilities: snapshot.vulnerabilitySnapshotService, Materialization: resultIngestCoordinator})
	var upgradeHandler *upgradehandler.UpgradeHandler
	var productUpdateHandler *productupdate.Handler
	var upgradeRecoveryJob managedBackgroundJob
	deploymentMode := os.Getenv("SKYSHIELD_DEPLOYMENT_MODE")
	if cfg.LocalMode {
		deploymentMode = "local"
	}
	if deploymentMode == "local" || deploymentMode == "docker" {
		productUpdateHandler = productupdate.New(infra.db, deploymentMode, os.Getenv("SKYSHIELD_UPDATE_SOCKET"))
	} else if !cfg.LocalMode {
		manifestSource, err := newUpgradeManifestSource(cfg.Upgrade)
		if err != nil {
			return nil, fmt.Errorf("configure release manifest source: %w", err)
		}
		migrationPolicySource := upgradeapp.MigrationPolicyFunc(func(context.Context) (upgradedomain.MigrationPolicy, error) {
			return upgradeapp.LoadMigrationPolicy(cfg.Upgrade.MigrationPolicyPath)
		})
		upgradeOperationRepository := upgraderepo.NewUpgradeOperationRepository(infra.db)
		upgradeAuthorizer := upgraderepo.NewActiveSuperuserRepository(infra.db)
		hostUpgradeDispatcher, err := upgradeinfra.NewHostUpgradeDispatcher(cfg.Upgrade.SocketPath)
		if err != nil {
			return nil, fmt.Errorf("configure host upgrade dispatcher: %w", err)
		}
		upgradeCoordinator := newUpgradePreDispatchCoordinator(scan.scanSvc, scan.scheduledScanController)
		upgradeAgentSource := newUpgradeAgentSource(repos.agentRepo, grpcPublisher, time.Now)
		upgradeVerifier, err := newUpgradeVerifier(infra, cfg)
		if err != nil {
			return nil, fmt.Errorf("configure upgrade verifier: %w", err)
		}
		upgradeService, err := upgradeapp.NewUpgradeService(upgradeapp.ServiceConfig{
			ManifestSource:        manifestSource,
			MigrationPolicySource: migrationPolicySource,
			Repository:            upgradeOperationRepository,
			Authorizer:            upgradeAuthorizer,
			Dispatcher:            hostUpgradeDispatcher,
			Coordinator:           upgradeCoordinator,
			Resumer:               upgradeCoordinator,
			AgentSource:           upgradeAgentSource,
			Verifier:              upgradeVerifier,
			CurrentVersion:        infra.releaseVersion,
			Registry:              cfg.Upgrade.Registry,
		})
		if err != nil {
			return nil, fmt.Errorf("wire upgrade service: %w", err)
		}
		upgradeJournalReader, err := upgradeapp.NewFileJournalEventReader(cfg.Upgrade.DeploymentRoot)
		if err != nil {
			return nil, fmt.Errorf("configure upgrade journal reader: %w", err)
		}
		recoveryJob, err := upgradeapp.NewRecoveryJob(upgradeService, upgradeJournalReader, 0)
		if err != nil {
			return nil, fmt.Errorf("wire upgrade recovery job: %w", err)
		}
		upgradeRecoveryJob = recoveryJob
		upgradeHandler = upgradehandler.NewUpgradeHandler(upgradeService)
	}
	mcpRegistry := mcptools.NewRegistry(mcpadapters.NewReaders(mcpadapters.Dependencies{
		Targets:         catalog.targetSvc,
		Organizations:   identity.organizationSvc,
		Scans:           scan.scanSvc,
		Websites:        asset.websiteSvc,
		Subdomains:      asset.subdomainSvc,
		Endpoints:       asset.endpointSvc,
		Directories:     asset.directorySvc,
		HostPorts:       asset.hostPortSvc,
		Vulnerabilities: security.vulnerabilitySvc,
		Screenshots:     asset.screenshotSvc,
		Workflows:       catalog.scanWorkflowService,
		Profiles:        catalog.scanWorkflowProfileService,
		Engines:         catalog.engineCatalogService,
		Wordlists:       catalog.wordlistService,
		ServerLogs:      system.serverLogService,
		AgentLogs:       agent.lokiLogQueryService,
		Agents:          agent.agentService,
	}))
	mcpHandler := mcptransport.NewIdentityHandler(identity.mcpKeyService, mcpRegistry)

	nucleiPocResolver := nucleipocapp.NewPublicSourceResolver()
	nucleiPocWorkspace, err := nucleipocapp.NewLocalWorkspace(cfg.Storage.NucleiPocWorkspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("wire nuclei POC workspace: %w", err)
	}
	nucleiPocRunner := nucleipocapp.NewSyncRunner(repos.nucleiPocRepo, nucleiPocResolver, nucleiPocWorkspace, nucleipocapp.NewProcessGitRunner())
	nucleiPocService, err := nucleipocapp.NewPOCService(repos.nucleiPocRepo, nucleiPocResolver, nucleiPocRunner)
	if err != nil {
		return nil, fmt.Errorf("wire nuclei POC service: %w", err)
	}
	nucleiPocRetention := nucleipocapp.NewRetentionJob(repos.nucleiPocRepo, nucleiPocWorkspace, nucleipocapp.RetentionInterval)
	// Recover interrupted tasks before accepting a new request. This makes the
	// singleton active-task constraint deterministic across process restarts.
	if _, err := repos.nucleiPocRepo.RecoverInterruptedTasks(context.Background(), time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("recover nuclei POC sync tasks: %w", err)
	}

	assetRegistryStore := assetregistrywiring.NewAssetRegistryStoreAdapter(infra.db)
	assetRegistryFacade := assetregistryapp.NewFacade(assetRegistryStore)
	aiRoot := os.Getenv("TIANQIONG_AI_DATA_ROOT")
	if aiRoot == "" {
		aiRoot = filepath.Join(filepath.Dir(cfg.Storage.NucleiPocWorkspaceRoot), "tianqiong-ai")
	}
	profileStore := aisettingswiring.NewAISettingsLLMProfileStoreAdapter(infra.db)
	secretBox := airuntime.SecretBox{}
	notify.SetHTTPProxyResolver(httpoutbound.DefaultProxyController.Proxy)
	aiNotificationStore := ainotification.NewStore(infra.db, secretBox)
	assetRegistrationStore := assetregistrywiring.NewAssetRegistryRegistrationStoreAdapter(infra.db, assetregistryapp.LegacyAssetCreators{
		Websites: asset.websiteSvc, Endpoints: asset.endpointSvc, Subdomains: asset.subdomainSvc,
	})
	assetRegistrationFacade := assetregistryapp.NewRegistrationFacade(assetRegistrationStore, secretBox)
	fofaFacade := spacesearchapp.NewFofaFacade(
		spacesearchwiring.NewSpacesearchFofaStoreAdapter(infra.db),
		spacesearchinfra.NewFofaClient(spacesearchinfra.FofaBaseURL, nil), secretBox,
		spacesearchwiring.NewSpacesearchAssetImporterAdapter(catalog.targetSvc, repos.targetRepo, asset.hostPortSvc, assetRegistrationFacade),
	)
	provider := aisettingsinfra.NewLLMResolver(profileStore, secretBox)
	aiGovernor := airuntime.NewExecutionGovernor(aiworkbenchwiring.NewAIExecutionPermitStore(infra.db))
	provider.SetExecutionGovernor(aiGovernor)
	provider.SetUsageSink(aisettingsapp.NewLLMRecordingRecorder(aisettingswiring.NewAISettingsLLMUsageSink(infra.db), aisettingswiring.NewAISettingsLLMRecordingStore(infra.db), secretBox))
	agentSettingFacade := aisettingsapp.NewAgentSettingFacade(aisettingswiring.NewAISettingsAgentSettingStoreAdapter(infra.db))
	agentCatalogFacade := aisettingsapp.NewAgentCatalogFacade(aisettingswiring.NewAISettingsAgentCatalogStoreAdapter(infra.db), agentSettingFacade.Resolve)
	agentControlFacade := aisettingsapp.NewAgentControlFacade(agentCatalogFacade, aisettingswiring.NewAISettingsAgentControlStoreAdapter(infra.db), profileStore)
	if err := aisettingswiring.SeedAISettingsDefaultAgents(context.Background(), infra.db); err != nil {
		return nil, fmt.Errorf("seed ARTEX default Agents: %w", err)
	}
	skillsRoot := strings.TrimSpace(os.Getenv("TIANQIONG_AI_SKILLS_ROOT"))
	if skillsRoot == "" {
		skillsRoot, err = filepath.Abs(filepath.Join("resources", "skills"))
		if err != nil {
			return nil, fmt.Errorf("resolve AI built-in skills directory: %w", err)
		}
	}
	if err := aisettingswiring.SeedAISettingsBuiltinSkills(context.Background(), infra.db, skillsRoot); err != nil {
		return nil, fmt.Errorf("seed ARTEX built-in skills: %w", err)
	}
	skillFacade := aisettingsapp.NewSkillFacade(aisettingswiring.NewAISettingsSkillStoreAdapter(infra.db, skillsRoot))
	skillFacade.SetMaterializer(aisettingsinfra.NewSkillMaterializer(aiRoot, aisettingswiring.NewAISettingsSkillRuntimeFileStoreAdapter(infra.db)))
	skillPackages := aisettingsapp.NewSkillPackageFacade(aisettingswiring.NewAISettingsSkillPackageStoreAdapter(infra.db, skillsRoot))
	mcpFacade := aisettingsapp.NewMCPFacade(aisettingswiring.NewAISettingsMCPStoreAdapter(infra.db), secretBox)
	mcpConnector := airuntime.NewMCPConnector(aiRoot)
	mcpFacade.SetConnector(mcpConnector)
	if err := aisettingswiring.SeedAISettingsDefaultMCP(context.Background(), infra.db); err != nil {
		return nil, fmt.Errorf("seed ARTEX default MCP: %w", err)
	}
	mcpAugmenter := airuntime.NewMCPAugmenterWithConnector(mcpFacade.Resolve, mcpConnector, skillFacade.Resolve)
	platformTools := airuntime.AssetToolResolver(assetRegistryFacade, assetRegistrationFacade)
	aiSQLDB, err := infra.db.DB()
	if err != nil {
		return nil, fmt.Errorf("wire AI asset query: %w", err)
	}
	approvalService := aiapprovalapp.NewService(aiapprovalwiring.NewStore(infra.db))
	if err := approvalService.Seed(context.Background()); err != nil {
		return nil, fmt.Errorf("seed ARTEX approval rules: %w", err)
	}
	platformBlacklist, err := aiapprovalwiring.NewPlatformBlacklistResolver(blacklist.blacklistPolicyService)
	if err != nil {
		return nil, fmt.Errorf("wire AI asset blacklist: %w", err)
	}
	approvalService.SetPlatformBlacklistResolver(platformBlacklist)
	httpRepeater := repeaterapp.NewService(repeaterrepo.NewAccessStore(infra.db, platformBlacklist), &repeaterinfra.RawTransport{Proxy: httpoutbound.DefaultProxyController.Proxy})
	assetRegistrationFacade.SetAIAssetGate(func(ctx context.Context, user int, scope *assetregistryapp.RegistrationTaskScope, request string, inputs []assetregistryapp.AssetInput) ([]string, error) {
		runID, _, ok := strings.Cut(request, "/")
		if !ok || runID == "" {
			return nil, errors.New("asset gate run unavailable")
		}
		var taskID int64
		if scope != nil {
			if scope.ExecutionID != runID {
				return nil, errors.New("asset gate run mismatch")
			}
			taskID = scope.TaskID
		}
		return approvalService.CheckAssetInputs(ctx, user, taskID, runID, inputs)
	})
	infoGatherStore := infogather.NewStore(infra.db, assetRegistrationFacade, security.vulnerabilitySvc)
	infoGatherService := infogather.NewService(infoGatherStore, &infogather.SandboxExecutor{Root: aiRoot, Resolve: skillFacade.Resolve}, func(ctx context.Context, user int) (bool, error) {
		config, err := agentSettingFacade.Resolve(ctx, user, "assistant")
		return config.RegisterAssetsEnabled, err
	})
	approvalService.SetJudgeDependencies(
		func(ctx context.Context, user int, name string, input json.RawMessage, tc *tool.ToolContext) (intercept.ReviewInput, error) {
			return airuntime.CaptureApprovalReview(ctx, aiRoot, user, name, input, tc)
		},
		airuntime.NewApprovalReviewer(func(ctx context.Context, user int, profile *string) (llm.Provider, error) {
			var selected llm.Provider
			var ok bool
			var err error
			if profile != nil {
				selected, _, ok, err = provider.ResolveProfileExclusive(ctx, user, *profile)
			} else {
				selected, _, ok, err = provider.Resolve(ctx, user)
			}
			if err != nil {
				return nil, err
			}
			if !ok || selected == nil {
				return nil, errors.New("judge model unavailable")
			}
			return selected, nil
		}),
	)
	approvalWrapper := func(ctx context.Context, user int, key string, info artexagent.RunInfo, emit func(airuntime.Activity), item tool.CoreTool) tool.CoreTool {
		call := aiapprovalapp.Call{UserID: user, RunID: airuntime.ExecutionID(ctx), ConversationID: info.SessionID, TaskID: info.TaskID, IntentID: info.IntentID, AgentName: key}
		return approvalService.Wrap(item, call, func(kind, summary, detail string) {
			if emit != nil {
				emit(airuntime.Activity{Kind: kind, Summary: summary, Detail: detail})
			}
		})
	}
	aiTraffic, err := aitraffic.NewManager(aiRoot, httpoutbound.DefaultProxyController.Proxy)
	if err != nil {
		return nil, fmt.Errorf("configure AI traffic recorder: %w", err)
	}
	aiConversationStore := aiworkbenchwiring.NewAIWorkbenchConversationStoreAdapter(infra.db)
	aiWorkbench := aiworkbenchapp.NewConversationFacade(
		aiConversationStore,
		airuntime.NewDeferredChatRunner(aiRoot), provider.Resolve, platformTools, agentSettingFacade.Resolve,
	)
	aiWorkbench.SetTrafficResolver(aitraffic.ConversationResolver(aiSQLDB, aiTraffic))
	aiWorkbench.SetExecutionGovernor(aiGovernor)
	aiWorkbench.SetSkillResolver(skillFacade.Resolve)
	aiWorkbench.SetFailureRecovery(infoGatherService.FinalizeAfterExecutionFailure)
	aiWorkbench.SetApprovalWrapper(approvalWrapper)
	aiSideQuestions := aiworkbenchapp.NewSideQuestionFacade(aiworkbenchwiring.NewAIWorkbenchSideQuestionStoreAdapter(infra.db), provider.ResolveSideModel)
	aiSideQuestions.SetExecutionGovernor(aiGovernor)
	aiMentions := aiworkbenchapp.NewChatMentionFacade(aiworkbenchwiring.NewAIWorkbenchChatMentionStoreAdapter(infra.db), assetRegistryFacade)
	aiWorkbench.SetChatMentions(aiMentions)
	aiSideQuestions.SetChatMentions(aiMentions)
	aiWorkbench.SetSideQuestions(aiSideQuestions)
	aiAttachments := aiworkbenchapp.NewAttachmentFacade(aiworkbenchwiring.NewAIWorkbenchChatAttachmentStoreAdapter(infra.db), aiworkbenchinfra.NewChatFileStorage(aiRoot))
	aiWorkbench.SetAttachments(aiAttachments)
	aiWorkbench.SetMCPAugmenter(mcpAugmenter)
	aiWorkbench.SetAgentCatalogResolver(agentCatalogFacade.ResolveForChat)
	aiWorkbench.SetProfileProviderResolver(provider.ResolveProfile)
	aiTriggerStore := aiworkbenchwiring.NewAIWorkbenchAgentTriggerStoreAdapter(infra.db)
	if err := aiTriggerStore.InitializeAgentEvents(context.Background()); err != nil {
		return nil, fmt.Errorf("initialize Agent event cursor: %w", err)
	}
	aiTriggerController := aiworkbenchapp.NewAgentTriggerController(aiTriggerStore, aiWorkbench, func(ctx context.Context) error { return aisettingswiring.SeedAISettingsDefaultAgents(ctx, infra.db) })
	graphConnector, err := airuntime.NewTaskGraphConnector(cfg.Database.DSN())
	if err != nil {
		return nil, fmt.Errorf("configure AI task graph connection: %w", err)
	}
	aiRuntimeSettings := aisettingsapp.NewRuntimeSettingFacade(aisettingswiring.NewAISettingsRuntimeSettingStoreAdapter(infra.db), secretBox, profileStore)
	aiWorkbench.SetChatBudgetResolver(aiRuntimeSettings.ChatBudget)
	aiTaskStore := aiworkbenchwiring.NewAIWorkbenchTaskStoreAdapter(infra.db)
	aiTaskLLMStore, ok := aiTaskStore.(aiworkbenchapp.TaskLLMStore)
	if !ok {
		return nil, fmt.Errorf("AI task model chain store is unavailable")
	}
	evidenceAccess, ok := aiTaskStore.(aiworkbenchapp.FindingEvidenceAccess)
	if !ok {
		return nil, fmt.Errorf("AI finding evidence authorization is unavailable")
	}
	aiFindingEvidence := aiworkbenchapp.NewFindingEvidenceFacade(evidenceAccess, aiTraffic, graphConnector, aiRoot)
	findingTrafficEnabled := func(ctx context.Context, user int) bool {
		options, err := aiRuntimeSettings.OptionsForMember(ctx, user)
		if err != nil {
			return false
		}
		on, _ := options["agent_traffic_binding"].(bool)
		return on
	}
	aiWorkbench.SetFindingTrafficEnabled(findingTrafficEnabled)
	aiTaskRuntime := airuntime.NewTaskRunner(graphConnector, aiRoot, platformTools, agentSettingFacade.Resolve)
	aiTaskRuntime.SetTaskBudgetResolver(aiRuntimeSettings.TaskBudget)
	aiTaskRuntime.SetTrafficResolver(aitraffic.TaskResolver(aiSQLDB, aiTraffic))
	aiTaskRuntime.SetExecutionGovernor(aiGovernor)
	aiTaskRuntime.SetSideDrain(aiSideQuestions.FlushSnapshots)
	aiTaskRuntime.SetFindingTrafficHooks(findingTrafficEnabled, aiFindingEvidence.RecordFinding)
	taskAssetGate := func(ctx context.Context, task airuntime.TaskExecution, ids []int64) error {
		return approvalService.CheckAssetIDs(ctx, task.UserID, task.ID, task.RunID, ids, task.TargetID)
	}
	aiTaskRuntime.SetAssetIDsGate(taskAssetGate)
	aiIntelligence := aiworkbenchapp.NewIntelligenceFacade(aiworkbenchwiring.NewAIWorkbenchIntelligenceStoreAdapter(infra.db), secretBox)
	aiIntelligence.SetAssetGate(taskAssetGate)
	aiIntelligence.SetAssetReader(assetRegistryFacade)
	aiTaskRuntime.SetIntelligenceHooks(aiIntelligence.Enabled, aiIntelligence.Record)
	aiTaskRuntime.SetApprovalWrapper(approvalWrapper)
	aiTaskRuntime.SetConstraintInjectResolver(func(ctx context.Context, user int, role string) (bool, error) {
		options, err := aiRuntimeSettings.OptionsForMember(ctx, user)
		if err != nil {
			return true, err
		}
		enabled, ok := options["constraints_inject_"+role].(bool)
		if !ok {
			return true, nil
		}
		return enabled, nil
	})
	aiTaskRuntime.SetSkillResolver(skillFacade.Resolve)
	aiTaskRuntime.SetSidePublisher(func(ctx context.Context, task airuntime.TaskExecution, snapshot sidequestion.Snapshot) {
		aiSideQuestions.Publish(ctx, task.UserID, snapshot.Parent, task.RunID, snapshot)
	})
	aiTaskRuntime.SetMCPAugmenter(mcpAugmenter)
	aiTaskRuntime.SetAgentResolver(agentCatalogFacade.ResolveForChat)
	selectTaskModel := func(ctx context.Context, user int, role string, ids []string, active *string) (airuntime.TaskModelSelection, bool, error) {
		definition, err := agentCatalogFacade.ResolveForChat(ctx, user, role)
		if err != nil {
			return airuntime.TaskModelSelection{}, false, err
		}
		if definition.LLMProfileID != nil {
			selected, model, ok, err := provider.ResolveProfile(ctx, user, *definition.LLMProfileID)
			if err != nil && !errors.Is(err, aisettingsapp.ErrProfileNotFound) {
				return airuntime.TaskModelSelection{}, false, err
			}
			if ok && selected != nil {
				return airuntime.TaskModelSelection{Provider: selected, Model: model}, true, nil
			}
		}
		if len(ids) > 0 {
			if active == nil {
				return airuntime.TaskModelSelection{}, false, airuntime.ErrTaskModelChainExhausted
			}
			selected, model, ok, err := provider.ResolveProfileExclusive(ctx, user, *active)
			if err == nil && ok && selected != nil {
				selected, err = provider.WithChainWindow(ctx, user, selected, ids)
			}
			return airuntime.TaskModelSelection{Provider: selected, Model: model, ProfileID: *active}, ok, err
		}
		selected, model, ok, err := provider.Resolve(ctx, user)
		return airuntime.TaskModelSelection{Provider: selected, Model: model}, ok, err
	}
	roleProvider := func(ctx context.Context, user int, role string) (llm.Provider, string, bool, error) {
		if role == "worker" {
			if pinned := airuntime.PinnedWorkerProfileFromContext(ctx); pinned != "" {
				return provider.ResolveProfileExclusive(ctx, user, pinned)
			}
		}
		task, scoped := airuntime.TaskExecutionFromContext(ctx)
		if !scoped {
			selection, configured, err := selectTaskModel(ctx, user, role, nil, nil)
			return selection.Provider, selection.Model, configured, err
		}
		selector := func(ctx context.Context) (airuntime.TaskModelSelection, error) {
			state, err := aiTaskLLMStore.TaskLLMState(ctx, user, task.ID)
			if err != nil {
				return airuntime.TaskModelSelection{}, err
			}
			ids := make([]string, 0, len(state.Profiles))
			for _, item := range state.Profiles {
				ids = append(ids, item.ProfileID)
			}
			selection, configured, err := selectTaskModel(ctx, user, role, ids, state.ActiveProfileID)
			if err != nil {
				return selection, err
			}
			if !configured || selection.Provider == nil {
				return selection, aiworkbenchapp.ErrModelNotConfigured
			}
			selection.Revision = state.Revision
			return selection, nil
		}
		initial, err := selector(ctx)
		if err != nil {
			return nil, "", false, err
		}
		router := &airuntime.TaskModelRouter{Context: ctx, Select: selector, Exhaust: func(ctx context.Context, selection airuntime.TaskModelSelection, cause error) (airuntime.TaskModelTransition, error) {
			result, err := aiTaskLLMStore.ExhaustTaskLLMProfile(ctx, task, selection.ProfileID, selection.Revision, cause.Error())
			return airuntime.TaskModelTransition{Stale: result.Stale, Advanced: result.Advanced, Exhausted: result.Exhausted}, err
		}}
		return router, initial.Model, true, nil
	}
	aiTaskRuntime.SetRoleProviderResolver(roleProvider)
	// A task may use independently bound models without a global default.
	taskModels := func(ctx context.Context, user int, ids []string, active *string) (llm.Provider, string, bool, error) {
		var selected llm.Provider
		var model string
		for _, role := range []string{"planner", "worker"} {
			choice, ok, err := selectTaskModel(ctx, user, role, ids, active)
			if err != nil || !ok || choice.Provider == nil {
				return nil, "", false, err
			}
			if role == "planner" {
				selected, model = choice.Provider, choice.Model
			}
		}
		return selected, model, true, nil
	}
	taskProvider := func(ctx context.Context, user int) (llm.Provider, string, bool, error) {
		return taskModels(ctx, user, nil, nil)
	}
	credentialStore := aiworkbenchwiring.NewAIWorkbenchCredentialJobStoreAdapter(infra.db)
	credentialTrafficStore, ok := credentialStore.(aiworkbenchapp.CredentialTrafficStore)
	if !ok {
		return nil, fmt.Errorf("AI credential traffic authorization is unavailable")
	}
	aiCredentialProcessor := aiworkbenchapp.NewCredentialProcessor(credentialStore, secretBox, func(ctx context.Context, user int) (llm.Provider, string, string, bool, error) {
		definition, err := agentCatalogFacade.ResolveForChat(ctx, user, "credential_processor")
		if err != nil || !definition.Enabled {
			return nil, "", "", false, err
		}
		selected, ok, err := selectTaskModel(ctx, user, "credential_processor", nil, nil)
		return selected.Provider, selected.Model, definition.SystemPrompt, ok, err
	})
	aiCredentialProcessor.SetTrafficMatcher(aiworkbenchapp.NewScopedCredentialTrafficMatcher(credentialTrafficStore, aiTraffic))
	aiTaskController := aiworkbenchapp.NewTaskController(aiTaskStore, aiTaskRuntime, taskProvider)
	aiTaskController.SetTaskModelResolver(taskModels)
	aiTaskController.SetWorkerSlotsResolver(aiRuntimeSettings.WorkerSlots)
	aiTaskFacade := aiworkbenchapp.NewTaskFacade(aiTaskStore, taskProvider, aiTaskController)
	aiTaskFacade.SetChatMentions(aiMentions)
	aiTaskFacade.SetAttachments(aiAttachments)
	aiTaskFacade.SetFindingEvidence(aiFindingEvidence)
	aiTaskFacade.SetTaskModelResolver(taskModels)
	aiVulnerabilityService := aivuln.New(infra.db, aiRoot, security.vulnerabilitySvc)
	var aiToolCatalog *aisettingsapp.ToolCatalogFacade
	platformMutation := aisettingswiring.NewAISettingsPlatformMutationStoreAdapter(infra.db)
	nativeTools := func(user int) []tool.CoreTool {
		tools := append([]tool.CoreTool{}, platformTools(user)...)
		tools = append(tools, infoGatherService.Tool(user), aiVulnerabilityService.Tool(user))
		tools = append(tools, infoGatherService.PhaseTools(user)...)
		tools = append(tools, airuntime.GlobalAssetDSLTools(aiSQLDB, user)...)
		tools = append(tools, aiworkbenchinfra.AutoTaskReadTools(aiTaskFacade, user)...)
		tools = append(tools, aiworkbenchinfra.FindingReportTools(aiTaskFacade, user, aiRoot)...)
		tools = append(tools, aiworkbenchinfra.ReporterFindingCaseTools(aiTaskFacade, user)...)
		tools = append(tools, aiworkbenchinfra.FindingTrafficTools(aiTaskFacade, user)...)
		tools = append(tools, aiworkbenchinfra.FindingRetestTools(aiTaskFacade, user)...)
		tools = append(tools, aiworkbenchinfra.TaskScopeTools(aiTaskFacade, user)...)
		tools = append(tools, aiworkbenchinfra.AutoLLMProfileTools(aisettingsapp.NewLLMProfileFacade(profileStore, secretBox), user)...)
		tools = append(tools, aiworkbenchinfra.AutoTaskHintTools(aiTaskFacade, user, findingTrafficEnabled)...)
		tools = append(tools, aiworkbenchinfra.CompanyTools(assetRegistryFacade, user)...)
		tools = append(tools, aiworkbenchinfra.TaskContextFallbackTools()...)
		tools = append(tools, aiworkbenchinfra.AutoPlatformTools(platformMutation, skillPackages, mcpFacade, func() aiworkbenchinfra.PlatformToolCatalog { return aiToolCatalog }, user)...)
		return append(tools, aiworkbenchinfra.AutoTaskWriteTools(aiTaskFacade, user)...)
	}
	aiToolCatalog = aisettingsapp.NewToolCatalogFacade(aisettingswiring.NewAISettingsToolCatalogStoreAdapter(infra.db), agentCatalogFacade, nativeTools, func(ctx context.Context, user int, row aisettingsapp.ToolRecord, base []tool.CoreTool) tool.CoreTool {
		return airuntime.BuildCustomToolWithWorkspace(row, base, func(ctx context.Context) (string, error) { return aiRuntimeSettings.PythonInterpreter(ctx, user) }, filepath.Join(aiRoot, assetregistryapp.DefaultTenantID, fmt.Sprintf("user-%d", user), "work"))
	})
	seedTools := append(nativeTools(1), traffic.SeedToolMetas()...)
	aiToolCatalog.SetSeeds(aisettingsapp.NativeToolSeeds(seedTools))
	toolTester := airuntime.CustomToolTesterWithPython(aiRoot, aiRuntimeSettings.PythonInterpreter)
	aiToolCatalog.SetTester(func(ctx context.Context, user int, row aisettingsapp.ToolRecord, params map[string]any) (tool.Result, error) {
		permitCtx, release, err := aiGovernor.Acquire(ctx, user, "tool_test", false)
		if err != nil {
			return tool.Result{}, err
		}
		defer release()
		return toolTester(airuntime.WithProcessGovernor(permitCtx, aiGovernor, user), user, row, params)
	})
	if err := aiToolCatalog.Seed(context.Background()); err != nil {
		return nil, fmt.Errorf("seed ARTEX tool catalog: %w", err)
	}
	toolLayer := func(ctx context.Context, user int, key string, base []tool.CoreTool) ([]tool.CoreTool, error) {
		resolved, err := aiToolCatalog.Resolve(ctx, user, key, base)
		if err != nil {
			return nil, err
		}
		definition, err := agentCatalogFacade.ResolveForChat(ctx, user, key)
		if err != nil {
			return nil, err
		}
		if !definition.Options.WebSearch {
			return resolved, nil
		}
		_, enabled, err := aiRuntimeSettings.SearchConfig(ctx, user)
		if err != nil {
			return nil, err
		}
		if !enabled {
			return resolved, nil
		}
		search, err := airuntime.NewDynamicWebSearch(func(ctx context.Context) (tool.WebSearchConfig, bool, error) {
			return aiRuntimeSettings.SearchConfig(ctx, user)
		})
		if err != nil {
			return nil, err
		}
		return append(resolved, search), nil
	}
	aiWorkbench.SetNativeTools(true, toolLayer)
	aiTaskRuntime.SetNativeTools(true, toolLayer)
	aiScheduleStore := aiworkbenchwiring.NewAIWorkbenchScheduleStoreAdapter(infra.db)
	aiScheduleController := aiworkbenchapp.NewScheduleController(aiScheduleStore, aiTaskController)
	aiScheduleFacade := aiworkbenchapp.NewScheduleFacade(aiScheduleStore, aiScheduleController)
	trafficAuthorizer, ok := aiTaskStore.(aiworkbenchapp.TaskTrafficAuthorizer)
	if !ok {
		return nil, fmt.Errorf("AI task traffic authorization is unavailable")
	}
	aiTaskHandler := aiworkbenchhandler.NewTaskHandler(aiTaskFacade, aiIntelligence)
	aiTaskTraffic := aiworkbenchapp.NewTaskTrafficFacade(trafficAuthorizer, aiTraffic)
	aiTaskHandler.SetTraffic(aiTaskTraffic)
	aiTaskHandler.SetFindingEvidence(aiFindingEvidence)
	aiTaskHandler.SetCredentialTraffic(aiworkbenchapp.NewCredentialTrafficDetailFacade(aiIntelligence, trafficAuthorizer, aiTraffic))
	aiTaskHandler.SetCredentialProcessor(aiCredentialProcessor)
	conversationTrafficAuthorizer, ok := aiConversationStore.(aiworkbenchapp.ConversationTrafficAuthorizer)
	if !ok {
		return nil, fmt.Errorf("AI conversation traffic authorization is unavailable")
	}
	aiWorkbenchHandler := aiworkbenchhandler.NewConversationHandler(aiWorkbench, aiSideQuestions)
	aiConversationTraffic := aiworkbenchapp.NewConversationTrafficFacade(conversationTrafficAuthorizer, aiTraffic)
	aiWorkbenchHandler.SetTraffic(aiConversationTraffic)
	aiVulnerabilityService.Traffic = aiConversationTraffic
	trafficCatalog, ok := aiTaskStore.(aiworkbenchapp.TrafficScopeCatalog)
	if !ok {
		return nil, fmt.Errorf("AI traffic scope catalog is unavailable")
	}
	aiTaskHandler.SetGlobalTraffic(aiworkbenchapp.NewGlobalTrafficFacade(aiTraffic, trafficCatalog, aiTaskTraffic, aiConversationTraffic))
	aiSettingsHandler := aisettingshandler.NewLLMProfileHandler(aisettingsapp.NewLLMProfileFacade(profileStore, secretBox), provider)
	aiSettingsHandler.SetUsage(aisettingsapp.NewLLMUsageFacade(aisettingswiring.NewAISettingsLLMUsageReader(infra.db)))
	aiSettingsHandler.SetRecordings(aisettingsapp.NewLLMRecordingFacade(aisettingswiring.NewAISettingsLLMRecordingReader(infra.db), secretBox))
	assetMonitor := assetmonitorapp.NewFacade(assetmonitorrepo.NewStore(infra.db), locationCoordinator, assetmonitorinfra.NewDNSResolver(nil))
	return &deps{
		infoGatherHandler:              infogather.NewHandler(infoGatherStore),
		aiVulnerabilityService:         aiVulnerabilityService,
		assetMonitorHandler:            assetmonitorhandler.NewHandler(assetMonitor),
		assetMonitor:                   assetMonitor,
		spaceSearchHandler:             spacesearchhandler.NewFofaHandler(fofaFacade),
		httpRepeaterHandler:            repeaterhandler.New(httpRepeater),
		assetRegistryHandler:           assetregistryhandler.NewAssetEntityHandler(assetRegistryFacade),
		assetRegistrationHandler:       assetregistryhandler.NewAssetRegistrationHandler(assetRegistrationFacade),
		assetRegistryBackfill:          assetregistryapp.NewBackfillController(assetRegistryStore),
		assetRootProjection:            assetregistryapp.NewRootDomainController(assetregistrywiring.NewAssetRegistryRootDomainStoreAdapter(infra.db)),
		assetApplicationProjection:     assetregistryapp.NewApplicationProjectionController(assetregistrywiring.NewAssetRegistryApplicationProjectionStoreAdapter(infra.db)),
		aiSettingsHandler:              aiSettingsHandler,
		aiAgentSettingHandler:          aisettingshandler.NewAgentSettingHandler(agentSettingFacade),
		aiAgentCatalogHandler:          aisettingshandler.NewAgentCatalogHandler(agentCatalogFacade),
		aiAgentControlHandler:          aisettingshandler.NewAgentControlHandler(agentControlFacade),
		aiSkillPackageHandler:          aisettingshandler.NewSkillPackageHandler(skillPackages),
		aiToolCatalogHandler:           aisettingshandler.NewToolCatalogHandler(aiToolCatalog),
		aiRuntimeSettingHandler:        aisettingshandler.NewRuntimeSettingHandler(aiRuntimeSettings),
		aiToolSettingHandler:           aisettingshandler.NewToolSettingHandler(aisettingsapp.NewToolSettingFacade(aisettingswiring.NewAISettingsToolSettingStoreAdapter(infra.db))),
		aiSkillHandler:                 aisettingshandler.NewSkillHandler(skillFacade),
		aiMCPHandler:                   aisettingshandler.NewMCPHandler(mcpFacade),
		aiApprovalHandler:              aiapprovalhandler.NewHandler(approvalService),
		aiWorkbenchHandler:             aiWorkbenchHandler,
		aiWorkbench:                    aiWorkbench,
		aiTraffic:                      aiTraffic,
		aiTaskHandler:                  aiTaskHandler,
		aiNotificationHandler:          ainotification.NewHandler(aiNotificationStore),
		aiNotificationWorker:           ainotification.NewWorker(aiNotificationStore),
		aiTaskController:               aiTaskController,
		aiCredentialProcessor:          aiCredentialProcessor,
		aiScheduleHandler:              aiworkbenchhandler.NewScheduleHandler(aiScheduleFacade),
		aiScheduleController:           aiScheduleController,
		aiAgentTriggerController:       aiTriggerController,
		tokenVersionReader:             identity.tokenVersionReader,
		mcpKeyService:                  identity.mcpKeyService,
		mcpHandler:                     mcpHandler,
		mcpClients:                     mcpexternal.New(infra.db, assetRegistryFacade, aiVulnerabilityService, security.vulnerabilitySvc),
		healthHandler:                  assethandler.NewHealthHandler(infra.db, infra.redisClient),
		authHandler:                    identity.authHandler,
		userHandler:                    identity.userHandler,
		orgHandler:                     identity.orgHandler,
		targetHandler:                  catalog.targetHandler,
		engineCatalogHandler:           catalog.engineCatalogHandler,
		scanWorkflowCatalogHandler:     catalog.scanWorkflowCatalogHandler,
		wordlistHandler:                catalog.wordlistHandler,
		subfinderAPIKeySettingsHandler: catalog.subfinderAPIKeySettingsHandler,
		blacklistPolicyHandler:         blacklist.blacklistPolicyHandler,
		websiteHandler:                 asset.websiteHandler,
		subdomainHandler:               asset.subdomainHandler,
		endpointHandler:                asset.endpointHandler,
		directoryHandler:               asset.directoryHandler,
		hostPortHandler:                asset.hostPortHandler,
		screenshotHandler:              asset.screenshotHandler,
		globalAssetSearchHandler:       asset.globalAssetSearchHandler,
		globalAssetExportHandler:       asset.globalAssetExportHandler,
		assetStatisticsHandler:         asset.assetStatisticsHandler,
		vulnerabilityHandler:           security.vulnerabilityHandler,
		scanHandler:                    scan.scanHandler,
		scheduledScanHandler:           scan.scheduledScanHandler,
		taskProgressLogHandler:         scan.taskProgressLogHandler,
		fingerprintHandler:             fingerprint.handler,
		fingerprintArtifacts:           fingerprint.artifacts,
		nucleiPocRepo:                  repos.nucleiPocRepo,

		agentHandler:                agent.agentHandler,
		agentLogHandler:             agent.agentLogHandler,
		agentClusterSummaryHandler:  agent.agentClusterSummaryHandler,
		agentLicenseHandler:         agent.agentLicenseHandler,
		agentLocationMapHandler:     agent.agentLocationMapHandler,
		serverLogHandler:            system.serverLogHandler,
		runtimeMetricsHandler:       system.runtimeMetricsHandler,
		httpProxySettingsHandler:    system.httpProxySettingsHandler,
		runtimeMetricsJob:           system.runtimeMetricsJob,
		scanHistoryRetentionJob:     scanHistoryRetentionJob,
		scheduledScanController:     scan.scheduledScanController,
		occurrenceRetentionJob:      scan.occurrenceRetentionJob,
		targetCleanupRunner:         targetCleanupRunner,
		registrationTokenCleanupJob: agent.registrationTokenCleanupJob,
		notificationOutboxWorker:    notification.OutboxWorker,
		notificationRetentionJob:    notification.RetentionJob,
		notificationInboxHandler:    notification.InboxHandler,
		notificationSSEHandler:      notification.SSEHandler,
		loginVisualHandler:          loginVisual.Handler,
		nucleiPocHandler:            nucleipocHandler.NewNucleiPOCHandler(nucleiPocService),
		nucleiPocRunner:             nucleiPocRunner,
		nucleiPocRetentionJob:       nucleiPocRetention,

		websiteSnapshotHandler:       snapshot.websiteSnapshotHandler,
		subdomainSnapshotHandler:     snapshot.subdomainSnapshotHandler,
		endpointSnapshotHandler:      snapshot.endpointSnapshotHandler,
		directorySnapshotHandler:     snapshot.directorySnapshotHandler,
		hostPortSnapshotHandler:      snapshot.hostPortSnapshotHandler,
		screenshotSnapshotHandler:    snapshot.screenshotSnapshotHandler,
		vulnerabilitySnapshotHandler: snapshot.vulnerabilitySnapshotHandler,

		agentRepo:    repos.agentRepo,
		scanRepo:     repos.scanRepo,
		scanTaskRepo: repos.scanTaskRepo,
		scanTaskSvc:  scan.scanTaskSvc,

		agentControlService:           agent.agentControlService,
		agentTaskService:              agent.agentTaskService,
		agentLocationObserver:         agent.agentLocationObserver,
		serverLocationReader:          system.serverLocationReader,
		serverLocationScheduler:       system.serverLocationScheduler,
		geolocationCoordinator:        locationCoordinator,
		executionProviderConfigSource: catalog.executionProviderConfigSource,
		executionWordlistSource:       catalog.executionWordlistSource,
		subdomainSnapshotRepo:         repos.subdomainSnapshotRepo,
		hostPortSnapshotRepo:          repos.hostPortSnapshotRepo,
		websiteSnapshotRepo:           repos.websiteSnapshotRepo,
		endpointSnapshotRepo:          repos.endpointSnapshotRepo,
		subdomainRepo:                 repos.subdomainRepo,
		hostPortRepo:                  repos.hostPortRepo,
		websiteRepo:                   repos.websiteRepo,
		endpointRepo:                  repos.endpointRepo,
		subdomainSnapshotService:      snapshot.subdomainSnapshotService,
		websiteSnapshotService:        snapshot.websiteSnapshotService,
		endpointSnapshotService:       snapshot.endpointSnapshotService,
		hostPortSnapshotService:       snapshot.hostPortSnapshotService,
		screenshotSnapshotService:     snapshot.screenshotSnapshotService,
		resultIngestService:           resultIngest,
		taskProgressLogService:        scan.taskProgressLogService,
		runtimeStreamRegistry:         streamRegistry,
		upgradeHandler:                upgradeHandler,
		productUpdateHandler:          productUpdateHandler,
		upgradeRecoveryJob:            upgradeRecoveryJob,
	}, nil
}

func newRepositoryBundle(infra *infra, cfg *config.Config) *repositoryBundle {
	db := infra.db
	notificationOutbox := notificationrepo.NewOutboxRepository(db)
	notificationProducer := notificationrepo.NewProducerWriter(notificationOutbox, notificationdomain.VulnerabilitySeverity(cfg.Notification.VulnerabilityThreshold))
	licenses := agentrepo.NewAgentLicenseRepository(db)
	return &repositoryBundle{
		userRepo:                      identityrepo.NewUserRepository(db),
		mcpKeyRepo:                    identityrepo.NewMCPKeyRepository(db),
		orgRepo:                       identityrepo.NewOrganizationRepository(db),
		targetRepo:                    catalogrepo.NewTargetRepository(db),
		blacklistPolicyRepo:           blacklistrepo.NewPolicyRepository(db),
		engineRepo:                    catalogrepo.NewEngineRepository(db),
		wordlistRepo:                  catalogrepo.NewWordlistRepository(db),
		scanWorkflowRepo:              catalogrepo.NewScanWorkflowRepository(db),
		websiteRepo:                   assetrepo.NewWebsiteRepository(db),
		subdomainRepo:                 assetrepo.NewSubdomainRepository(db),
		endpointRepo:                  assetrepo.NewEndpointRepository(db),
		directoryRepo:                 assetrepo.NewDirectoryRepository(db),
		hostPortRepo:                  assetrepo.NewHostPortRepository(db),
		assetStatisticsRepo:           assetrepo.NewAssetStatisticsRepository(db),
		screenshotRepo:                assetrepo.NewScreenshotRepository(db),
		vulnerabilityRepo:             securityrepo.NewVulnerabilityRepository(db),
		scanRepo:                      scanrepo.NewScanRepository(db, notificationProducer).WithAgentExecutionGuard(licenses),
		scheduledScanRepo:             scheduledscanrepo.NewScheduledScanRepository(db),
		targetCleanupRepo:             targetcleanuprepo.NewTargetCleanupRepository(db),
		taskProgressLogRepo:           scanrepo.NewTaskProgressLogRepository(db),
		subfinderProviderSettingsRepo: catalogrepo.NewSubfinderProviderSettingsRepository(db),
		websiteSnapshotRepo:           snapshotrepo.NewWebsiteSnapshotRepository(db),
		subdomainSnapshotRepo:         snapshotrepo.NewSubdomainSnapshotRepository(db),
		endpointSnapshotRepo:          snapshotrepo.NewEndpointSnapshotRepository(db),
		directorySnapshotRepo:         snapshotrepo.NewDirectorySnapshotRepository(db),
		hostPortSnapshotRepo:          snapshotrepo.NewHostPortSnapshotRepository(db),
		screenshotSnapshotRepo:        snapshotrepo.NewScreenshotSnapshotRepository(db),
		vulnerabilitySnapshotRepo:     snapshotrepo.NewVulnerabilitySnapshotRepository(db),
		agentRepo:                     agentrepo.NewAgentRepository(db, notificationProducer),
		agentLicenseRepo:              licenses,
		agentLocationRepo:             agentrepo.NewAgentLocationRepository(db),
		registrationTokenRepo:         agentrepo.NewRegistrationTokenRepository(db),
		serverLocationRepo:            systemrepo.NewServerLocationRepository(db),
		scanTaskRepo:                  scanrepo.NewScanTaskRepository(db, licenses),
		fingerprintRepo:               fingerprintrepo.NewFingerprintRepository(db),
		notificationProducer:          notificationProducer,
		nucleiPocRepo:                 nucleipocwiring.NewNucleiPOCRepository(db, notificationProducer),
	}
}

func wireIdentityModule(repos *repositoryBundle, infra *infra) identityModuleHandlers {
	identityUserQueryStore := identitywiring.NewIdentityUserQueryStoreAdapter(repos.userRepo)
	identityUserCommandStore := identitywiring.NewIdentityUserCommandStoreAdapter(repos.userRepo)
	identityOrgQueryStore := identitywiring.NewIdentityOrganizationQueryStoreAdapter(repos.orgRepo)
	identityOrgCommandStore := identitywiring.NewIdentityOrganizationCommandStoreAdapter(repos.orgRepo)
	identityAuthUserStore := identitywiring.NewIdentityAuthUserStoreAdapter(repos.userRepo)
	identityTokenVersionReader := identitywiring.NewIdentityTokenVersionReaderAdapter(repos.userRepo)
	mcpKeyService := identityservice.NewMCPKeyLifecycleService(repos.mcpKeyRepo, identityinfra.NewMCPKeySecretGenerator())

	userQueryService := identityservice.NewUserQueryService(identityUserQueryStore)
	userCommandService := identityservice.NewUserCommandService(identityUserCommandStore, identityservice.NewAuthPasswordHasher())
	orgQueryService := identityservice.NewOrganizationQueryService(identityOrgQueryStore)
	orgCommandService := identityservice.NewOrganizationCommandService(identityOrgCommandStore)
	authCommandService := identityservice.NewAuthCommandService(identityAuthUserStore, identityservice.NewAuthPasswordVerifier(), infra.jwtManager)

	userSvc := identityservice.NewUserFacade(userQueryService, userCommandService)
	orgSvc := identityservice.NewOrganizationFacade(orgQueryService, orgCommandService)
	authSvc := identityservice.NewAuthFacade(authCommandService)

	return identityModuleHandlers{
		authHandler:        identityhandler.NewAuthHandler(authSvc),
		userHandler:        identityhandler.NewUserHandler(userSvc, mcpKeyService),
		orgHandler:         identityhandler.NewOrganizationHandler(orgSvc),
		organizationSvc:    orgSvc,
		tokenVersionReader: identityTokenVersionReader,
		mcpKeyService:      mcpKeyService,
	}
}

func wireCatalogModule(repos *repositoryBundle, cfg *config.Config, infra *infra) catalogModuleHandlers {
	catalogTargetQueryStore := catalogwiring.NewCatalogTargetQueryStoreAdapter(repos.targetRepo)
	catalogTargetCommandStore := catalogwiring.NewCatalogTargetCommandStoreAdapter(repos.targetRepo)
	catalogWordlistQueryStore := catalogwiring.NewCatalogWordlistQueryStoreAdapter(repos.wordlistRepo)
	catalogWordlistCommandStore := catalogwiring.NewCatalogWordlistCommandStoreAdapter(repos.wordlistRepo)
	catalogOrganizationTargetBindingStore := catalogwiring.NewCatalogOrganizationTargetBindingStoreAdapter(repos.orgRepo)
	catalogTransactionCoordinator := catalogwiring.NewCatalogTransactionCoordinator(infra.db)
	engineCatalogQueryStore := catalogwiring.NewCatalogEngineCatalogQueryStoreAdapter(infra.installedEngineQuery)
	subfinderAPIKeySettingsStore := catalogwiring.NewCatalogSubfinderProviderSettingsStoreAdapter(repos.subfinderProviderSettingsRepo)
	executionProviderSettingsStore := catalogwiring.NewCatalogExecutionSubfinderProviderSettingsStoreAdapter(repos.subfinderProviderSettingsRepo)

	targetQueryService := catalogservice.NewTargetQueryService(catalogTargetQueryStore)
	targetCommandService := catalogservice.NewTargetCommandService(catalogTargetCommandStore, catalogOrganizationTargetBindingStore, catalogTransactionCoordinator)
	wordlistFileStore := catalogservice.NewLocalWordlistFileStore()
	wordlistQueryService := catalogservice.NewWordlistQueryService(catalogWordlistQueryStore, wordlistFileStore)
	wordlistCommandService := catalogservice.NewWordlistCommandService(catalogWordlistCommandStore, cfg.Storage.WordlistsBasePath, wordlistFileStore)

	targetSvc := catalogservice.NewTargetFacade(targetQueryService, targetCommandService)
	wordlistSvc := catalogservice.NewWordlistFacade(wordlistQueryService, wordlistCommandService)
	scanWorkflowCatalogSvc, err := catalogwiring.NewScanWorkflowManagementService(repos.scanWorkflowRepo, infra.installedEngineQuery)
	if err != nil {
		panic(fmt.Sprintf("initialize scan workflow management service: %v", err))
	}
	scanWorkflowProfileSvc, err := catalogwiring.NewScanWorkflowProfileService(repos.scanWorkflowRepo, infra.installedEngineQuery)
	if err != nil {
		panic(fmt.Sprintf("initialize scan workflow Profile service: %v", err))
	}
	engineCatalogSvc := catalogservice.NewEngineCatalogFacade(engineCatalogQueryStore)
	subfinderAPIKeySettingsSvc := catalogservice.NewSubfinderAPIKeySettingsService(subfinderAPIKeySettingsStore)
	executionProviderConfigSource := catalogservice.NewExecutionProviderConfigSource(executionProviderSettingsStore)
	executionWordlistSource := catalogservice.NewExecutionWordlistSource(wordlistQueryService)

	return catalogModuleHandlers{
		targetHandler:                  cataloghandler.NewTargetHandler(targetSvc),
		engineCatalogHandler:           cataloghandler.NewEngineCatalogHandler(engineCatalogSvc, infra.operatorEngineInstall),
		scanWorkflowCatalogHandler:     cataloghandler.NewScanWorkflowManagementHandler(scanWorkflowCatalogSvc, scanWorkflowProfileSvc),
		wordlistHandler:                cataloghandler.NewWordlistHandler(wordlistSvc),
		subfinderAPIKeySettingsHandler: cataloghandler.NewSubfinderAPIKeySettingsHandler(subfinderAPIKeySettingsSvc),
		wordlistService:                wordlistSvc,
		targetSvc:                      targetSvc,
		scanWorkflowService:            scanWorkflowCatalogSvc,
		scanWorkflowProfileService:     scanWorkflowProfileSvc,
		engineCatalogService:           engineCatalogSvc,
		executionProviderConfigSource:  executionProviderConfigSource,
		executionWordlistSource:        executionWordlistSource,
	}
}

func wireBlacklistModule(repos *repositoryBundle) (blacklistModuleHandlers, error) {
	policyStore := blacklistwiring.NewBlacklistPolicyStoreAdapter(repos.blacklistPolicyRepo)
	policyService, err := blacklistwiring.NewBlacklistPolicyApplicationService(policyStore)
	if err != nil {
		return blacklistModuleHandlers{}, err
	}
	policyHandler, err := blacklisthandler.NewBlacklistPolicyHandler(policyService)
	if err != nil {
		return blacklistModuleHandlers{}, err
	}
	return blacklistModuleHandlers{blacklistPolicyHandler: policyHandler, blacklistPolicyService: policyService}, nil
}

func wireAssetModule(repos *repositoryBundle, infra *infra, tokenVersions auth.TokenVersionReader) assetModuleWiring {
	assetTargetLookup := assetwiring.NewAssetTargetLookupAdapter(repos.targetRepo)
	assetWebsiteStore := assetwiring.NewAssetWebsiteStoreAdapter(repos.websiteRepo)
	assetSubdomainStore := assetwiring.NewAssetSubdomainStoreAdapter(repos.subdomainRepo)
	assetEndpointStore := assetwiring.NewAssetEndpointStoreAdapter(repos.endpointRepo)
	assetDirectoryStore := assetwiring.NewAssetDirectoryStoreAdapter(repos.directoryRepo)
	assetHostPortStore := assetwiring.NewAssetHostPortStoreAdapter(repos.hostPortRepo)
	assetScreenshotStore := assetwiring.NewAssetScreenshotStoreAdapter(repos.screenshotRepo)
	assetGlobalWebsiteSearchStore := assetwiring.NewAssetGlobalWebsiteSearchStoreAdapter(repos.websiteRepo)
	assetGlobalEndpointSearchStore := assetwiring.NewAssetGlobalEndpointSearchStoreAdapter(repos.endpointRepo)
	assetStatisticsStore := assetwiring.NewAssetStatisticsStoreAdapter(repos.assetStatisticsRepo)
	globalAssetSearchSvc := assetservice.NewGlobalAssetSearchService(assetGlobalWebsiteSearchStore, assetGlobalEndpointSearchStore, assetScreenshotStore)
	grantStore := assetwiring.NewAssetGlobalExportGrantStoreAdapter(assetrepo.NewGlobalAssetExportGrantStore(infra.redisClient))
	grantService := assetservice.NewGlobalAssetExportService(grantStore)
	exportSnapshotStore := assetwiring.NewAssetGlobalExportSnapshotStoreAdapter(assetrepo.NewGlobalAssetExportSnapshotStore(infra.db))
	exportRunner := assetservice.NewGlobalAssetExportServiceRunner(exportSnapshotStore)
	assetStatisticsSvc := assetservice.NewAssetStatisticsQueryService(assetStatisticsStore, time.Now)

	websiteSvc := assetservice.NewWebsiteFacade(
		assetservice.NewWebsiteQueryService(assetWebsiteStore, assetScreenshotStore, assetTargetLookup),
		assetservice.NewWebsiteCommandService(assetWebsiteStore, assetTargetLookup),
	)
	subdomainSvc := assetservice.NewSubdomainFacade(
		assetservice.NewSubdomainQueryService(assetSubdomainStore, assetTargetLookup),
		assetservice.NewSubdomainCommandService(assetSubdomainStore, assetTargetLookup),
	)
	endpointSvc := assetservice.NewEndpointFacade(
		assetservice.NewEndpointQueryService(assetEndpointStore, assetTargetLookup),
		assetservice.NewEndpointCommandService(assetEndpointStore, assetTargetLookup),
	)
	directorySvc := assetservice.NewDirectoryFacade(
		assetservice.NewDirectoryQueryService(assetDirectoryStore, assetTargetLookup),
		assetservice.NewDirectoryCommandService(assetDirectoryStore, assetTargetLookup),
	)
	hostPortSvc := assetservice.NewHostPortFacade(
		assetservice.NewHostPortQueryService(assetHostPortStore, assetTargetLookup),
		assetservice.NewHostPortCommandService(assetHostPortStore, assetTargetLookup),
	)
	screenshotSvc := assetservice.NewScreenshotFacade(
		assetservice.NewScreenshotQueryService(assetScreenshotStore, assetTargetLookup),
		assetservice.NewScreenshotCommandService(assetScreenshotStore, assetTargetLookup),
	)

	return assetModuleWiring{
		websiteHandler:           websitehandler.NewWebsiteHandler(websiteSvc),
		subdomainHandler:         subdomainhandler.NewSubdomainHandler(subdomainSvc),
		endpointHandler:          endpointhandler.NewEndpointHandler(endpointSvc),
		directoryHandler:         directoryhandler.NewDirectoryHandler(directorySvc),
		hostPortHandler:          hostporthandler.NewHostPortHandler(hostPortSvc),
		screenshotHandler:        screenshothandler.NewScreenshotHandler(screenshotSvc),
		globalAssetSearchHandler: searchhandler.NewGlobalAssetSearchHandler(globalAssetSearchSvc),
		globalAssetExportHandler: searchhandler.NewGlobalAssetExportHandler(grantService, exportRunner, tokenVersions),
		assetStatisticsHandler:   assethandler.NewAssetStatisticsHandler(assetStatisticsSvc),
		websiteSvc:               websiteSvc,
		subdomainSvc:             subdomainSvc,
		endpointSvc:              endpointSvc,
		directorySvc:             directorySvc,
		hostPortSvc:              hostPortSvc,
		screenshotSvc:            screenshotSvc,
	}
}

func wireFingerprintModule(repos *repositoryBundle, cfg *config.Config) (fingerprintModuleWiring, error) {
	artifacts, err := fingerprintapp.NewFingerprintArtifactService(repos.fingerprintRepo, cfg.Storage.FingerprintArtifactsBasePath)
	if err != nil {
		return fingerprintModuleWiring{}, err
	}
	service := fingerprintapp.NewFacadeWithArtifacts(repos.fingerprintRepo, artifacts)
	return fingerprintModuleWiring{handler: fingerprinthandler.NewFingerprintHandler(service), artifacts: artifacts}, nil
}

func wireSecurityModule(repos *repositoryBundle) securityModuleWiring {
	securityVulnerabilityStore := securitywiring.NewSecurityVulnerabilityStoreAdapter(repos.vulnerabilityRepo)
	securityTargetLookup := securitywiring.NewSecurityTargetLookupAdapter(repos.targetRepo)
	vulnerabilitySvc := securityservice.NewVulnerabilityFacade(securityVulnerabilityStore, securityTargetLookup)

	return securityModuleWiring{
		vulnerabilityHandler: securityhandler.NewVulnerabilityHandler(vulnerabilitySvc),
		vulnerabilitySvc:     vulnerabilitySvc,
	}
}

func wireScanModule(repos *repositoryBundle, infra *infra, cfg *config.Config, notifier agentservice.AgentMessagePublisher, wordlistCatalog scaninfra.PlanTaskWordlistCatalog, blacklistPolicyService blacklistapp.BlacklistPolicyApplicationService) (scanModuleWiring, error) {
	scanQueryStore := scanwiring.NewScanQueryStoreAdapter(repos.scanRepo)
	effectivePolicyResolver, err := scanwiring.NewEffectiveBlacklistPolicyResolverAdapter(blacklistPolicyService)
	if err != nil {
		return scanModuleWiring{}, err
	}
	scanCommandStore := scanwiring.NewScanCommandStoreAdapter(repos.scanRepo, effectivePolicyResolver)
	if scanCommandStore == nil {
		return scanModuleWiring{}, fmt.Errorf("initialize Scan command store")
	}
	scanDomainRepository := scanwiring.NewScanDomainRepositoryAdapter(repos.scanRepo)
	scanTaskStore := scanwiring.NewScanTaskStoreAdapter(repos.scanTaskRepo)
	scanTaskRuntimeScanStore := scanwiring.NewScanTaskRuntimeScanStoreAdapter(repos.scanRepo)
	taskProgressLogQueryStore := taskprogresslogwiring.NewTaskProgressLogQueryStoreAdapter(repos.taskProgressLogRepo)
	taskProgressLogCommandStore := taskprogresslogwiring.NewTaskProgressLogCommandStoreAdapter(repos.taskProgressLogRepo)
	scanStopStore := scanwiring.NewScanStopStoreAdapter(repos.scanRepo)
	scanTargetLookup := scanwiring.NewScanTargetLookupAdapter(
		repos.targetRepo,
		catalogservice.NewTargetCommandService(catalogwiring.NewCatalogTargetCommandStoreAdapter(repos.targetRepo), nil),
		repos.orgRepo,
	)
	taskProgressLogLookup := taskprogresslogwiring.NewTaskProgressLogScanLookupAdapter(repos.scanRepo)
	taskProgressLogSvc := taskprogresslogwiring.NewTaskProgressLogApplicationService(taskProgressLogQueryStore, taskProgressLogCommandStore, taskProgressLogLookup)
	runtimeLogs := scanapp.NewTaskRuntimeLogAppender(taskProgressLogSvc)
	configResourceResolver, configResourceValidator, err := scanwiring.NewConfigResourceValidationComponents(infra.installedEngineQuery, wordlistCatalog)
	if err != nil {
		return scanModuleWiring{}, err
	}
	scanSvc, err := scanwiring.NewScanApplicationService(
		scanQueryStore,
		scanCommandStore,
		scanDomainRepository,
		scanStopStore,
		notifier,
		scanTargetLookup,
		scanwiring.NewScanCreateAgentLookupAdapter(repos.agentRepo),
		scanwiring.NewScanAgentExecutionReaderAdapter(repos.agentLicenseRepo),
		repos.scanWorkflowRepo,
		repos.engineRepo,
		infra.installedEngineQuery,
		configResourceResolver,
		runtimeLogs,
	)
	if err != nil {
		return scanModuleWiring{}, err
	}
	scheduledScanSvc := scheduledscanapp.NewScheduledScanService(repos.scheduledScanRepo, repos.scanWorkflowRepo).
		WithAgentLookup(scanwiring.NewScanCreateAgentLookupAdapter(repos.agentRepo)).
		WithConfigResourceValidator(configResourceValidator).
		WithOccurrenceStore(repos.scheduledScanRepo)
	scheduledScanController := scheduledscanapp.NewSchedulerController(
		repos.scheduledScanRepo,
		scheduledscanapp.NewOccurrenceDispatcher(scanSvc),
	)
	occurrenceRetentionJob := scheduledscanapp.NewOccurrenceRetentionJob(repos.scheduledScanRepo)
	scanTaskSvc := scanwiring.NewScanTaskApplicationService(scanTaskStore, scanTaskRuntimeScanStore, repos.subdomainSnapshotRepo, repos.hostPortSnapshotRepo, runtimeLogs)

	return scanModuleWiring{
		scanHandler: scanhandler.NewScanHandler(scanSvc, scanhandler.ScanHistoryRetentionPolicy{
			MinimumRetentionSeconds: int64(cfg.ScanHistoryRetention.Retention.Seconds()),
			AutomaticCleanupEnabled: cfg.ScanHistoryRetention.Mode == "enforce",
		}),
		scheduledScanHandler:    scheduledscanhandler.NewScheduledScanHandler(scheduledScanSvc),
		taskProgressLogHandler:  scanhandler.NewTaskProgressLogHandler(taskProgressLogSvc),
		scanTaskBridge:          scanTaskSvc,
		scanTaskSvc:             scanTaskSvc,
		taskProgressLogService:  taskProgressLogSvc,
		scanSvc:                 scanSvc,
		scheduledScanController: scheduledScanController,
		occurrenceRetentionJob:  occurrenceRetentionJob,
	}, nil
}

func wireAgentModule(
	repos *repositoryBundle,
	infra *infra,
	cfg *config.Config,
	scanTaskBridge agentservice.ScanTaskBridgePort,
	messageBus agentservice.AgentMessagePublisher,
	locationCoordinator *geolocation.Coordinator,
	serverLocationReader agentservice.ServerLocationReader,
) (agentModuleHandlers, error) {
	agentClock := agentinfra.NewSystemClock()
	agentTokenGenerator := agentinfra.NewCryptoTokenGenerator()
	agentSvc := agentservice.NewAgentFacade(
		agentservice.NewAgentQueryService(repos.agentRepo),
		agentservice.NewAgentCommandService(repos.agentRepo).WithDeletionLifecycle(repos.scanRepo),
		agentservice.NewAgentRegistrationService(repos.agentRepo, repos.registrationTokenRepo, agentClock, agentTokenGenerator),
	)
	agentControlSvc := agentservice.NewAgentControlLifecycleService(
		repos.agentRepo,
		infra.heartbeatCache,
		messageBus,
		agentClock,
		infra.agentVersion,
		infra.agentImageRef,
	)
	agentTaskSvc := agentservice.NewAgentTaskService(scanTaskBridge)
	locationLookup, err := agentinfra.NewCoordinatorAgentLocationLookup(locationCoordinator)
	if err != nil {
		return agentModuleHandlers{}, err
	}
	locationObserver, err := agentservice.NewAgentLocationObserver(repos.agentLocationRepo, locationLookup, agentClock)
	if err != nil {
		return agentModuleHandlers{}, err
	}
	agentLicenseService, err := agentservice.NewAgentLicenseService(repos.agentLicenseRepo, agentClock)
	if err != nil {
		return agentModuleHandlers{}, err
	}
	clusterSummaryService, err := agentservice.NewAgentClusterSummaryService(repos.agentRepo, agentClock, repos.agentLicenseRepo)
	if err != nil {
		return agentModuleHandlers{}, err
	}
	locationMapService, err := agentservice.NewAgentLocationMapService(repos.agentRepo, serverLocationReader, agentClock)
	if err != nil {
		return agentModuleHandlers{}, err
	}
	lokiLogQuerySvc := agentservice.NewLokiLogQueryService(infra.lokiClient, cfg.JWT.Secret)

	return agentModuleHandlers{
		agentHandler: agenthandler.NewAgentHandler(
			agentSvc,
			agentControlSvc,
			infra.agentVersion,
			cfg.PublicURL,
			fmt.Sprintf("http://server:%d", cfg.Server.GRPCPort),
			infra.agentImageRef,
			infra.sharedDataVolumeBind,
			infra.heartbeatCache,
		),
		agentLogHandler:            agenthandler.NewAgentLogHandler(agentSvc, lokiLogQuerySvc),
		agentClusterSummaryHandler: agenthandler.NewAgentClusterSummaryHandler(clusterSummaryService),
		agentLicenseHandler:        agenthandler.NewAgentLicenseHandler(agentLicenseService),
		agentLocationMapHandler:    agenthandler.NewAgentLocationMapHandler(locationMapService),

		agentControlService:         agentControlSvc,
		agentTaskService:            agentTaskSvc,
		agentService:                agentSvc,
		lokiLogQueryService:         lokiLogQuerySvc,
		agentLocationObserver:       locationObserver,
		registrationTokenCleanupJob: agentservice.NewRegistrationTokenCleanupJob(repos.registrationTokenRepo, agentClock, time.Hour),
	}, nil
}

func wireSystemModule(
	repos *repositoryBundle,
	infra *infra,
	cfg *config.Config,
	locationCoordinator *geolocation.Coordinator,
) (systemModuleHandlers, error) {
	httpProxySettingsService := systemservice.NewHTTPProxySettingsService(
		systemrepo.NewHTTPProxySettingsRepository(infra.db),
		systemrepo.NewActiveSystemSuperuserRepository(infra.db),
		airuntime.SecretBox{},
		httpoutbound.DefaultProxyController,
	)
	if err := httpProxySettingsService.Initialize(context.Background()); err != nil {
		return systemModuleHandlers{}, err
	}
	serverLogSvc := systemservice.NewServerLogService(infra.lokiClient, cfg.JWT.Secret)
	// Runtime metrics describe the server shared-data boundary, not any optional
	// business subdirectory that may be created lazily by a specific module.
	metricsRoot := sharedstorage.SharedDataRoot
	if cfg.LocalMode {
		metricsRoot = cfg.LocalDataRoot
	}
	runtimeMetricsSvc := systemservice.NewRuntimeMetricsService(
		systemservice.NewOSRuntimeMetricsSampler(metricsRoot),
		systemservice.NewOSRuntimeMetricsMetadataProvider(metricsRoot),
	)
	serverLocationRuntime := systeminfra.NewServerLocationRuntime()
	serverLocationLookup, err := systeminfra.NewCoordinatorServerLocationLookup(locationCoordinator)
	if err != nil {
		return systemModuleHandlers{}, err
	}
	serverLocationReader, err := systemservice.NewServerLocationReader(repos.serverLocationRepo, serverLocationRuntime)
	if err != nil {
		return systemModuleHandlers{}, err
	}
	serverLocationScheduler, err := systemservice.NewServerLocationScheduler(repos.serverLocationRepo, serverLocationLookup, serverLocationRuntime)
	if err != nil {
		return systemModuleHandlers{}, err
	}
	return systemModuleHandlers{
		serverLogHandler:         systemhandler.NewServerLogHandler(serverLogSvc),
		serverLogService:         serverLogSvc,
		runtimeMetricsHandler:    systemhandler.NewRuntimeMetricsHandler(runtimeMetricsSvc),
		httpProxySettingsHandler: systemhandler.NewHTTPProxySettingsHandler(httpProxySettingsService),
		runtimeMetricsJob:        runtimeMetricsSvc,
		serverLocationReader:     serverLocationReader,
		serverLocationScheduler:  serverLocationScheduler,
	}, nil
}

func wireSnapshotModule(
	db *gorm.DB,
	repos *repositoryBundle,
	asset assetModuleWiring,
	security securityModuleWiring,
) snapshotModuleHandlers {
	materializationCoordinator := snapshotwiring.NewSnapshotMaterializationCoordinator(db, repos.scanRepo)
	snapshotScanLookup := snapshotwiring.NewSnapshotScanRefLookupAdapter(repos.scanRepo)

	websiteSnapshotQueryStore := snapshotwiring.NewSnapshotWebsiteQueryStoreAdapter(repos.websiteSnapshotRepo)
	subdomainSnapshotQueryStore := snapshotwiring.NewSnapshotSubdomainQueryStoreAdapter(repos.subdomainSnapshotRepo)
	endpointSnapshotQueryStore := snapshotwiring.NewSnapshotEndpointQueryStoreAdapter(repos.endpointSnapshotRepo)
	directorySnapshotQueryStore := snapshotwiring.NewSnapshotDirectoryQueryStoreAdapter(repos.directorySnapshotRepo)
	hostPortSnapshotQueryStore := snapshotwiring.NewSnapshotHostPortQueryStoreAdapter(repos.hostPortSnapshotRepo)
	screenshotSnapshotQueryStore := snapshotwiring.NewSnapshotScreenshotQueryStoreAdapter(repos.screenshotSnapshotRepo)
	vulnerabilitySnapshotQueryStore := snapshotwiring.NewSnapshotVulnerabilityQueryStoreAdapter(repos.vulnerabilitySnapshotRepo)

	websiteSnapshotCommandStore := snapshotwiring.NewSnapshotWebsiteCommandStoreAdapter(repos.websiteSnapshotRepo)
	subdomainSnapshotCommandStore := snapshotwiring.NewSnapshotSubdomainCommandStoreAdapter(repos.subdomainSnapshotRepo)
	endpointSnapshotCommandStore := snapshotwiring.NewSnapshotEndpointCommandStoreAdapter(repos.endpointSnapshotRepo)
	directorySnapshotCommandStore := snapshotwiring.NewSnapshotDirectoryCommandStoreAdapter(repos.directorySnapshotRepo)
	hostPortSnapshotCommandStore := snapshotwiring.NewSnapshotHostPortCommandStoreAdapter(repos.hostPortSnapshotRepo)
	screenshotSnapshotCommandStore := snapshotwiring.NewSnapshotScreenshotCommandStoreAdapter(repos.screenshotSnapshotRepo)
	vulnerabilitySnapshotCommandStore := snapshotwiring.NewSnapshotVulnerabilityCommandStoreAdapter(repos.vulnerabilitySnapshotRepo)

	websiteAssetSync := snapshotwiring.NewSnapshotWebsiteAssetSyncAdapter(asset.websiteSvc)
	subdomainAssetSync := snapshotwiring.NewSnapshotSubdomainAssetSyncAdapter(asset.subdomainSvc)
	endpointAssetSync := snapshotwiring.NewSnapshotEndpointAssetSyncAdapter(asset.endpointSvc)
	directoryAssetSync := snapshotwiring.NewSnapshotDirectoryAssetSyncAdapter(asset.directorySvc)
	hostPortAssetSync := snapshotwiring.NewSnapshotHostPortAssetSyncAdapter(asset.hostPortSvc)
	screenshotAssetSync := snapshotwiring.NewSnapshotScreenshotAssetSyncAdapter(asset.screenshotSvc)
	vulnerabilityAssetSync := snapshotwiring.NewSnapshotVulnerabilityAssetSyncAdapter(security.vulnerabilitySvc)
	vulnerabilityRawOutputCodec := snapshotwiring.NewSnapshotVulnerabilityRawOutputCodec()
	vulnerabilityNotificationWriter := snapshotwiring.NewSnapshotVulnerabilityNotificationOccurrenceWriter(db, repos.notificationProducer)

	websiteSnapshotSvc := snapshotwiring.NewSnapshotWebsiteApplicationService(websiteSnapshotQueryStore, websiteSnapshotCommandStore, snapshotScanLookup, websiteAssetSync)
	subdomainSnapshotSvc := snapshotwiring.NewSnapshotSubdomainApplicationService(subdomainSnapshotQueryStore, subdomainSnapshotCommandStore, snapshotScanLookup, subdomainAssetSync)
	endpointSnapshotSvc := snapshotwiring.NewSnapshotEndpointApplicationService(endpointSnapshotQueryStore, endpointSnapshotCommandStore, snapshotScanLookup, endpointAssetSync)
	directorySnapshotSvc := snapshotwiring.NewSnapshotDirectoryApplicationService(directorySnapshotQueryStore, directorySnapshotCommandStore, snapshotScanLookup, directoryAssetSync, materializationCoordinator)
	hostPortSnapshotSvc := snapshotwiring.NewSnapshotHostPortApplicationService(hostPortSnapshotQueryStore, hostPortSnapshotCommandStore, snapshotScanLookup, hostPortAssetSync)
	screenshotSnapshotSvc := snapshotwiring.NewSnapshotScreenshotApplicationService(screenshotSnapshotQueryStore, screenshotSnapshotCommandStore, snapshotScanLookup, screenshotAssetSync, materializationCoordinator)
	vulnerabilitySnapshotSvc := snapshotwiring.NewSnapshotVulnerabilityApplicationService(vulnerabilitySnapshotQueryStore, vulnerabilitySnapshotCommandStore, snapshotScanLookup, vulnerabilityAssetSync, vulnerabilityRawOutputCodec, materializationCoordinator, vulnerabilityNotificationWriter)

	return snapshotModuleHandlers{
		websiteSnapshotHandler:       snapshothandler.NewWebsiteSnapshotHandler(websiteSnapshotSvc),
		subdomainSnapshotHandler:     snapshothandler.NewSubdomainSnapshotHandler(subdomainSnapshotSvc),
		endpointSnapshotHandler:      snapshothandler.NewEndpointSnapshotHandler(endpointSnapshotSvc),
		directorySnapshotHandler:     snapshothandler.NewDirectorySnapshotHandler(directorySnapshotSvc),
		hostPortSnapshotHandler:      snapshothandler.NewHostPortSnapshotHandler(hostPortSnapshotSvc),
		screenshotSnapshotHandler:    snapshothandler.NewScreenshotSnapshotHandler(screenshotSnapshotSvc),
		vulnerabilitySnapshotHandler: snapshothandler.NewVulnerabilitySnapshotHandler(vulnerabilitySnapshotSvc),
		subdomainSnapshotService:     subdomainSnapshotSvc,
		websiteSnapshotService:       websiteSnapshotSvc,
		endpointSnapshotService:      endpointSnapshotSvc,
		directorySnapshotService:     directorySnapshotSvc,
		hostPortSnapshotService:      hostPortSnapshotSvc,
		screenshotSnapshotService:    screenshotSnapshotSvc,
		vulnerabilitySnapshotService: vulnerabilitySnapshotSvc,
	}
}

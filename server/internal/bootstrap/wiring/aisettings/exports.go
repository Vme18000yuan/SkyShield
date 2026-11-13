package aisettingswiring

import (
	"context"
	"strings"

	"github.com/yyhuni/lunafox/server/internal/airuntime"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	settingsrepo "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	workbenchinfra "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/infrastructure"
	"gorm.io/gorm"
)

func NewAISettingsLLMProfileStoreAdapter(db *gorm.DB) settingsapp.ProfileStore {
	return settingsrepo.NewLLMProfileStore(db)
}
func NewAISettingsLLMUsageSink(db *gorm.DB) airuntime.LLMUsageSink {
	return settingsrepo.NewLLMUsageStore(db)
}
func NewAISettingsLLMUsageReader(db *gorm.DB) settingsapp.LLMUsageReader {
	return settingsrepo.NewLLMUsageStore(db)
}
func NewAISettingsLLMRecordingStore(db *gorm.DB) settingsapp.LLMRecordingStore {
	return settingsrepo.NewLLMUsageStore(db)
}
func NewAISettingsLLMRecordingReader(db *gorm.DB) settingsapp.LLMRecordingReader {
	return settingsrepo.NewLLMUsageStore(db)
}

func NewAISettingsAgentSettingStoreAdapter(db *gorm.DB) settingsapp.AgentSettingStore {
	return settingsrepo.NewAgentSettingStore(db)
}

func NewAISettingsAgentCatalogStoreAdapter(db *gorm.DB) settingsapp.AgentCatalogStore {
	return settingsrepo.NewAgentCatalogStore(db)
}

func NewAISettingsAgentControlStoreAdapter(db *gorm.DB) settingsapp.AgentControlStore {
	return settingsrepo.NewAgentCatalogStore(db)
}
func SeedAISettingsDefaultAgents(ctx context.Context, db *gorm.DB) error {
	return settingsrepo.NewAgentCatalogStore(db).SeedDefaultAgents(ctx)
}

func NewAISettingsToolSettingStoreAdapter(db *gorm.DB) settingsapp.ToolSettingStore {
	return settingsrepo.NewToolSettingStore(db)
}

func NewAISettingsToolCatalogStoreAdapter(db *gorm.DB) settingsapp.ToolCatalogStore {
	return settingsrepo.NewToolCatalogStore(db)
}
func NewAISettingsPlatformMutationStoreAdapter(db *gorm.DB) workbenchinfra.PlatformMutationGate {
	return settingsrepo.NewPlatformMutationStore(db)
}
func NewAISettingsRuntimeSettingStoreAdapter(db *gorm.DB) settingsapp.RuntimeSettingStore {
	return settingsrepo.NewToolCatalogStore(db)
}

func NewAISettingsSkillStoreAdapter(db *gorm.DB, builtinRoot ...string) settingsapp.SkillStore {
	return settingsrepo.NewSkillStore(db, builtinRoot...)
}

func NewAISettingsSkillPackageStoreAdapter(db *gorm.DB, builtinRoot ...string) settingsapp.SkillPackageStore {
	return settingsrepo.NewSkillStore(db, builtinRoot...)
}
func NewAISettingsSkillRuntimeFileStoreAdapter(db *gorm.DB) settingsapp.SkillRuntimeFileStore {
	return settingsrepo.NewSkillStore(db)
}

func SeedAISettingsBuiltinSkills(ctx context.Context, db *gorm.DB, builtinRoot string) error {
	return settingsrepo.NewSkillStore(db, strings.TrimSpace(builtinRoot)).SeedBuiltinSkills(ctx)
}

func NewAISettingsMCPStoreAdapter(db *gorm.DB) settingsapp.MCPStore {
	return settingsrepo.NewMCPStore(db)
}

func SeedAISettingsDefaultMCP(ctx context.Context, db *gorm.DB) error {
	return settingsrepo.NewMCPStore(db).SeedMCPDefaults(ctx)
}

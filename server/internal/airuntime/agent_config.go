package airuntime

import (
	"context"
	"github.com/Autumn-27/norma/tool"
)

// AgentConfig is resolved by the host for each new run. A zero budget follows
// the task's own limit (or ARTEX's default where no task is present).
type AgentConfig struct {
	Key                   string
	Enabled               bool
	MaxTurns              int
	RunSeconds            int
	AdditionalPrompt      string
	SearchAssetsEnabled   bool
	RegisterAssetsEnabled bool
}
type AgentConfigResolver func(context.Context, int, string) (AgentConfig, error)
type assetSearchGrantKey struct{}

func withAssetSearchGrant(ctx context.Context, allowed bool) context.Context {
	return context.WithValue(ctx, assetSearchGrantKey{}, allowed)
}
func AssetSearchAllowed(ctx context.Context) bool {
	allowed, ok := ctx.Value(assetSearchGrantKey{}).(bool)
	return !ok || allowed
}

func DefaultAgentConfig(key string) AgentConfig {
	// ARTEX seeds mainagent, planner, and worker with an unlimited turn budget
	// and a 1200-second run limit. Keep the host roles mapped to those defaults;
	// a persisted AI agent setting still overrides this fallback.
	return AgentConfig{
		Key:                   key,
		Enabled:               true,
		RunSeconds:            1200,
		SearchAssetsEnabled:   true,
		RegisterAssetsEnabled: true,
	}
}

// FilterAgentTools applies the administrator's per-Agent platform tool grants.
// Core graph tools are governed separately and are not introduced by this list.
func FilterAgentTools(config AgentConfig, tools []tool.CoreTool) []tool.CoreTool {
	allowed := make([]tool.CoreTool, 0, len(tools))
	for _, item := range tools {
		if (item.Name() == "search_platform_assets" || item.Name() == "list_assets" || item.Name() == "list_untested_assets") && !config.SearchAssetsEnabled {
			continue
		}
		if item.Name() == "insert_assets" && !config.RegisterAssetsEnabled {
			continue
		}
		allowed = append(allowed, item)
	}
	return allowed
}

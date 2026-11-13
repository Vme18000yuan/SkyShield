package application

import (
	"context"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
)

type AgentSettingStore interface {
	ListAgentSettings(context.Context, int) ([]airuntime.AgentConfig, error)
	SaveAgentSetting(context.Context, int, airuntime.AgentConfig) error
	ResolveAgentSetting(context.Context, int, string) (airuntime.AgentConfig, error)
}

package application

import (
	"context"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"strings"
)

type AgentSettingFacade struct{ store AgentSettingStore }

func NewAgentSettingFacade(store AgentSettingStore) *AgentSettingFacade {
	return &AgentSettingFacade{store: store}
}
func validAgentKey(key string) bool { return key == "assistant" || key == "planner" || key == "worker" }
func (f *AgentSettingFacade) List(ctx context.Context, user int) ([]airuntime.AgentConfig, error) {
	if user <= 0 {
		return nil, ErrForbidden
	}
	return f.store.ListAgentSettings(ctx, user)
}
func (f *AgentSettingFacade) Save(ctx context.Context, user int, config airuntime.AgentConfig) error {
	config.AdditionalPrompt = strings.TrimSpace(config.AdditionalPrompt)
	if user <= 0 {
		return ErrForbidden
	}
	if !validAgentKey(config.Key) || config.MaxTurns < 0 || config.MaxTurns > 1000 || config.RunSeconds < 0 || config.RunSeconds > 86400 || len(config.AdditionalPrompt) > 32768 {
		return ErrInvalidProfile
	}
	return f.store.SaveAgentSetting(ctx, user, config)
}
func (f *AgentSettingFacade) Resolve(ctx context.Context, user int, key string) (airuntime.AgentConfig, error) {
	if user <= 0 {
		return airuntime.AgentConfig{}, ErrForbidden
	}
	if !validAgentKey(key) {
		return airuntime.AgentConfig{}, ErrInvalidProfile
	}
	return f.store.ResolveAgentSetting(ctx, user, key)
}

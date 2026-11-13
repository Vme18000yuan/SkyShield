package application

import (
	"context"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
)

func (f *MCPFacade) Details(ctx context.Context, user int, id string) (airuntime.MCPConfig, error) {
	item, err := f.store.GetMCPForAdmin(ctx, user, id)
	if err != nil {
		return airuntime.MCPConfig{}, err
	}
	config, err := f.decode(*item)
	if config.Transport != "stdio" {
		config.Env = config.Headers
	}
	return config, err
}
func (f *MCPFacade) CachedTools(ctx context.Context, user int, id string) ([]MCPTool, error) {
	item, err := f.store.GetMCPForAdmin(ctx, user, id)
	if err != nil {
		return nil, err
	}
	if item.Tools == nil {
		return []MCPTool{}, nil
	}
	return item.Tools, nil
}
func (f *MCPFacade) Visibility(ctx context.Context, user int, id string) ([]string, error) {
	control, ok := f.store.(MCPControlStore)
	if !ok {
		return nil, ErrInvalidProfile
	}
	return control.MCPVisibility(ctx, user, id)
}
func (f *MCPFacade) SetVisible(ctx context.Context, user int, id, key string, on bool) error {
	if !agentCatalogKeyPattern.MatchString(key) {
		return ErrInvalidProfile
	}
	control, ok := f.store.(MCPControlStore)
	if !ok {
		return ErrInvalidProfile
	}
	return control.SetMCPVisible(ctx, user, id, key, on)
}

func (f *MCPFacade) SetEnabled(ctx context.Context, user int, id string, on bool) error {
	item, err := f.store.GetMCPForAdmin(ctx, user, id)
	if err != nil {
		return err
	}
	if on && item.Transport != "stdio" && item.Endpoint == "" {
		return ErrInvalidProfile
	}
	item.Enabled = on
	item.EncryptedHeaders = nil
	item.EncryptedEnv = nil
	item.ReplaceEnv = false
	item.PreserveAgents = true
	_, err = f.store.SaveMCP(ctx, user, *item, false, false)
	return err
}

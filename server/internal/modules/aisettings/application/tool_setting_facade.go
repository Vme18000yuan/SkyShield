package application

import "context"

type ToolSettingFacade struct{ store ToolSettingStore }

func NewToolSettingFacade(store ToolSettingStore) *ToolSettingFacade {
	return &ToolSettingFacade{store: store}
}
func (f *ToolSettingFacade) List(ctx context.Context, user int) ([]ToolVisibility, error) {
	if user <= 0 {
		return nil, ErrForbidden
	}
	return f.store.ListToolSettings(ctx, user)
}
func (f *ToolSettingFacade) Save(ctx context.Context, user int, item ToolVisibility) error {
	if user <= 0 {
		return ErrForbidden
	}
	if !validAgentKey(item.AgentKey) || (item.ToolKey != "search_platform_assets" && item.ToolKey != "insert_assets") {
		return ErrInvalidProfile
	}
	return f.store.SaveToolSetting(ctx, user, item)
}

package application

import "context"

type ToolVisibility struct {
	AgentKey string
	ToolKey  string
	Enabled  bool
}
type ToolSettingStore interface {
	ListToolSettings(context.Context, int) ([]ToolVisibility, error)
	SaveToolSetting(context.Context, int, ToolVisibility) error
}

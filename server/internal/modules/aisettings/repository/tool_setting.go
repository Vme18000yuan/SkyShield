package repository

import (
	"context"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

type ToolSettingStore struct{ db *gorm.DB }

func NewToolSettingStore(db *gorm.DB) *ToolSettingStore { return &ToolSettingStore{db: db} }
func (s *ToolSettingStore) ListToolSettings(ctx context.Context, user int) ([]settingsapp.ToolVisibility, error) {
	out := []settingsapp.ToolVisibility{}
	for _, agent := range []string{"assistant", "planner", "worker"} {
		for _, tool := range []string{"search_platform_assets", "insert_assets"} {
			out = append(out, settingsapp.ToolVisibility{AgentKey: agent, ToolKey: tool, Enabled: true})
		}
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		var rows []settingsapp.ToolVisibility
		if err := tx.Raw(`SELECT agent_key,tool_key,enabled FROM ai_agent_tool_setting WHERE tenant_id=?`, assetapp.DefaultTenantID).Scan(&rows).Error; err != nil {
			return err
		}
		for _, item := range rows {
			for i := range out {
				if out[i].AgentKey == item.AgentKey && out[i].ToolKey == item.ToolKey {
					out[i] = item
					break
				}
			}
		}
		return nil
	})
	return out, err
}
func (s *ToolSettingStore) SaveToolSetting(ctx context.Context, user int, item settingsapp.ToolVisibility) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		return tx.Exec(`INSERT INTO ai_agent_tool_setting(tenant_id,agent_key,tool_key,enabled,updated_by) VALUES(?,?,?,?,?)
   ON CONFLICT(tenant_id,agent_key,tool_key) DO UPDATE SET enabled=EXCLUDED.enabled,updated_by=EXCLUDED.updated_by,updated_at=clock_timestamp()`, assetapp.DefaultTenantID, item.AgentKey, item.ToolKey, item.Enabled, user).Error
	})
}

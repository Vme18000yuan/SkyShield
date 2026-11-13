package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/yyhuni/lunafox/server/internal/airuntime"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

type AgentSettingStore struct{ db *gorm.DB }

func NewAgentSettingStore(db *gorm.DB) *AgentSettingStore { return &AgentSettingStore{db: db} }
func readAgentSettings(tx *gorm.DB) ([]airuntime.AgentConfig, error) {
	configs := []airuntime.AgentConfig{airuntime.DefaultAgentConfig("assistant"), airuntime.DefaultAgentConfig("planner"), airuntime.DefaultAgentConfig("worker")}
	rows, err := tx.Raw(`SELECT agent_key,enabled,max_turns,run_seconds,additional_prompt FROM ai_agent_setting WHERE tenant_id=?`, assetapp.DefaultTenantID).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		c := airuntime.DefaultAgentConfig("")
		if err := rows.Scan(&c.Key, &c.Enabled, &c.MaxTurns, &c.RunSeconds, &c.AdditionalPrompt); err != nil {
			return nil, err
		}
		for i := range configs {
			if configs[i].Key == c.Key {
				configs[i] = c
				break
			}
		}
	}
	return configs, rows.Err()
}
func (s *AgentSettingStore) ListAgentSettings(ctx context.Context, user int) ([]airuntime.AgentConfig, error) {
	var out []airuntime.AgentConfig
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		var err error
		out, err = readAgentSettings(tx)
		return err
	})
	return out, err
}
func (s *AgentSettingStore) SaveAgentSetting(ctx context.Context, user int, c airuntime.AgentConfig) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		return tx.Exec(`INSERT INTO ai_agent_setting(tenant_id,agent_key,enabled,max_turns,run_seconds,additional_prompt,updated_by)
   VALUES(?,?,?,?,?,?,?) ON CONFLICT(tenant_id,agent_key) DO UPDATE SET enabled=EXCLUDED.enabled,max_turns=EXCLUDED.max_turns,
   run_seconds=EXCLUDED.run_seconds,additional_prompt=EXCLUDED.additional_prompt,updated_by=EXCLUDED.updated_by,updated_at=clock_timestamp()`, assetapp.DefaultTenantID, c.Key, c.Enabled, c.MaxTurns, c.RunSeconds, c.AdditionalPrompt, user).Error
	})
}
func (s *AgentSettingStore) ResolveAgentSetting(ctx context.Context, user int, key string) (airuntime.AgentConfig, error) {
	c := airuntime.DefaultAgentConfig(key)
	var allowed bool
	err := s.db.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
  WHERE m.tenant_id=? AND m.user_id=? AND t.status='active' AND u.is_active=TRUE)`, assetapp.DefaultTenantID, user).Scan(&allowed).Error
	if err != nil {
		return c, err
	}
	if !allowed {
		return c, settingsapp.ErrForbidden
	}
	err = s.db.WithContext(ctx).Raw(`SELECT agent_key,enabled,max_turns,run_seconds,additional_prompt FROM ai_agent_setting WHERE tenant_id=? AND agent_key=?`, assetapp.DefaultTenantID, key).Row().Scan(&c.Key, &c.Enabled, &c.MaxTurns, &c.RunSeconds, &c.AdditionalPrompt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return c, err
	}
	rows, err := s.db.WithContext(ctx).Raw(`SELECT tool_key,enabled FROM ai_agent_tool_setting WHERE tenant_id=? AND agent_key=?`, assetapp.DefaultTenantID, key).Rows()
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var toolKey string
		var enabled bool
		if err := rows.Scan(&toolKey, &enabled); err != nil {
			return c, err
		}
		switch toolKey {
		case "search_platform_assets":
			c.SearchAssetsEnabled = enabled
		case "insert_assets":
			c.RegisterAssetsEnabled = enabled
		}
	}
	return c, rows.Err()
}

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

func (s *AgentCatalogStore) RequireControlAdmin(ctx context.Context, user int) error {
	return requireAdmin(s.db.WithContext(ctx), user)
}
func (s *AgentCatalogStore) ReadSavedAgentPrompt(ctx context.Context, user int, key string) (string, bool, error) {
	if err := s.RequireControlAdmin(ctx, user); err != nil {
		return "", false, err
	}
	var prompt string
	err := s.db.WithContext(ctx).Raw(`SELECT prompt_template FROM ai_builtin_agent_config WHERE tenant_id=? AND agent_key=?`, assets.DefaultTenantID, key).Row().Scan(&prompt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return prompt, err == nil, err
}
func requireAgentKey(tx *gorm.DB, key string) error {
	if _, ok := airuntime.BuiltinChatAgent(key); ok {
		return nil
	}
	var found string
	err := tx.Raw(`SELECT agent_key FROM ai_custom_agent WHERE tenant_id=? AND agent_key=? FOR SHARE`, assets.DefaultTenantID, key).Row().Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return settings.ErrAgentCatalogNotFound
	}
	return err
}
func (s *AgentCatalogStore) controlTransaction(ctx context.Context, user int, key string, write func(*gorm.DB) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		if err := lockAgentSettingsTenant(tx); err != nil {
			return err
		}
		if err := requireAgentKey(tx, key); err != nil {
			return err
		}
		return write(tx)
	})
}
func ensureBuiltinConfig(tx *gorm.DB, user int, item airuntime.ChatAgentDefinition) error {
	if !item.Builtin {
		return nil
	}
	return tx.Exec(`INSERT INTO ai_builtin_agent_config(tenant_id,agent_key,enabled,max_turns,run_seconds,prompt_template,updated_by) VALUES(?,?,?,?,?,?,?) ON CONFLICT(tenant_id,agent_key) DO NOTHING`, assets.DefaultTenantID, item.Key, item.Enabled, item.MaxTurns, item.RunSeconds, item.PromptTemplate, user).Error
}
func (s *AgentCatalogStore) PatchAgentConfig(ctx context.Context, user int, item airuntime.ChatAgentDefinition, input settings.AgentConfigPatch) error {
	return s.controlTransaction(ctx, user, item.Key, func(tx *gorm.DB) error {
		if err := ensureBuiltinConfig(tx, user, item); err != nil {
			return err
		}
		changes := map[string]any{}
		if input.Enabled != nil {
			changes["enabled"] = *input.Enabled
		}
		if input.MaxTurns != nil {
			changes["max_turns"] = *input.MaxTurns
		}
		if input.RunSeconds != nil {
			changes["run_seconds"] = *input.RunSeconds
		}
		if len(changes) > 0 {
			table := "ai_builtin_agent_config"
			if !item.Builtin {
				table = "ai_custom_agent"
				changes["updated_by"] = user
			}
			changes["updated_by"] = user
			changes["updated_at"] = gorm.Expr("CURRENT_TIMESTAMP")
			if err := tx.Table(table).Where("tenant_id=? AND agent_key=?", assets.DefaultTenantID, item.Key).Updates(changes).Error; err != nil {
				return err
			}
		}
		if input.LLMProfileID != nil {
			var id *string
			if err := json.Unmarshal(input.LLMProfileID, &id); err != nil {
				return err
			}
			item.UpdateLLMBinding = true
			item.LLMProfileID = id
			if err := updateAgentLLMBinding(tx, user, item); err != nil {
				return err
			}
		}
		options := map[string]any{}
		if input.WebSearch != nil {
			options["web_search"] = *input.WebSearch
		}
		if input.InteractiveShell != nil {
			options["interactive_shell"] = *input.InteractiveShell
		}
		if input.TriggerRunMode != nil {
			options["trigger_run_mode"] = *input.TriggerRunMode
		}
		if input.TriggerMergeMode != nil {
			options["trigger_merge_mode"] = *input.TriggerMergeMode
		}
		if input.TriggerMaxParallel != nil {
			options["trigger_max_parallel"] = *input.TriggerMaxParallel
		}
		if len(options) > 0 {
			data, err := json.Marshal(options)
			if err != nil {
				return err
			}
			if err := tx.Exec(`INSERT INTO ai_agent_option(tenant_id,agent_key,options) VALUES(?,?,?::jsonb) ON CONFLICT(tenant_id,agent_key) DO UPDATE SET options=ai_agent_option.options||EXCLUDED.options,updated_at=CURRENT_TIMESTAMP`, assets.DefaultTenantID, item.Key, string(data)).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *AgentCatalogStore) WriteAgentPrompt(ctx context.Context, user int, item airuntime.ChatAgentDefinition, prompt string) (int, error) {
	var version int
	err := s.controlTransaction(ctx, user, item.Key, func(tx *gorm.DB) error {
		if err := ensureBuiltinConfig(tx, user, item); err != nil {
			return err
		}
		if err := ensureAgentPromptInitialVersion(tx, user, item.Key, item.SystemPrompt); err != nil {
			return err
		}
		if err := appendAgentPromptVersion(tx, user, item.Key, prompt); err != nil {
			return err
		}
		if item.Builtin {
			if err := tx.Exec(`UPDATE ai_builtin_agent_config SET prompt_template=?,updated_by=?,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=? AND agent_key=?`, prompt, user, assets.DefaultTenantID, item.Key).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Exec(`UPDATE ai_custom_agent SET system_prompt=?,updated_by=?,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=? AND agent_key=?`, prompt, user, assets.DefaultTenantID, item.Key).Error; err != nil {
				return err
			}
		}
		return tx.Raw(`SELECT MAX(version) FROM ai_agent_prompt_version WHERE tenant_id=? AND agent_key=?`, assets.DefaultTenantID, item.Key).Row().Scan(&version)
	})
	return version, err
}
func (s *AgentCatalogStore) WriteAgentWrapup(ctx context.Context, user int, key string, taskTimeout bool, prompt string, turns int) error {
	return s.controlTransaction(ctx, user, key, func(tx *gorm.DB) error {
		if err := tx.Exec(`INSERT INTO ai_agent_wrapup_config(tenant_id,agent_key,updated_by) VALUES(?,?,?) ON CONFLICT(tenant_id,agent_key) DO NOTHING`, assets.DefaultTenantID, key, user).Error; err != nil {
			return err
		}
		values := map[string]any{"updated_by": user, "updated_at": gorm.Expr("CURRENT_TIMESTAMP")}
		if taskTimeout {
			values["task_timeout_prompt"] = prompt
			values["task_timeout_max_turns"] = turns
		} else {
			values["wrapup_prompt"] = prompt
			values["wrapup_max_turns"] = turns
		}
		return tx.Table("ai_agent_wrapup_config").Where("tenant_id=? AND agent_key=?", assets.DefaultTenantID, key).Updates(values).Error
	})
}
func (s *AgentCatalogStore) ReadAgentVisibility(ctx context.Context, user int, key string) (settings.AgentVisibility, error) {
	out := settings.AgentVisibility{MCP: []string{}, Skill: []string{}}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		if err := requireAgentKey(tx, key); err != nil {
			return err
		}
		if err := tx.Raw(`SELECT server_id::text FROM ai_mcp_server_agent WHERE tenant_id=? AND agent_key=? ORDER BY server_id`, assets.DefaultTenantID, key).Scan(&out.MCP).Error; err != nil {
			return err
		}
		return tx.Raw(`SELECT s.name FROM ai_skill_agent a JOIN ai_skill s ON s.tenant_id=a.tenant_id AND s.id=a.skill_id WHERE a.tenant_id=? AND a.agent_key=? ORDER BY s.name`, assets.DefaultTenantID, key).Scan(&out.Skill).Error
	})
	return out, err
}
func (s *AgentCatalogStore) WriteAgentVisibility(ctx context.Context, user int, key string, input settings.AgentVisibility) error {
	return s.controlTransaction(ctx, user, key, func(tx *gorm.DB) error {
		for _, id := range input.MCP {
			var exists bool
			if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_mcp_server WHERE tenant_id=? AND id=?)`, assets.DefaultTenantID, id).Scan(&exists).Error; err != nil {
				return err
			}
			if !exists {
				return settings.ErrProfileNotFound
			}
		}
		ids := []string{}
		for _, name := range input.Skill {
			var found []string
			if err := tx.Raw(`SELECT id::text FROM ai_skill WHERE tenant_id=? AND (name=? OR builtin_key=?) ORDER BY id LIMIT 2`, assets.DefaultTenantID, name, name).Scan(&found).Error; err != nil {
				return err
			}
			if len(found) != 1 {
				return settings.ErrProfileNotFound
			}
			ids = append(ids, found[0])
		}
		if err := tx.Exec(`DELETE FROM ai_mcp_server_agent WHERE tenant_id=? AND agent_key=?`, assets.DefaultTenantID, key).Error; err != nil {
			return err
		}
		for _, id := range input.MCP {
			if err := tx.Exec(`INSERT INTO ai_mcp_server_agent(tenant_id,server_id,agent_key) VALUES(?,?,?)`, assets.DefaultTenantID, id, key).Error; err != nil {
				return err
			}
		}
		if err := tx.Exec(`DELETE FROM ai_skill_agent WHERE tenant_id=? AND agent_key=?`, assets.DefaultTenantID, key).Error; err != nil {
			return err
		}
		for _, id := range ids {
			if err := tx.Exec(`INSERT INTO ai_skill_agent(tenant_id,skill_id,agent_key) VALUES(?,?,?)`, assets.DefaultTenantID, id, key).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *AgentCatalogStore) ListAgentTriggers(ctx context.Context, user int, key string) ([]artexdb.AgentTrigger, error) {
	out := []artexdb.AgentTrigger{}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		var rows []struct {
			ID       int64
			Config   string
			LastFire sql.NullTime
		}
		if err := tx.Raw(`SELECT id,config::text,last_fire FROM ai_agent_trigger WHERE tenant_id=? AND agent_key=? ORDER BY id`, assets.DefaultTenantID, key).Scan(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			var value artexdb.AgentTrigger
			if err := json.Unmarshal([]byte(row.Config), &value); err != nil {
				return err
			}
			value.ID = row.ID
			value.AgentKey = key
			if row.LastFire.Valid {
				at := row.LastFire.Time
				value.LastFire = &at
			}
			out = append(out, value)
		}
		return nil
	})
	return out, err
}
func (s *AgentCatalogStore) GetAgentTrigger(ctx context.Context, user int, id int64) (*artexdb.AgentTrigger, error) {
	var out artexdb.AgentTrigger
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		var key, config string
		var last sql.NullTime
		err := tx.Raw(`SELECT agent_key,config::text,last_fire FROM ai_agent_trigger WHERE tenant_id=? AND id=?`, assets.DefaultTenantID, id).Row().Scan(&key, &config, &last)
		if errors.Is(err, sql.ErrNoRows) {
			return settings.ErrAgentCatalogNotFound
		}
		if err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(config), &out); err != nil {
			return err
		}
		out.ID = id
		out.AgentKey = key
		if last.Valid {
			out.LastFire = &last.Time
		}
		return nil
	})
	return &out, err
}
func (s *AgentCatalogStore) SaveAgentTrigger(ctx context.Context, user int, key string, input artexdb.AgentTrigger, create bool) (*artexdb.AgentTrigger, error) {
	input.AgentKey = key
	data, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	err = s.controlTransaction(ctx, user, key, func(tx *gorm.DB) error {
		if create {
			return tx.Raw(`INSERT INTO ai_agent_trigger(tenant_id,agent_key,owner_user_id,enabled,config) VALUES(?,?,?,?,?::jsonb) RETURNING id`, assets.DefaultTenantID, key, user, input.Enabled, string(data)).Row().Scan(&input.ID)
		}
		result := tx.Exec(`UPDATE ai_agent_trigger SET enabled=?,config=?::jsonb,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=? AND id=? AND agent_key=?`, input.Enabled, string(data), assets.DefaultTenantID, input.ID, key)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return settings.ErrAgentCatalogNotFound
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetAgentTrigger(ctx, user, input.ID)
}
func (s *AgentCatalogStore) DeleteAgentTrigger(ctx context.Context, user int, id int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		if err := lockAgentSettingsTenant(tx); err != nil {
			return err
		}
		result := tx.Exec(`DELETE FROM ai_agent_trigger WHERE tenant_id=? AND id=?`, assets.DefaultTenantID, id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return settings.ErrAgentCatalogNotFound
		}
		return nil
	})
}

var _ settings.AgentControlStore = (*AgentCatalogStore)(nil)

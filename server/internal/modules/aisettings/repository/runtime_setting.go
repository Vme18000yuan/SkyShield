package repository

import (
	"context"
	"encoding/json"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

func (s *ToolCatalogStore) ReadRuntimeOptions(ctx context.Context, user int, admin bool) (map[string]any, map[string][]byte, error) {
	options := map[string]any{}
	secrets := map[string][]byte{}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if admin {
			if err := requireAdmin(tx, user); err != nil {
				return err
			}
		} else if err := requireAgentCatalogMember(ctx, tx, user); err != nil {
			return err
		}
		var raw string
		if err := tx.Raw(`SELECT COALESCE((SELECT options::text FROM ai_runtime_setting WHERE tenant_id=?),'{}')`, assets.DefaultTenantID).Row().Scan(&raw); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(raw), &options); err != nil {
			return err
		}
		rows, err := tx.Raw(`SELECT key,encrypted_value FROM ai_runtime_secret WHERE tenant_id=?`, assets.DefaultTenantID).Rows()
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var key string
			var cipher []byte
			if err = rows.Scan(&key, &cipher); err != nil {
				return err
			}
			secrets[key] = cipher
		}
		return rows.Err()
	})
	return options, secrets, err
}
func (s *ToolCatalogStore) PatchRuntimeOptions(ctx context.Context, user int, patch map[string]any, secrets map[string][]byte) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		if err := lockAgentSettingsTenant(tx); err != nil {
			return err
		}
		_, recordChanged := patch["task_intelligence_enabled"]
		_, agentChanged := patch["credential_agent_enabled"]
		_, bindingChanged := patch["credential_traffic_binding"]
		if recordChanged || agentChanged || bindingChanged {
			var currentRaw string
			if err := tx.Raw(`SELECT COALESCE((SELECT options::text FROM ai_runtime_setting WHERE tenant_id=?),'{}')`, assets.DefaultTenantID).Row().Scan(&currentRaw); err != nil {
				return err
			}
			current := map[string]any{}
			if err := json.Unmarshal([]byte(currentRaw), &current); err != nil {
				return err
			}
			value := func(key string) bool {
				if v, ok := patch[key].(bool); ok {
					return v
				}
				v, _ := current[key].(bool)
				return v
			}
			record, agent, binding := value("task_intelligence_enabled"), value("credential_agent_enabled"), value("credential_traffic_binding")
			if !record {
				if agentChanged && agent || bindingChanged && binding {
					return settings.ErrInvalidProfile
				}
				agent, binding = false, false
			}
			if !agent {
				if bindingChanged && binding {
					return settings.ErrInvalidProfile
				}
				binding = false
			}
			patch["task_intelligence_enabled"], patch["credential_agent_enabled"], patch["credential_traffic_binding"] = record, agent, binding
		}
		raw, err := json.Marshal(patch)
		if err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO ai_runtime_setting(tenant_id,options) VALUES(?,?::jsonb) ON CONFLICT(tenant_id) DO UPDATE SET options=ai_runtime_setting.options||EXCLUDED.options,updated_at=CURRENT_TIMESTAMP`, assets.DefaultTenantID, string(raw)).Error; err != nil {
			return err
		}
		for key, cipher := range secrets {
			if len(cipher) == 0 {
				if err := tx.Exec(`DELETE FROM ai_runtime_secret WHERE tenant_id=? AND key=?`, assets.DefaultTenantID, key).Error; err != nil {
					return err
				}
			} else if err := tx.Exec(`INSERT INTO ai_runtime_secret(tenant_id,key,encrypted_value) VALUES(?,?,?) ON CONFLICT(tenant_id,key) DO UPDATE SET encrypted_value=EXCLUDED.encrypted_value,updated_at=CURRENT_TIMESTAMP`, assets.DefaultTenantID, key, cipher).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

var _ settings.RuntimeSettingStore = (*ToolCatalogStore)(nil)

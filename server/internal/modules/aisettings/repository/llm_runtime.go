package repository

import (
	"context"
	"encoding/json"
	"github.com/Autumn-27/artex/llmpool"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

func (s *LLMProfileStore) RuntimeSettings(ctx context.Context, user int, admin bool) (settingsapp.LLMRuntimeSettings, error) {
	var out settingsapp.LLMRuntimeSettings
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if admin {
			if err := requireAdmin(tx, user); err != nil {
				return err
			}
		} else if err := requireAgentCatalogMember(ctx, tx, user); err != nil {
			return err
		}
		var raw string
		if err := tx.Raw(`SELECT COALESCE((SELECT options::text FROM ai_runtime_setting WHERE tenant_id=?),'{}')`, assetapp.DefaultTenantID).Row().Scan(&raw); err != nil {
			return err
		}
		return json.Unmarshal([]byte(raw), &out)
	})
	return out, err
}

func (s *LLMProfileStore) SaveRuntimeSettings(ctx context.Context, user int, input settingsapp.LLMRuntimeSettings) error {
	input.Retry = input.Retry.Clamped()
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		return tx.Exec(`INSERT INTO ai_runtime_setting(tenant_id,options) VALUES(?,?::jsonb) ON CONFLICT(tenant_id) DO UPDATE SET options=ai_runtime_setting.options||EXCLUDED.options,updated_at=CURRENT_TIMESTAMP`, assetapp.DefaultTenantID, string(data)).Error
	})
}

func (s *LLMProfileStore) PatchRuntimeSettings(ctx context.Context, user int, input settingsapp.LLMRuntimePatch) (settingsapp.LLMRuntimeSettings, error) {
	data, err := json.Marshal(input)
	if err != nil {
		return settingsapp.LLMRuntimeSettings{}, err
	}
	var out settingsapp.LLMRuntimeSettings
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		var raw string
		if err := tx.Raw(`INSERT INTO ai_runtime_setting(tenant_id,options) VALUES(?,?::jsonb) ON CONFLICT(tenant_id) DO UPDATE SET options=ai_runtime_setting.options||EXCLUDED.options,updated_at=CURRENT_TIMESTAMP RETURNING options::text`, assetapp.DefaultTenantID, string(data)).Row().Scan(&raw); err != nil {
			return err
		}
		return json.Unmarshal([]byte(raw), &out)
	})
	return out, err
}

func (s *LLMProfileStore) ReadLLMHealth(ctx context.Context) (map[int64]llmpool.State, error) {
	var rows []struct {
		RuntimeID int64
		State     string
	}
	if err := s.db.WithContext(ctx).Raw(`SELECT runtime_id,state::text FROM ai_llm_health WHERE tenant_id=?`, assetapp.DefaultTenantID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]llmpool.State, len(rows))
	for _, row := range rows {
		var state llmpool.State
		if err := json.Unmarshal([]byte(row.State), &state); err != nil {
			return nil, err
		}
		out[row.RuntimeID] = state
	}
	return out, nil
}
func (s *LLMProfileStore) SaveLLMHealth(ctx context.Context, id int64, state llmpool.State) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Exec(`INSERT INTO ai_llm_health(tenant_id,runtime_id,state) SELECT tenant_id,runtime_id,?::jsonb FROM ai_llm_profile WHERE runtime_id=? AND tenant_id=? ON CONFLICT(runtime_id) DO UPDATE SET state=EXCLUDED.state,updated_at=CURRENT_TIMESTAMP`, string(data), id, assetapp.DefaultTenantID).Error
}
func (s *LLMProfileStore) ResetLLMHealth(ctx context.Context, id int64) error {
	return s.db.WithContext(ctx).Exec(`DELETE FROM ai_llm_health WHERE tenant_id=? AND (?=0 OR runtime_id=?)`, assetapp.DefaultTenantID, id, id).Error
}

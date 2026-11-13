package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Autumn-27/norma/llm"
	app "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

func (s *Store) ReadJudge(ctx context.Context, user int, admin bool) (app.JudgeConfig, error) {
	cfg := app.DefaultJudgeConfig()
	if err := member(s.db.WithContext(ctx), user, admin, false); err != nil {
		return cfg, err
	}
	var raw []byte
	err := s.db.WithContext(ctx).Raw(`SELECT judge::text FROM ai_intercept_settings WHERE tenant_id=?`, assets.DefaultTenantID).Row().Scan(&raw)
	if err != nil {
		return cfg, err
	}
	if err = json.Unmarshal(raw, &cfg); err != nil {
		return cfg, err
	}
	if cfg.Prompt == "" {
		cfg.Prompt = app.DefaultJudgeConfig().Prompt
	}
	return cfg, nil
}
func (s *Store) SaveJudge(ctx context.Context, user int, cfg app.JudgeConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := member(tx, user, true, true); err != nil {
			return err
		}
		if cfg.ProfileID != nil {
			var found string
			err := tx.Raw(`SELECT id::text FROM ai_llm_profile WHERE tenant_id=? AND id=? FOR SHARE`, assets.DefaultTenantID, *cfg.ProfileID).Row().Scan(&found)
			if errors.Is(err, sql.ErrNoRows) {
				return app.ErrInvalidInput
			}
			if err != nil {
				return err
			}
		}
		result := tx.Exec(`UPDATE ai_intercept_settings SET judge=?::jsonb,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=?`, string(raw), assets.DefaultTenantID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return app.ErrNotFound
		}
		return nil
	})
}
func (s *Store) AddJudgeUsage(ctx context.Context, user int, profile *string, usage llm.Usage) error {
	if err := member(s.db.WithContext(ctx), user, false, false); err != nil {
		return err
	}
	key := ""
	if profile != nil {
		key = *profile
	}
	return s.db.WithContext(ctx).Exec(`INSERT INTO ai_judge_usage(tenant_id,day,profile_key,calls,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens) VALUES(?,(clock_timestamp() AT TIME ZONE 'UTC')::date,?,1,?,?,?,?) ON CONFLICT(tenant_id,day,profile_key) DO UPDATE SET calls=ai_judge_usage.calls+1,input_tokens=ai_judge_usage.input_tokens+EXCLUDED.input_tokens,output_tokens=ai_judge_usage.output_tokens+EXCLUDED.output_tokens,cache_read_tokens=ai_judge_usage.cache_read_tokens+EXCLUDED.cache_read_tokens,cache_write_tokens=ai_judge_usage.cache_write_tokens+EXCLUDED.cache_write_tokens`, assets.DefaultTenantID, key, max(usage.InputTokens, 0), max(usage.OutputTokens, 0), max(usage.CacheReadTokens, 0), max(usage.CacheWriteTokens, 0)).Error
}
func (s *Store) JudgeUsage(ctx context.Context, user, days int) (app.JudgeUsage, error) {
	out := app.JudgeUsage{Daily: []app.JudgeDayUsage{}}
	if err := member(s.db.WithContext(ctx), user, true, false); err != nil {
		return out, err
	}
	err := s.db.WithContext(ctx).Raw(`SELECT COALESCE(sum(calls),0),COALESCE(sum(input_tokens),0),COALESCE(sum(output_tokens),0),COALESCE(sum(cache_read_tokens),0),COALESCE(sum(cache_write_tokens),0) FROM ai_judge_usage WHERE tenant_id=?`, assets.DefaultTenantID).Row().Scan(&out.Calls, &out.InputTokens, &out.OutputTokens, &out.CacheReadTokens, &out.CacheWriteTokens)
	if err != nil {
		return out, err
	}
	err = s.db.WithContext(ctx).Raw(`SELECT to_char(day,'YYYY-MM-DD') AS date,sum(calls) AS calls,sum(input_tokens) AS input_tokens,sum(output_tokens) AS output_tokens FROM ai_judge_usage WHERE tenant_id=? AND day>=((clock_timestamp() AT TIME ZONE 'UTC')::date-(?::integer-1)) GROUP BY day ORDER BY day`, assets.DefaultTenantID, days).Scan(&out.Daily).Error
	return out, err
}

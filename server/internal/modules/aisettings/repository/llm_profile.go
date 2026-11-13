package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

type LLMProfileStore struct{ db *gorm.DB }

func NewLLMProfileStore(db *gorm.DB) *LLMProfileStore { return &LLMProfileStore{db: db} }

func requireAdmin(tx *gorm.DB, user int) error {
	var role string
	var superuser bool
	err := tx.Raw(`SELECT m.role,u.is_superuser FROM tenant_membership m JOIN auth_user u ON u.id=m.user_id
		JOIN tenant t ON t.id=m.tenant_id WHERE m.tenant_id=? AND m.user_id=? AND u.is_active=TRUE AND t.status='active'
		FOR SHARE OF m,u`, assetapp.DefaultTenantID, user).Row().Scan(&role, &superuser)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (role != "admin" || !superuser)) {
		return settingsapp.ErrForbidden
	}
	return err
}

const profileColumns = `id::text,runtime_id,name,provider,model,base_url,encrypted_api_key AS encrypted_key,encrypted_proxy_url AS encrypted_proxy,is_default,api_key_hint,(octet_length(encrypted_api_key)>0) AS has_api_key,options::text AS options_json,created_at,updated_at`

func decodeProfile(profile *settingsapp.LLMProfile) error {
	profile.Options = settingsapp.DefaultLLMOptions()
	if profile.OptionsJSON != "" {
		if err := json.Unmarshal([]byte(profile.OptionsJSON), &profile.Options); err != nil {
			return err
		}
	}
	if profile.HasAPIKey && profile.APIKeyHint == "" {
		profile.APIKeyHint = "••••"
	}
	return nil
}

func loadProfile(tx *gorm.DB, id string) (*settingsapp.LLMProfile, error) {
	var p settingsapp.LLMProfile
	result := tx.Raw(`SELECT `+profileColumns+` FROM ai_llm_profile WHERE tenant_id=? AND id=?`, assetapp.DefaultTenantID, id).Scan(&p)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, settingsapp.ErrProfileNotFound
	}
	return &p, decodeProfile(&p)
}

func (s *LLMProfileStore) List(ctx context.Context, user int) ([]settingsapp.LLMProfile, error) {
	return s.list(ctx, user, true)
}
func (s *LLMProfileStore) ListForMember(ctx context.Context, user int) ([]settingsapp.LLMProfile, error) {
	return s.list(ctx, user, false)
}
func (s *LLMProfileStore) list(ctx context.Context, user int, admin bool) ([]settingsapp.LLMProfile, error) {
	items := []settingsapp.LLMProfile{}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if admin {
			if err := requireAdmin(tx, user); err != nil {
				return err
			}
		} else if err := requireAgentCatalogMember(ctx, tx, user); err != nil {
			return err
		}
		if err := tx.Raw(`SELECT `+profileColumns+` FROM ai_llm_profile WHERE tenant_id=? ORDER BY is_default DESC,created_at,id`, assetapp.DefaultTenantID).Scan(&items).Error; err != nil {
			return err
		}
		for i := range items {
			if err := decodeProfile(&items[i]); err != nil {
				return err
			}
			items[i].EncryptedKey = nil
			if !admin {
				items[i].EncryptedProxy = nil
			}
		}
		return nil
	})
	return items, err
}

func (s *LLMProfileStore) Save(ctx context.Context, user int, p settingsapp.LLMProfile, create bool) (*settingsapp.LLMProfile, error) {
	var saved *settingsapp.LLMProfile
	raw, err := settingsapp.EncodeLLMOptions(p.Options)
	if err != nil {
		return nil, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		var tenant string
		if err := tx.Raw(`SELECT id::text FROM tenant WHERE id=? AND status='active' FOR UPDATE`, assetapp.DefaultTenantID).Row().Scan(&tenant); err != nil {
			return err
		}
		if !create {
			if _, err := loadProfile(tx, p.ID); err != nil {
				return err
			}
		}
		if p.IsDefault {
			if err := tx.Exec(`UPDATE ai_llm_profile SET is_default=FALSE WHERE tenant_id=? AND is_default=TRUE`, assetapp.DefaultTenantID).Error; err != nil {
				return err
			}
		}
		if create {
			if err := tx.Exec(`INSERT INTO ai_llm_profile(id,tenant_id,name,provider,model,base_url,encrypted_api_key,is_default,options,api_key_hint,encrypted_proxy_url) VALUES(?,?,?,?,?,?,?,?,?::jsonb,?,?)`, p.ID, assetapp.DefaultTenantID, p.Name, p.Provider, p.Model, p.BaseURL, p.EncryptedKey, p.IsDefault, raw, p.APIKeyHint, p.EncryptedProxy).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Exec(`UPDATE ai_llm_profile SET name=?,provider=?,model=?,base_url=?,encrypted_api_key=COALESCE(?,encrypted_api_key),api_key_hint=CASE WHEN ? THEN api_key_hint ELSE ? END,is_default=?,options=?::jsonb,encrypted_proxy_url=CASE WHEN ? THEN ? ELSE encrypted_proxy_url END,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=? AND id=?`, p.Name, p.Provider, p.Model, p.BaseURL, p.EncryptedKey, p.EncryptedKey == nil, p.APIKeyHint, p.IsDefault, raw, p.ReplaceProxy, p.EncryptedProxy, assetapp.DefaultTenantID, p.ID).Error; err != nil {
				return err
			}
		}
		var err error
		saved, err = loadProfile(tx, p.ID)
		return err
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, settingsapp.ErrProfileConflict
		}
		return nil, err
	}
	saved.EncryptedKey = nil
	saved.EncryptedProxy = nil
	return saved, nil
}

func (s *LLMProfileStore) Delete(ctx context.Context, user int, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		var tenant string
		if err := tx.Raw(`SELECT id::text FROM tenant WHERE id=? AND status='active' FOR UPDATE`, assetapp.DefaultTenantID).Row().Scan(&tenant); err != nil {
			return err
		}
		p, err := loadProfile(tx, id)
		if err != nil {
			return err
		}
		if p.IsDefault {
			return settingsapp.ErrProfileActive
		}
		if err := detachTaskProfile(tx, id); err != nil {
			return err
		}
		result := tx.Exec(`DELETE FROM ai_llm_profile WHERE tenant_id=? AND id=?`, assetapp.DefaultTenantID, id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return settingsapp.ErrProfileNotFound
		}
		return nil
	})
}

func (s *LLMProfileStore) ActivateProfile(ctx context.Context, user int, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		var tenant string
		if err := tx.Raw(`SELECT id::text FROM tenant WHERE id=? AND status='active' FOR UPDATE`, assetapp.DefaultTenantID).Row().Scan(&tenant); err != nil {
			return err
		}
		if _, err := loadProfile(tx, id); err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE ai_llm_profile SET is_default=FALSE WHERE tenant_id=? AND is_default=TRUE`, assetapp.DefaultTenantID).Error; err != nil {
			return err
		}
		return tx.Exec(`UPDATE ai_llm_profile SET is_default=TRUE,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=? AND id=?`, assetapp.DefaultTenantID, id).Error
	})
}
func (s *LLMProfileStore) GetDefault(ctx context.Context, user int) (*settingsapp.LLMProfile, error) {
	var profile *settingsapp.LLMProfile
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCatalogMember(ctx, tx, user); err != nil {
			return err
		}
		var id string
		if err := tx.Raw(`SELECT id::text FROM ai_llm_profile WHERE tenant_id=? AND is_default=TRUE`, assetapp.DefaultTenantID).Row().Scan(&id); errors.Is(err, sql.ErrNoRows) {
			return settingsapp.ErrProfileNotFound
		} else if err != nil {
			return err
		}
		var err error
		profile, err = loadProfile(tx, id)
		return err
	})
	return profile, err
}
func (s *LLMProfileStore) GetForAdmin(ctx context.Context, user int, id string) (*settingsapp.LLMProfile, error) {
	return s.get(ctx, user, id, true)
}
func (s *LLMProfileStore) GetForMember(ctx context.Context, user int, id string) (*settingsapp.LLMProfile, error) {
	return s.get(ctx, user, id, false)
}
func (s *LLMProfileStore) get(ctx context.Context, user int, id string, admin bool) (*settingsapp.LLMProfile, error) {
	var profile *settingsapp.LLMProfile
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if admin {
			if err := requireAdmin(tx, user); err != nil {
				return err
			}
		} else if err := requireAgentCatalogMember(ctx, tx, user); err != nil {
			return err
		}
		var err error
		profile, err = loadProfile(tx, id)
		return err
	})
	return profile, err
}

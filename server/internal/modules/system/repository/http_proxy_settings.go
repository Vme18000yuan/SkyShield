package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/yyhuni/lunafox/server/internal/modules/system/domain"
	"github.com/yyhuni/lunafox/server/internal/modules/system/repository/persistence"
	"gorm.io/gorm"
)

const httpProxySettingsSingletonID int16 = 1

type HTTPProxySettingsRepository struct{ db *gorm.DB }

func NewHTTPProxySettingsRepository(db *gorm.DB) *HTTPProxySettingsRepository {
	if db == nil {
		panic("system HTTP proxy settings database is required")
	}
	return &HTTPProxySettingsRepository{db: db}
}

func (repository *HTTPProxySettingsRepository) GetHTTPProxySettings(ctx context.Context) (*domain.HTTPProxySettings, error) {
	var record persistence.HTTPProxySettings
	err := repository.db.WithContext(ctx).Take(&record, "singleton_id = ?", httpProxySettingsSingletonID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read system HTTP proxy settings: %w", err)
	}
	return &domain.HTTPProxySettings{
		Enabled:           record.Enabled,
		EncryptedProxyURL: append([]byte(nil), record.EncryptedProxyURL...),
		UpdatedAt:         record.UpdatedAt.UTC(),
	}, nil
}

func (repository *HTTPProxySettingsRepository) SaveHTTPProxySettings(ctx context.Context, settings domain.HTTPProxySettings, updatedBy int) error {
	if updatedBy <= 0 {
		return fmt.Errorf("system HTTP proxy settings updater is required")
	}
	if err := repository.db.WithContext(ctx).Exec(`
INSERT INTO system_http_proxy_setting(singleton_id,enabled,encrypted_proxy_url,updated_by)
VALUES(?,?,?,?)
ON CONFLICT(singleton_id) DO UPDATE SET
 enabled=EXCLUDED.enabled,
 encrypted_proxy_url=EXCLUDED.encrypted_proxy_url,
 updated_by=EXCLUDED.updated_by,
 updated_at=clock_timestamp()`, httpProxySettingsSingletonID, settings.Enabled, nullableProxyURL(settings.EncryptedProxyURL), updatedBy).Error; err != nil {
		return fmt.Errorf("save system HTTP proxy settings: %w", err)
	}
	return nil
}

func nullableProxyURL(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

var _ domain.HTTPProxySettingsRepository = (*HTTPProxySettingsRepository)(nil)

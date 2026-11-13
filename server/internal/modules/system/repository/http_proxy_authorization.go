package repository

import (
	"context"
	"fmt"

	systemapp "github.com/yyhuni/lunafox/server/internal/modules/system/application"
	"gorm.io/gorm"
)

type ActiveSystemSuperuserRepository struct{ db *gorm.DB }

func NewActiveSystemSuperuserRepository(db *gorm.DB) *ActiveSystemSuperuserRepository {
	if db == nil {
		panic("system settings authorization database is required")
	}
	return &ActiveSystemSuperuserRepository{db: db}
}

func (repository *ActiveSystemSuperuserRepository) IsActiveSuperuser(ctx context.Context, userID int) (bool, error) {
	if userID <= 0 {
		return false, nil
	}
	var count int64
	if err := repository.db.WithContext(ctx).Table("auth_user").Where("id = ? AND is_active = ? AND is_superuser = ?", userID, true, true).Count(&count).Error; err != nil {
		return false, fmt.Errorf("read active system superuser: %w", err)
	}
	return count == 1, nil
}

var _ systemapp.HTTPProxySettingsAuthorizer = (*ActiveSystemSuperuserRepository)(nil)

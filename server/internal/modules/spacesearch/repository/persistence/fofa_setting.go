package persistence

import "time"

type FofaSetting struct {
	TenantID        string    `gorm:"column:tenant_id"`
	Email           string    `gorm:"column:email"`
	EncryptedAPIKey []byte    `gorm:"column:encrypted_api_key"`
	UpdatedBy       int       `gorm:"column:updated_by"`
	UpdatedAt       time.Time `gorm:"column:updated_at"`
}

func (FofaSetting) TableName() string { return "fofa_setting" }

package persistence

import "time"

type HTTPProxySettings struct {
	SingletonID       int16     `gorm:"column:singleton_id;primaryKey;autoIncrement:false"`
	Enabled           bool      `gorm:"column:enabled;not null"`
	EncryptedProxyURL []byte    `gorm:"column:encrypted_proxy_url"`
	UpdatedBy         *int      `gorm:"column:updated_by"`
	UpdatedAt         time.Time `gorm:"column:updated_at;type:timestamptz;not null"`
}

func (HTTPProxySettings) TableName() string { return "system_http_proxy_setting" }

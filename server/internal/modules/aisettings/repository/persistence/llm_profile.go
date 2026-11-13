package persistence

import "time"

type LLMProfile struct {
	ID           string
	Name         string
	Provider     string
	Model        string
	BaseURL      string
	EncryptedKey []byte
	IsDefault    bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

package application

import (
	"context"
	"errors"
	"time"
)

var (
	ErrProfileNotFound       = errors.New("AI model profile not found")
	ErrForbidden             = errors.New("administrator permission required")
	ErrInvalidProfile        = errors.New("invalid AI model profile")
	ErrProfileConflict       = errors.New("AI model profile name already exists")
	ErrProfileActive         = errors.New("active AI model profile cannot be deleted")
	ErrBuiltinSkillProtected = errors.New("built-in skills cannot be deleted or have their source content changed")
)

type LLMProfile struct {
	RuntimeID      int64
	Options        LLMOptions `gorm:"-"`
	OptionsJSON    string
	APIKeyHint     string
	HasAPIKey      bool
	EncryptedProxy []byte
	ReplaceProxy   bool `gorm:"-"`
	ID             string
	Name           string
	Provider       string
	Model          string
	BaseURL        string
	EncryptedKey   []byte
	IsDefault      bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type ProfileInput struct {
	PreserveDefault bool        `json:"preserveDefault,omitempty"`
	Options         *LLMOptions `json:"options,omitempty"`
	Name            string      `json:"name"`
	Provider        string      `json:"provider"`
	Model           string      `json:"model"`
	BaseURL         string      `json:"baseUrl"`
	APIKey          string      `json:"apiKey"`
	IsDefault       bool        `json:"isDefault"`
}

type ProfileStore interface {
	List(context.Context, int) ([]LLMProfile, error)
	ListForMember(context.Context, int) ([]LLMProfile, error)
	Save(context.Context, int, LLMProfile, bool) (*LLMProfile, error)
	Delete(context.Context, int, string) error
	GetDefault(context.Context, int) (*LLMProfile, error)
	GetForAdmin(context.Context, int, string) (*LLMProfile, error)
	GetForMember(context.Context, int, string) (*LLMProfile, error)
}

type SecretCodec interface {
	Seal(string, string) ([]byte, error)
	Open(string, []byte) (string, error)
}

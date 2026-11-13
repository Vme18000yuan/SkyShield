package application

import (
	"context"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Autumn-27/artex/agent"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/httpoutbound"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

type LLMProfileFacade struct {
	store ProfileStore
	codec SecretCodec
}

func NewLLMProfileFacade(store ProfileStore, codec SecretCodec) *LLMProfileFacade {
	return &LLMProfileFacade{store: store, codec: codec}
}

func (f *LLMProfileFacade) List(ctx context.Context, user int) ([]LLMProfile, error) {
	if user <= 0 {
		return nil, ErrForbidden
	}
	items, err := f.store.List(ctx, user)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if len(items[i].EncryptedProxy) > 0 {
			value, err := f.codec.Open(profileScope(items[i].ID)+"/proxy", items[i].EncryptedProxy)
			if err != nil {
				return nil, err
			}
			items[i].Options.Proxy = value
		}
		items[i].EncryptedProxy = nil
	}
	return items, nil
}

func (f *LLMProfileFacade) ListForMember(ctx context.Context, user int) ([]LLMProfile, error) {
	if user <= 0 {
		return nil, ErrForbidden
	}
	return f.store.ListForMember(ctx, user)
}

func profileScope(id string) string { return assetapp.DefaultTenantID + "/" + id }

func (f *LLMProfileFacade) Save(ctx context.Context, user int, id string, input ProfileInput) (*LLMProfile, error) {
	create := id == ""
	if create {
		id = uuid.NewString()
	}
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id || user <= 0 {
		return nil, ErrInvalidProfile
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Provider = strings.TrimSpace(input.Provider)
	if input.Provider == "" {
		if !create {
			return nil, ErrInvalidProfile
		}
		input.Provider = "anthropic"
	}
	input.Model = strings.TrimSpace(input.Model)
	if input.Model == "" {
		if !create {
			return nil, ErrInvalidProfile
		}
		input.Model = defaultLLMModel(input.Provider)
	}
	input.BaseURL = strings.TrimSpace(input.BaseURL)
	if input.Name == "" || utf8.RuneCountInString(input.Name) > 120 || input.Model == "" || len(input.Model) > 200 ||
		(input.Provider != "openai" && input.Provider != "anthropic" && input.Provider != "openai-responses") ||
		len(input.APIKey) > 8192 || len(input.BaseURL) > 2048 {
		return nil, ErrInvalidProfile
	}
	if input.BaseURL != "" {
		u, parseErr := url.Parse(input.BaseURL)
		if parseErr != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
			return nil, ErrInvalidProfile
		}
	}
	var encrypted []byte
	options := DefaultLLMOptions()
	if !create {
		existing, err := f.store.GetForAdmin(ctx, user, id)
		if err != nil {
			return nil, err
		}
		options = existing.Options
		if input.PreserveDefault {
			input.IsDefault = existing.IsDefault
		}
	}
	if input.Options != nil {
		options = *input.Options
	}
	if err := options.Normalize(input.Provider); err != nil {
		return nil, err
	}
	var encryptedProxy []byte
	if input.Options != nil && options.Proxy != "" {
		if err := validateLLMProxy(options.Proxy); err != nil {
			return nil, ErrInvalidProfile
		}
		encryptedProxy, err = f.codec.Seal(profileScope(id)+"/proxy", options.Proxy)
		if err != nil {
			return nil, err
		}
	}
	options.Proxy = ""
	hint := ""
	if input.APIKey != "" {
		encrypted, err = f.codec.Seal(profileScope(id), input.APIKey)
		if err != nil {
			return nil, err
		}
		if len(input.APIKey) >= 4 {
			hint = "…" + input.APIKey[len(input.APIKey)-4:]
		} else {
			hint = "••••"
		}
	}
	if create && encrypted == nil {
		encrypted = []byte{}
	}
	return f.store.Save(ctx, user, LLMProfile{ID: id, Name: input.Name, Provider: input.Provider, Model: input.Model,
		BaseURL: input.BaseURL, EncryptedKey: encrypted, IsDefault: input.IsDefault, Options: options, APIKeyHint: hint, EncryptedProxy: encryptedProxy, ReplaceProxy: input.Options != nil}, create)
}

func defaultLLMModel(provider string) string {
	switch provider {
	case "anthropic":
		return "claude-opus-4-8"
	case "openai":
		return "gpt-4o"
	case "openai-responses":
		return "gpt-5"
	default:
		return ""
	}
}

func (f *LLMProfileFacade) Delete(ctx context.Context, user int, id string) error {
	parsed, err := uuid.Parse(id)
	if user <= 0 || err != nil || parsed.String() != id {
		return ErrInvalidProfile
	}
	return f.store.Delete(ctx, user, id)
}

func (f *LLMProfileFacade) Test(ctx context.Context, user int, id string) (time.Duration, string, error) {
	parsed, err := uuid.Parse(id)
	if user <= 0 || err != nil || parsed.String() != id {
		return 0, "", ErrInvalidProfile
	}
	profile, err := f.store.GetForAdmin(ctx, user, id)
	if err != nil {
		return 0, "", err
	}
	key, err := f.codec.Open(profileScope(id), profile.EncryptedKey)
	if err != nil {
		return 0, "", err
	}
	proxy := ""
	if len(profile.EncryptedProxy) > 0 {
		proxy, err = f.codec.Open(profileScope(id)+"/proxy", profile.EncryptedProxy)
		if err != nil {
			return 0, "", err
		}
	}
	config := profile.Options.AgentConfig(profile.Provider, profile.Model, profile.BaseURL, key, proxy, artexdb.LLMRetryPolicy{})
	config.ProxyResolver = httpoutbound.DefaultProxyController.WithProfileProxy(config.Proxy)
	return agent.TestConnection(ctx, config)
}

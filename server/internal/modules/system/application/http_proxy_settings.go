package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yyhuni/lunafox/server/internal/modules/system/domain"
)

var (
	ErrHTTPProxyForbidden    = errors.New("HTTP proxy settings require an active superuser")
	ErrHTTPProxyInvalidInput = errors.New("HTTP proxy settings input is invalid")
	ErrHTTPProxyURLRequired  = errors.New("an HTTP proxy URL is required when the proxy is enabled")
)

const httpProxyCredentialScope = "tianqiong/system/http-proxy/v1"

type HTTPProxySettingsAuthorizer interface {
	IsActiveSuperuser(context.Context, int) (bool, error)
}

type HTTPProxySecretCodec interface {
	Seal(scope, secret string) ([]byte, error)
	Open(scope string, encrypted []byte) (string, error)
}

type HTTPProxyRuntime interface {
	Set(enabled bool, proxyURL string) error
	UseEnvironment()
}

type HTTPProxySettingsView struct {
	Enabled      bool
	Configured   bool
	ProxyAddress string
	UpdatedAt    *time.Time
}

type HTTPProxySettingsService struct {
	store      domain.HTTPProxySettingsRepository
	authorizer HTTPProxySettingsAuthorizer
	codec      HTTPProxySecretCodec
	runtime    HTTPProxyRuntime
}

func NewHTTPProxySettingsService(
	store domain.HTTPProxySettingsRepository,
	authorizer HTTPProxySettingsAuthorizer,
	codec HTTPProxySecretCodec,
	runtime HTTPProxyRuntime,
) *HTTPProxySettingsService {
	if store == nil || authorizer == nil || codec == nil || runtime == nil {
		panic("HTTP proxy settings dependencies are required")
	}
	return &HTTPProxySettingsService{store: store, authorizer: authorizer, codec: codec, runtime: runtime}
}

// Initialize restores the saved policy before background workers and HTTP
// requests are started. An absent row preserves the process environment's
// existing HTTP_PROXY/HTTPS_PROXY behavior.
func (service *HTTPProxySettingsService) Initialize(ctx context.Context) error {
	settings, err := service.store.GetHTTPProxySettings(ctx)
	if err != nil {
		return fmt.Errorf("load system HTTP proxy settings: %w", err)
	}
	if settings == nil {
		service.runtime.UseEnvironment()
		return nil
	}
	proxyURL := ""
	if len(settings.EncryptedProxyURL) > 0 {
		proxyURL, err = service.codec.Open(httpProxyCredentialScope, settings.EncryptedProxyURL)
		if err != nil {
			if settings.Enabled {
				return fmt.Errorf("decrypt enabled system HTTP proxy settings: %w", err)
			}
			proxyURL = ""
		}
	}
	if err := service.runtime.Set(settings.Enabled, proxyURL); err != nil {
		return fmt.Errorf("restore system HTTP proxy settings: %w", err)
	}
	return nil
}

func (service *HTTPProxySettingsService) Get(ctx context.Context, userID int) (HTTPProxySettingsView, error) {
	if err := service.authorize(ctx, userID); err != nil {
		return HTTPProxySettingsView{}, err
	}
	settings, err := service.store.GetHTTPProxySettings(ctx)
	if err != nil {
		return HTTPProxySettingsView{}, fmt.Errorf("read system HTTP proxy settings: %w", err)
	}
	return service.view(settings)
}

func (service *HTTPProxySettingsService) Update(ctx context.Context, userID int, enabled bool, rawProxyURL string) (HTTPProxySettingsView, error) {
	if err := service.authorize(ctx, userID); err != nil {
		return HTTPProxySettingsView{}, err
	}
	rawProxyURL = strings.TrimSpace(rawProxyURL)
	settings, err := service.store.GetHTTPProxySettings(ctx)
	if err != nil {
		return HTTPProxySettingsView{}, fmt.Errorf("read current system HTTP proxy settings: %w", err)
	}
	var normalizedURL string
	var encryptedURL []byte
	if rawProxyURL != "" {
		normalizedURL, err = domain.NormalizeHTTPProxyURL(rawProxyURL)
		if err != nil {
			return HTTPProxySettingsView{}, ErrHTTPProxyInvalidInput
		}
		encryptedURL, err = service.codec.Seal(httpProxyCredentialScope, normalizedURL)
		if err != nil {
			return HTTPProxySettingsView{}, fmt.Errorf("encrypt system HTTP proxy settings: %w", err)
		}
	} else if settings != nil && len(settings.EncryptedProxyURL) > 0 {
		normalizedURL, err = service.codec.Open(httpProxyCredentialScope, settings.EncryptedProxyURL)
		if err != nil {
			return HTTPProxySettingsView{}, fmt.Errorf("decrypt saved system HTTP proxy settings: %w", err)
		}
		if _, err = domain.NormalizeHTTPProxyURL(normalizedURL); err != nil {
			return HTTPProxySettingsView{}, fmt.Errorf("saved system HTTP proxy settings are invalid: %w", err)
		}
		encryptedURL = append([]byte(nil), settings.EncryptedProxyURL...)
	}
	if enabled && normalizedURL == "" {
		return HTTPProxySettingsView{}, ErrHTTPProxyURLRequired
	}
	if err := service.runtime.Set(enabled, normalizedURL); err != nil {
		return HTTPProxySettingsView{}, ErrHTTPProxyInvalidInput
	}
	updated := domain.HTTPProxySettings{Enabled: enabled, EncryptedProxyURL: encryptedURL}
	if err := service.store.SaveHTTPProxySettings(ctx, updated, userID); err != nil {
		// The runtime policy follows the durable setting; restore the previous
		// state if persistence fails.
		if settings == nil {
			service.runtime.UseEnvironment()
		} else {
			previousURL := ""
			if len(settings.EncryptedProxyURL) > 0 {
				previousURL, _ = service.codec.Open(httpProxyCredentialScope, settings.EncryptedProxyURL)
			}
			_ = service.runtime.Set(settings.Enabled, previousURL)
		}
		return HTTPProxySettingsView{}, fmt.Errorf("save system HTTP proxy settings: %w", err)
	}
	saved, err := service.store.GetHTTPProxySettings(ctx)
	if err != nil {
		return HTTPProxySettingsView{}, fmt.Errorf("read saved system HTTP proxy settings: %w", err)
	}
	return service.view(saved)
}

func (service *HTTPProxySettingsService) Clear(ctx context.Context, userID int) (HTTPProxySettingsView, error) {
	if err := service.authorize(ctx, userID); err != nil {
		return HTTPProxySettingsView{}, err
	}
	updated := domain.HTTPProxySettings{Enabled: false}
	if err := service.store.SaveHTTPProxySettings(ctx, updated, userID); err != nil {
		return HTTPProxySettingsView{}, fmt.Errorf("clear system HTTP proxy settings: %w", err)
	}
	if err := service.runtime.Set(false, ""); err != nil {
		return HTTPProxySettingsView{}, fmt.Errorf("disable system HTTP proxy settings: %w", err)
	}
	saved, err := service.store.GetHTTPProxySettings(ctx)
	if err != nil {
		return HTTPProxySettingsView{}, fmt.Errorf("read cleared system HTTP proxy settings: %w", err)
	}
	return service.view(saved)
}

func (service *HTTPProxySettingsService) view(settings *domain.HTTPProxySettings) (HTTPProxySettingsView, error) {
	if settings == nil {
		return HTTPProxySettingsView{}, nil
	}
	view := HTTPProxySettingsView{Enabled: settings.Enabled, Configured: len(settings.EncryptedProxyURL) > 0}
	if !settings.UpdatedAt.IsZero() {
		updatedAt := settings.UpdatedAt.UTC()
		view.UpdatedAt = &updatedAt
	}
	if view.Configured {
		proxyURL, err := service.codec.Open(httpProxyCredentialScope, settings.EncryptedProxyURL)
		if err != nil {
			return HTTPProxySettingsView{}, fmt.Errorf("decrypt system HTTP proxy settings: %w", err)
		}
		if _, err := domain.NormalizeHTTPProxyURL(proxyURL); err != nil {
			return HTTPProxySettingsView{}, fmt.Errorf("saved system HTTP proxy settings are invalid: %w", err)
		}
		view.ProxyAddress = domain.SafeHTTPProxyAddress(proxyURL)
	}
	return view, nil
}

func (service *HTTPProxySettingsService) authorize(ctx context.Context, userID int) error {
	if userID <= 0 {
		return ErrHTTPProxyForbidden
	}
	allowed, err := service.authorizer.IsActiveSuperuser(ctx, userID)
	if err != nil {
		return fmt.Errorf("resolve system HTTP proxy settings authority: %w", err)
	}
	if !allowed {
		return ErrHTTPProxyForbidden
	}
	return nil
}

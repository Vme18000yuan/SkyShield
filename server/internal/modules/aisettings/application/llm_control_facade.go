package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Autumn-27/artex/agent"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/httpoutbound"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (f *LLMProfileFacade) RuntimeSettings(ctx context.Context, user int) (LLMRuntimeSettings, error) {
	store, ok := f.store.(LLMControlStore)
	if !ok {
		return LLMRuntimeSettings{}, ErrInvalidProfile
	}
	return store.RuntimeSettings(ctx, user, true)
}
func (f *LLMProfileFacade) SaveRuntimeSettings(ctx context.Context, user int, input LLMRuntimeSettings) error {
	store, ok := f.store.(LLMControlStore)
	if !ok {
		return ErrInvalidProfile
	}
	return store.SaveRuntimeSettings(ctx, user, input)
}

func (f *LLMProfileFacade) PatchRuntimeSettings(ctx context.Context, user int, input LLMRuntimePatch) (LLMRuntimeSettings, error) {
	if input.PoolEnabled == nil && input.PoolBindFallback == nil && input.NoaCompaction == nil && input.Retry == nil {
		return LLMRuntimeSettings{}, ErrInvalidProfile
	}
	if input.Retry != nil {
		value := input.Retry.Clamped()
		input.Retry = &value
	}
	store, ok := f.store.(interface {
		PatchRuntimeSettings(context.Context, int, LLMRuntimePatch) (LLMRuntimeSettings, error)
	})
	if !ok {
		return LLMRuntimeSettings{}, ErrInvalidProfile
	}
	return store.PatchRuntimeSettings(ctx, user, input)
}
func (f *LLMProfileFacade) Activate(ctx context.Context, user int, id string) error {
	if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
		return ErrInvalidProfile
	}
	if store, ok := f.store.(interface {
		ActivateProfile(context.Context, int, string) error
	}); ok {
		return store.ActivateProfile(ctx, user, id)
	}
	profile, err := f.store.GetForAdmin(ctx, user, id)
	if err != nil {
		return err
	}
	profile.IsDefault = true
	profile.EncryptedKey = nil
	profile.EncryptedProxy = nil
	profile.ReplaceProxy = false
	_, err = f.store.Save(ctx, user, *profile, false)
	return err
}

type ModelProbeInput struct {
	ProfileID string     `json:"profileId"`
	Provider  string     `json:"provider"`
	Model     string     `json:"model"`
	BaseURL   string     `json:"baseUrl"`
	APIKey    string     `json:"apiKey"`
	Proxy     string     `json:"proxy"`
	Options   LLMOptions `json:"options"`
}

func (f *LLMProfileFacade) draftConfig(ctx context.Context, user int, input ModelProbeInput) (agent.Config, error) {
	if _, err := f.store.List(ctx, user); err != nil {
		return agent.Config{}, err
	}
	if input.Provider != "anthropic" && input.Provider != "openai" && input.Provider != "openai-responses" {
		return agent.Config{}, ErrInvalidProfile
	}
	if len(input.APIKey) > 8192 || len(input.BaseURL) > 2048 || len(input.Model) > 200 {
		return agent.Config{}, ErrInvalidProfile
	}
	input.BaseURL = strings.TrimSpace(input.BaseURL)
	if input.BaseURL != "" {
		u, err := url.Parse(input.BaseURL)
		if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Fragment != "" || u.RawQuery != "" {
			return agent.Config{}, ErrInvalidProfile
		}
	}
	if input.Options.Proxy == "" {
		input.Options.Proxy = input.Proxy
	}
	if input.Options.Proxy != "" {
		if err := validateLLMProxy(input.Options.Proxy); err != nil {
			return agent.Config{}, ErrInvalidProfile
		}
	}
	if input.ProfileID != "" {
		parsed, err := uuid.Parse(input.ProfileID)
		if err != nil || parsed.String() != input.ProfileID {
			return agent.Config{}, ErrInvalidProfile
		}
		p, err := f.store.GetForAdmin(ctx, user, input.ProfileID)
		if err != nil {
			return agent.Config{}, err
		}
		if input.APIKey == "" && len(p.EncryptedKey) > 0 {
			input.APIKey, err = f.codec.Open(profileScope(p.ID), p.EncryptedKey)
			if err != nil {
				return agent.Config{}, err
			}
		}
	}
	if input.APIKey == "" {
		return agent.Config{}, errors.New("未提供 API Key")
	}
	if err := input.Options.Normalize(input.Provider); err != nil {
		return agent.Config{}, err
	}
	state, err := f.RuntimeSettings(ctx, user)
	if err != nil {
		return agent.Config{}, err
	}
	cfg := input.Options.AgentConfig(input.Provider, input.Model, input.BaseURL, input.APIKey, input.Options.Proxy, state.Retry)
	cfg.ProxyResolver = httpoutbound.DefaultProxyController.WithProfileProxy(cfg.Proxy)
	return cfg, nil
}

func (f *LLMProfileFacade) TestDraft(ctx context.Context, user int, input ModelProbeInput) (time.Duration, string, string, error) {
	cfg, err := f.draftConfig(ctx, user, input)
	if err != nil {
		return 0, "", "", err
	}
	latency, reply, err := agent.TestConnection(ctx, cfg)
	if err != nil {
		message := strings.ReplaceAll(err.Error(), cfg.APIKey, "[REDACTED]")
		if cfg.Proxy != "" {
			message = strings.ReplaceAll(message, cfg.Proxy, "[PROXY]")
		}
		if len(message) > 1200 {
			message = message[:1200]
		}
		return latency, "", cfg.Model, errors.New(message)
	}
	return latency, reply, cfg.Model, nil
}

func (f *LLMProfileFacade) FetchModels(ctx context.Context, user int, input ModelProbeInput) ([]string, error) {
	cfg, err := f.draftConfig(ctx, user, input)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		url       string
		anthropic bool
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	var candidates []candidate
	if input.Provider == "openai" || input.Provider == "openai-responses" {
		if base == "" {
			base = "https://api.openai.com/v1"
		}
		base = strings.TrimRight(strings.TrimSuffix(strings.TrimSuffix(base, "/chat/completions"), "/responses"), "/")
		candidates = append(candidates, candidate{base + "/models", false})
	} else {
		if base == "" {
			base = "https://api.anthropic.com"
		}
		base = strings.TrimRight(strings.TrimSuffix(base, "/v1/messages"), "/")
		candidates = append(candidates, candidate{base + "/v1/models", true})
		if root := strings.TrimRight(strings.TrimSuffix(base, "/anthropic"), "/"); root != base {
			candidates = append(candidates, candidate{root + "/models", false}, candidate{root + "/v1/models", false})
		}
	}
	transport := &http.Transport{Proxy: cfg.ProxyResolver}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var lastErr error
	emptyOK := false
	for _, endpoint := range candidates {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.url, nil)
		if err != nil {
			return nil, ErrInvalidProfile
		}
		if endpoint.anthropic {
			request.Header.Set("x-api-key", cfg.APIKey)
			request.Header.Set("anthropic-version", "2023-06-01")
		} else {
			request.Header.Set("Authorization", "Bearer "+cfg.APIKey)
		}
		response, err := client.Do(request)
		if err != nil {
			lastErr = errors.New("无法连接模型接口，请检查地址与代理")
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<20))
		response.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if response.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("模型接口返回 HTTP %d", response.StatusCode)
			continue
		}
		var decoded struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(data, &decoded); err != nil {
			lastErr = errors.New("模型列表响应格式无效")
			continue
		}
		models := []string{}
		for _, row := range decoded.Data {
			if row.ID != "" {
				models = append(models, row.ID)
			}
		}
		if len(models) > 0 {
			return models, nil
		}
		emptyOK = true
	}
	if emptyOK {
		return []string{}, nil
	}
	if lastErr == nil {
		lastErr = errors.New("未获取到模型列表")
	}
	return nil, lastErr
}

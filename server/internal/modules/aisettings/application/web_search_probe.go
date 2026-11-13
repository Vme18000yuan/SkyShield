package application

import (
	"context"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/Autumn-27/norma/tool"
	"github.com/yyhuni/lunafox/server/internal/httpoutbound"
)

type WebSearchProbeInput struct {
	Backend   string `json:"web_search_backend"`
	Proxy     string `json:"web_search_proxy"`
	BraveKey  string `json:"brave_search_api_key"`
	TavilyKey string `json:"tavily_search_api_key"`
}
type WebSearchProbeResult struct {
	OK      bool   `json:"ok"`
	Backend string `json:"backend"`
	Count   int    `json:"count,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (f *RuntimeSettingFacade) DetectPython(ctx context.Context, user int) (string, error) {
	if _, err := f.Get(ctx, user); err != nil {
		return "", err
	}
	path, _ := exec.LookPath("python3")
	if path == "" {
		path, _ = exec.LookPath("python")
	}
	return path, nil
}
func (f *RuntimeSettingFacade) ProbeWebSearch(ctx context.Context, user int, input WebSearchProbeInput) (WebSearchProbeResult, error) {
	if _, err := f.Get(ctx, user); err != nil {
		return WebSearchProbeResult{}, err
	}
	config, _, err := f.SearchConfig(ctx, user)
	if err != nil {
		return WebSearchProbeResult{}, err
	}
	if input.Backend != "" {
		config.Backend = input.Backend
	}
	if config.Backend == "deepseek" && config.DeepSeekAPIKey == "" {
		if err := f.fillDeepSeekCredentials(ctx, user, &config); err != nil {
			return WebSearchProbeResult{}, err
		}
	}
	if strings.TrimSpace(input.BraveKey) != "" {
		config.BraveAPIKey = input.BraveKey
	}
	if strings.TrimSpace(input.TavilyKey) != "" {
		config.TavilyAPIKey = input.TavilyKey
	}
	config.Proxy = strings.TrimSpace(input.Proxy)
	result := WebSearchProbeResult{Backend: config.Backend}
	if config.Proxy != "" && validateLLMProxy(config.Proxy) != nil {
		result.Error = "代理地址无效"
		return result, nil
	}
	timeout := 30 * time.Second
	query := "test"
	if config.Backend == "deepseek" {
		timeout = 120 * time.Second
		query = "DeepSeek company official website"
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	relay, err := httpoutbound.NewLocalRelay(probeCtx, config.Proxy)
	if err != nil {
		result.Error = "无法启动搜索代理"
		return result, nil
	}
	defer relay.Close()
	originalProxy := config.Proxy
	config.Proxy = relay.URL()
	items, err := tool.WebSearchProbe(probeCtx, config, query, 3)
	if err != nil {
		message := err.Error()
		for _, secret := range []string{config.BraveAPIKey, config.TavilyAPIKey, config.DeepSeekAPIKey, originalProxy, config.Proxy} {
			if secret != "" {
				message = strings.ReplaceAll(message, secret, "[REDACTED]")
			}
		}
		if parsed, e := url.Parse(originalProxy); e == nil && parsed.User != nil {
			if password, ok := parsed.User.Password(); ok && password != "" {
				message = strings.ReplaceAll(message, password, "[REDACTED]")
			}
		}
		result.Error = message
		return result, nil
	}
	if len(items) == 0 {
		result.Error = "搜索返回 0 条结果，请检查网络或代理配置"
		return result, nil
	}
	result.OK = true
	result.Count = len(items)
	return result, nil
}

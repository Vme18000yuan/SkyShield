package airuntime

import (
	"fmt"
	"os"
	"strings"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/norma/llm"
	"github.com/yyhuni/lunafox/server/internal/httpoutbound"
)

// EnvironmentProvider uses ARTEX provider construction, while accepting a
// Tianqiong-specific configuration namespace for new deployments.
type EnvironmentProvider struct{ limiter agent.RequestLimiter }

func (p *EnvironmentProvider) Resolve() (llm.Provider, string, bool, error) {
	format := strings.TrimSpace(os.Getenv("TIANQIONG_LLM_PROVIDER"))
	apiKey := strings.TrimSpace(os.Getenv("TIANQIONG_LLM_API_KEY"))
	var cfg agent.Config
	if format == "" && apiKey == "" {
		var ok bool
		cfg, ok = agent.FromEnv()
		if !ok {
			return nil, "", false, nil
		}
	} else {
		if format != "anthropic" && format != "openai" && format != "openai-responses" {
			return nil, "", false, fmt.Errorf("invalid TIANQIONG_LLM_PROVIDER")
		}
		if apiKey == "" {
			return nil, "", false, nil
		}
		cfg = agent.ConfigFrom(format, os.Getenv("TIANQIONG_LLM_MODEL"), os.Getenv("TIANQIONG_LLM_BASE_URL"), apiKey, os.Getenv("TIANQIONG_LLM_PROXY"))
	}
	cfg.ProxyResolver = httpoutbound.DefaultProxyController.WithFallback(cfg.Proxy)
	provider, err := cfg.NewProviderWithLimiter(&p.limiter)
	if err != nil {
		return nil, "", false, err
	}
	return provider, cfg.Model, true, nil
}

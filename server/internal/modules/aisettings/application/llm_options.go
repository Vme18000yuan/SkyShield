package application

import (
	"context"
	"encoding/json"
	"github.com/Autumn-27/artex/agent"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/llmpool"
	"golang.org/x/net/http/httpguts"
	"math"
	"strings"
	"time"
)

// LLMOptions mirrors ARTEX db.LLMProfile without credentials or identities.
type LLMOptions struct {
	FirstResponseSeconds  int                   `json:"first_response_seconds"`
	StreamIdleSeconds     int                   `json:"stream_idle_seconds"`
	RequestTimeoutSeconds int                   `json:"request_timeout_seconds"`
	MaxConcurrent         int                   `json:"max_concurrent"`
	Proxy                 string                `json:"proxy,omitempty"`
	RatePerSecond         float64               `json:"rate_per_second"`
	RatePerMinute         float64               `json:"rate_per_minute"`
	ContextWindowK        int                   `json:"context_window_k"`
	ThinkingType          string                `json:"thinking_type"`
	ReasoningEffort       string                `json:"reasoning_effort"`
	Priority              int                   `json:"priority"`
	PoolExclude           bool                  `json:"pool_exclude"`
	Streaming             *bool                 `json:"streaming,omitempty"`
	MaxTokens             int                   `json:"max_tokens"`
	MaxTokensField        string                `json:"max_tokens_field"`
	SessionHeaderKey      string                `json:"session_header_key"`
	Retry                 artexdb.RetryOverride `json:"retry"`
}

func DefaultLLMOptions() LLMOptions { value := true; return LLMOptions{Streaming: &value} }

func (o *LLMOptions) Normalize(provider string) error {
	if o.ContextWindowK > 0 && o.ContextWindowK < 32 {
		return ErrInvalidProfile
	}
	if o.MaxTokens >= (agent.Config{ContextWindowK: o.ContextWindowK}).CompactionWindow() {
		return ErrInvalidProfile
	}
	for _, seconds := range []int{o.FirstResponseSeconds, o.StreamIdleSeconds, o.RequestTimeoutSeconds} {
		if seconds < 0 || seconds > 7200 {
			return ErrInvalidProfile
		}
	}
	if o.MaxConcurrent < 0 || o.MaxConcurrent > 64 {
		return ErrInvalidProfile
	}
	if math.IsNaN(o.RatePerSecond) || math.IsInf(o.RatePerSecond, 0) || o.RatePerSecond < 0 || o.RatePerSecond > 1e6 || math.IsNaN(o.RatePerMinute) || math.IsInf(o.RatePerMinute, 0) || o.RatePerMinute < 0 || o.RatePerMinute > 1e6 || o.ContextWindowK < 0 || o.ContextWindowK > 1000 || o.MaxTokens < 0 || o.MaxTokens > 10_000_000 || o.Priority < -1_000_000 || o.Priority > 1_000_000 {
		return ErrInvalidProfile
	}
	if o.ThinkingType != "" && o.ThinkingType != "enabled" && o.ThinkingType != "disabled" {
		return ErrInvalidProfile
	}
	if o.ReasoningEffort != "" && o.ReasoningEffort != "low" && o.ReasoningEffort != "medium" && o.ReasoningEffort != "high" && o.ReasoningEffort != "xhigh" && o.ReasoningEffort != "max" {
		return ErrInvalidProfile
	}
	if provider != "openai" || o.MaxTokensField != "max_completion_tokens" {
		o.MaxTokensField = ""
	}
	o.SessionHeaderKey = strings.TrimSpace(o.SessionHeaderKey)
	if len(o.SessionHeaderKey) > 120 || o.SessionHeaderKey != "" && !httpguts.ValidHeaderFieldName(o.SessionHeaderKey) {
		return ErrInvalidProfile
	}
	o.Retry = o.Retry.Clamped()
	return nil
}

func (o LLMOptions) AgentConfig(provider, model, baseURL, key, proxy string, policy artexdb.LLMRetryPolicy) agent.Config {
	cfg := agent.ConfigFrom(provider, model, baseURL, key, proxy)
	cfg.FirstResponseTimeout = time.Duration(o.FirstResponseSeconds) * time.Second
	cfg.StreamIdleTimeout = time.Duration(o.StreamIdleSeconds) * time.Second
	cfg.RequestTimeout = time.Duration(o.RequestTimeoutSeconds) * time.Second
	cfg.MaxConcurrent = o.MaxConcurrent
	cfg.RatePerSecond, cfg.RatePerMinute = o.RatePerSecond, o.RatePerMinute
	cfg.ContextWindowK, cfg.ThinkingType, cfg.ReasoningEffort = o.ContextWindowK, o.ThinkingType, o.ReasoningEffort
	cfg.Stream = o.Streaming == nil || *o.Streaming
	cfg.MaxTokens, cfg.MaxTokensField, cfg.SessionHeaderKey = o.MaxTokens, o.MaxTokensField, o.SessionHeaderKey
	connect, empty, stream := o.Retry.Connect.Or(policy.Connect), o.Retry.Empty.Or(policy.Empty), o.Retry.Stream.Or(policy.Stream)
	cfg.Retry = agent.RetryConfig{ConnectAttempts: connect.Attempts, ConnectInterval: connect.Interval(), EmptyAttempts: empty.Attempts, EmptyInterval: empty.Interval(), StreamAttempts: stream.Attempts, StreamInterval: stream.Interval()}
	return cfg
}

type LLMRuntimeSettings struct {
	PoolEnabled      bool                   `json:"llm_pool_enabled"`
	PoolBindFallback bool                   `json:"llm_pool_bind_fallback"`
	Retry            artexdb.LLMRetryPolicy `json:"retry"`
	NoaCompaction    bool                   `json:"noa_compaction"`
}

type LLMRuntimePatch struct {
	PoolEnabled      *bool                   `json:"llm_pool_enabled,omitempty"`
	PoolBindFallback *bool                   `json:"llm_pool_bind_fallback,omitempty"`
	NoaCompaction    *bool                   `json:"noa_compaction,omitempty"`
	Retry            *artexdb.LLMRetryPolicy `json:"retry,omitempty"`
}

type LLMControlStore interface {
	RuntimeSettings(context.Context, int, bool) (LLMRuntimeSettings, error)
	SaveRuntimeSettings(context.Context, int, LLMRuntimeSettings) error
	ReadLLMHealth(context.Context) (map[int64]llmpool.State, error)
	SaveLLMHealth(context.Context, int64, llmpool.State) error
	ResetLLMHealth(context.Context, int64) error
}

func EncodeLLMOptions(o LLMOptions) (string, error) {
	o.Proxy = ""
	data, err := json.Marshal(o)
	return string(data), err
}

type LLMPoolMember struct {
	ProfileID       string     `json:"profile_id"`
	Name            string     `json:"name"`
	Model           string     `json:"model"`
	Format          string     `json:"format"`
	Priority        int        `json:"priority"`
	Active          bool       `json:"active"`
	Excluded        bool       `json:"excluded"`
	State           string     `json:"state"`
	Fails           int        `json:"fails"`
	Trips           int        `json:"trips"`
	CooldownSeconds int64      `json:"cooldown_secs"`
	LastError       string     `json:"last_error,omitempty"`
	LastAt          *time.Time `json:"last_at,omitempty"`
}
type LLMPoolStatus struct {
	Enabled      bool            `json:"enabled"`
	BindFallback bool            `json:"bind_fallback"`
	Chain        []LLMPoolMember `json:"chain"`
}
type LLMPoolController interface {
	PoolStatus(context.Context, int) (LLMPoolStatus, error)
	ResetPool(context.Context, int, string) (LLMPoolStatus, error)
}

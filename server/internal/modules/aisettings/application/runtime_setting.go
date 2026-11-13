package application

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"os/exec"
	"strings"
)

type RuntimeSettingStore interface {
	ReadRuntimeOptions(context.Context, int, bool) (map[string]any, map[string][]byte, error)
	PatchRuntimeOptions(context.Context, int, map[string]any, map[string][]byte) error
}
type RuntimeSettingFacade struct {
	store    RuntimeSettingStore
	codec    SecretCodec
	profiles ProfileStore
}

func NewRuntimeSettingFacade(store RuntimeSettingStore, codec SecretCodec, profiles ProfileStore) *RuntimeSettingFacade {
	return &RuntimeSettingFacade{store: store, codec: codec, profiles: profiles}
}
func runtimeOptionDefaults() map[string]any {
	return map[string]any{"chat_run_max_tokens": 0, "chat_run_max_requests": 0, "task_run_max_tokens": 0, "task_run_max_requests": 0, "traffic_capture": false, "credential_agent_enabled": false, "credential_traffic_binding": false, "task_intelligence_enabled": false, "agent_traffic_binding": false, "llm_record": false, "web_search_enabled": false, "web_search_backend": "ddgs", "brave_key_set": false, "tavily_key_set": false, "web_search_proxy": "", "global_proxy": "", "python_interpreter": "", "workers": 3, "worker_pools": []any{}, "global_process_limit": 16, "user_process_limit": 8, "global_execution_limit": 16, "user_execution_limit": 8, "global_model_limit": 32, "user_model_limit": 8, "task_concurrency_enabled": true, "task_concurrency_limit": 5, "llm_pool_enabled": false, "llm_pool_bind_fallback": false, "constraints_inject_planner": true, "constraints_inject_worker": true, "noa_compaction": false, "notify_enabled": true, "notify_public_base_url": "", "notify_digest_interval_min": 30}
}

var runtimeSecretKeys = map[string]bool{"brave_search_api_key": true, "tavily_search_api_key": true, "web_search_proxy": true}

func (f *RuntimeSettingFacade) Get(ctx context.Context, user int) (map[string]any, error) {
	return f.read(ctx, user, true, false)
}
func (f *RuntimeSettingFacade) read(ctx context.Context, user int, admin, includeKeys bool) (map[string]any, error) {
	if user <= 0 {
		return nil, ErrForbidden
	}
	options, secrets, err := f.store.ReadRuntimeOptions(ctx, user, admin)
	if err != nil {
		return nil, err
	}
	out := runtimeOptionDefaults()
	for key, value := range options {
		if !runtimeSecretKeys[key] {
			out[key] = value
		}
	}
	for key, cipher := range secrets {
		if len(cipher) == 0 {
			continue
		}
		if key == "brave_search_api_key" {
			out["brave_key_set"] = true
		}
		if key == "tavily_search_api_key" {
			out["tavily_key_set"] = true
		}
		if includeKeys || admin && key == "web_search_proxy" {
			value, err := f.codec.Open(assets.DefaultTenantID+"/runtime/"+key, cipher)
			if err != nil {
				return nil, err
			}
			out[key] = value
		}
	}
	if out["python_interpreter"] == "" {
		path, _ := exec.LookPath("python3")
		out["python_interpreter"] = path
	}
	return out, nil
}
func (f *RuntimeSettingFacade) Patch(ctx context.Context, user int, input map[string]any) (map[string]any, error) {
	if _, err := f.Get(ctx, user); err != nil {
		return nil, err
	}
	defaults := runtimeOptionDefaults()
	patch := map[string]any{}
	secrets := map[string][]byte{}
	for key, value := range input {
		if runtimeSecretKeys[key] {
			text, ok := value.(string)
			if !ok || len(text) > 16384 {
				return nil, ErrInvalidProfile
			}
			if key == "web_search_proxy" && text != "" {
				if err := validateLLMProxy(text); err != nil {
					return nil, ErrInvalidProfile
				}
			}
			var cipher []byte
			if text != "" {
				var err error
				cipher, err = f.codec.Seal(assets.DefaultTenantID+"/runtime/"+key, text)
				if err != nil {
					return nil, err
				}
			}
			secrets[key] = cipher
			continue
		}
		original, ok := defaults[key]
		if !ok || key == "brave_key_set" || key == "tavily_key_set" || key == "global_proxy" {
			return nil, ErrInvalidProfile
		}
		switch original.(type) {
		case bool:
			flag, ok := value.(bool)
			if !ok {
				return nil, ErrInvalidProfile
			}
			if key == "llm_record" && flag {
				if f.codec == nil {
					return nil, ErrInvalidProfile
				}
				if _, err := f.codec.Seal(assets.DefaultTenantID+"/llm-record-check", "enabled"); err != nil {
					return nil, err
				}
			}
		case string:
			text, ok := value.(string)
			if !ok || len(text) > 4096 {
				return nil, ErrInvalidProfile
			}
			if key == "web_search_backend" && text != "ddgs" && text != "brave-free" && text != "tavily" && text != "deepseek" {
				return nil, ErrInvalidProfile
			}
			if key == "python_interpreter" && strings.ContainsAny(text, "\r\n\x00") {
				return nil, ErrInvalidProfile
			}
		case int:
			number, ok := value.(float64)
			maximum := 1000
			if key == "chat_run_max_tokens" || key == "task_run_max_tokens" {
				maximum = 100_000_000
			}
			if key == "chat_run_max_requests" || key == "task_run_max_requests" {
				maximum = 4096
			}
			if !ok || number != float64(int(number)) || number < 0 || number > float64(maximum) {
				return nil, ErrInvalidProfile
			}
			if key == "global_process_limit" || key == "user_process_limit" {
				if number < 2 || number > 16 {
					return nil, ErrInvalidProfile
				}
			}
			if key == "global_execution_limit" || key == "user_execution_limit" || key == "global_process_limit" || key == "user_process_limit" {
				if number < 1 || number > 16 {
					return nil, ErrInvalidProfile
				}
			}
			if key == "global_model_limit" || key == "user_model_limit" {
				if number < 1 || number > 64 {
					return nil, ErrInvalidProfile
				}
			}
			if key == "workers" && (number < 1 || number > 50) || key == "task_concurrency_limit" && number < 1 {
				return nil, ErrInvalidProfile
			}
		case []any:
			if key != "worker_pools" {
				return nil, ErrInvalidProfile
			}
			raw, _ := json.Marshal(value)
			var pools []struct {
				ProfileID string `json:"profile_id"`
				Workers   int    `json:"workers"`
			}
			if json.Unmarshal(raw, &pools) != nil || len(pools) > 50 {
				return nil, ErrInvalidProfile
			}
			seen := map[string]bool{}
			total := 0
			for _, pool := range pools {
				parsed, parseErr := uuid.Parse(pool.ProfileID)
				if parseErr != nil || parsed.String() != pool.ProfileID || seen[pool.ProfileID] || pool.Workers < 1 || pool.Workers > 50 {
					return nil, ErrInvalidProfile
				}
				seen[pool.ProfileID] = true
				total += pool.Workers
				if total > 50 {
					return nil, ErrInvalidProfile
				}
				if _, err := f.profiles.GetForAdmin(ctx, user, pool.ProfileID); err != nil {
					return nil, err
				}
			}
		}
		patch[key] = value
	}
	if err := f.store.PatchRuntimeOptions(ctx, user, patch, secrets); err != nil {
		return nil, err
	}
	return f.Get(ctx, user)
}

func (f *RuntimeSettingFacade) ChatBudget(ctx context.Context, user int) (airuntime.ModelRunBudgetLimits, error) {
	return f.modelRunBudget(ctx, user, "chat_run")
}
func (f *RuntimeSettingFacade) TaskBudget(ctx context.Context, user int) (airuntime.ModelRunBudgetLimits, error) {
	return f.modelRunBudget(ctx, user, "task_run")
}
func (f *RuntimeSettingFacade) modelRunBudget(ctx context.Context, user int, prefix string) (airuntime.ModelRunBudgetLimits, error) {
	options, err := f.OptionsForMember(ctx, user)
	if err != nil {
		return airuntime.ModelRunBudgetLimits{}, err
	}
	read := func(key string, maximum int64) (int64, error) {
		var n int64
		switch value := options[key].(type) {
		case int:
			n = int64(value)
		case float64:
			if value != float64(int64(value)) {
				return 0, ErrInvalidProfile
			}
			n = int64(value)
		default:
			return 0, ErrInvalidProfile
		}
		if n < 0 || n > maximum {
			return 0, ErrInvalidProfile
		}
		return n, nil
	}
	tokens, err := read(prefix+"_max_tokens", 100_000_000)
	if err != nil {
		return airuntime.ModelRunBudgetLimits{}, err
	}
	requests, err := read(prefix+"_max_requests", 4096)
	if err != nil {
		return airuntime.ModelRunBudgetLimits{}, err
	}
	return airuntime.ModelRunBudgetLimits{Requests: requests, Tokens: tokens}, nil
}

// WorkerSlots expands ARTEX's per-model pools. An explicit task count uses
// unpinned slots; otherwise global pool order and global worker count apply.
func (f *RuntimeSettingFacade) WorkerSlots(ctx context.Context, user, override int) ([]string, error) {
	if override < 0 || override > 50 {
		return nil, ErrInvalidProfile
	}
	if override > 0 {
		return make([]string, override), nil
	}
	options, err := f.OptionsForMember(ctx, user)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(options["worker_pools"])
	var pools []struct {
		ProfileID string `json:"profile_id"`
		Workers   int    `json:"workers"`
	}
	if json.Unmarshal(raw, &pools) != nil {
		return nil, ErrInvalidProfile
	}
	if len(pools) > 0 {
		slots := []string{}
		seen := map[string]bool{}
		for _, pool := range pools {
			if pool.ProfileID == "" || seen[pool.ProfileID] || pool.Workers < 1 || len(slots)+pool.Workers > 50 {
				return nil, ErrInvalidProfile
			}
			seen[pool.ProfileID] = true
			for i := 0; i < pool.Workers; i++ {
				slots = append(slots, pool.ProfileID)
			}
		}
		return slots, nil
	}
	workers := 3
	switch value := options["workers"].(type) {
	case int:
		workers = value
	case float64:
		workers = int(value)
	}
	if workers < 1 || workers > 50 {
		workers = 3
	}
	return make([]string, workers), nil
}
func (f *RuntimeSettingFacade) SearchConfig(ctx context.Context, user int) (tool.WebSearchConfig, bool, error) {
	options, err := f.read(ctx, user, false, true)
	if err != nil {
		return tool.WebSearchConfig{}, false, err
	}
	enabled, _ := options["web_search_enabled"].(bool)
	text := func(key string) string { value, _ := options[key].(string); return value }
	config := tool.WebSearchConfig{Backend: text("web_search_backend"), BraveAPIKey: text("brave_search_api_key"), TavilyAPIKey: text("tavily_search_api_key"), Proxy: text("web_search_proxy")}
	if config.Backend == "deepseek" {
		if err := f.fillDeepSeekCredentials(ctx, user, &config); err != nil {
			return config, false, err
		}
	}
	return config, enabled, nil
}

func (f *RuntimeSettingFacade) fillDeepSeekCredentials(ctx context.Context, user int, config *tool.WebSearchConfig) error {
	profile, err := f.profiles.GetDefault(ctx, user)
	if err != nil && !errors.Is(err, ErrProfileNotFound) {
		return err
	}
	if profile != nil && len(profile.EncryptedKey) > 0 {
		key, err := f.codec.Open(assets.DefaultTenantID+"/"+profile.ID, profile.EncryptedKey)
		if err != nil {
			return err
		}
		config.DeepSeekBaseURL, config.DeepSeekAPIKey, config.DeepSeekModel = profile.BaseURL, key, profile.Model
	}
	return nil
}
func (f *RuntimeSettingFacade) OptionsForMember(ctx context.Context, user int) (map[string]any, error) {
	return f.read(ctx, user, false, false)
}

func (f *RuntimeSettingFacade) PythonInterpreter(ctx context.Context, user int) (string, error) {
	options, err := f.OptionsForMember(ctx, user)
	if err != nil {
		return "", err
	}
	value, _ := options["python_interpreter"].(string)
	return value, nil
}

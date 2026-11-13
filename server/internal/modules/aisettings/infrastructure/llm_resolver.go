package infrastructure

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/llmpool"
	"github.com/Autumn-27/artex/sidequestion"
	"github.com/Autumn-27/norma/llm"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/httpoutbound"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

type profileProviderEntry struct {
	fingerprint [32]byte
	provider    llm.Provider
	config      agent.Config
}
type LLMResolver struct {
	governor    *airuntime.ExecutionGovernor
	store       settingsapp.ProfileStore
	codec       settingsapp.SecretCodec
	fallback    *airuntime.EnvironmentProvider
	limiters    sync.Map
	mu          sync.Mutex
	cache       map[string]profileProviderEntry
	health      *llmpool.Registry
	restore     sync.Once
	healthWrite sync.Mutex
	usage       airuntime.LLMUsageSink
}

// The recorder is installed at bootstrap before providers are resolved. Clear
// cached concrete providers if it changes so no profile retains an old sink.
func (r *LLMResolver) SetUsageSink(sink airuntime.LLMUsageSink) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usage = sink
	r.cache = map[string]profileProviderEntry{}
}
func (r *LLMResolver) usageSink() airuntime.LLMUsageSink {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.usage
}
func (r *LLMResolver) bindUsageUser(provider llm.Provider, user int) llm.Provider {
	if r.governor == nil && r.usageSink() == nil {
		return provider
	}
	return airuntime.BindLLMUsageUser(provider, user)
}

func (r *LLMResolver) SetExecutionGovernor(g *airuntime.ExecutionGovernor) { r.governor = g }

// Configure compaction for every eligible task-chain member before a failover
// occurs. This also protects the experimental compactor's initial window.
func (r *LLMResolver) WithChainWindow(ctx context.Context, user int, provider llm.Provider, ids []string) (llm.Provider, error) {
	profiles, err := r.store.ListForMember(ctx, user)
	if err != nil {
		return nil, err
	}
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	options := airuntime.ModelOptions(provider)
	for _, profile := range profiles {
		if selected[profile.ID] {
			window := (agent.Config{ContextWindowK: profile.Options.ContextWindowK}).CompactionWindow()
			if window < options.ContextWindow {
				options.ContextWindow = window
			}
		}
	}
	return airuntime.WithModelOptions(provider, options), nil
}

func NewLLMResolver(store settingsapp.ProfileStore, codec settingsapp.SecretCodec) *LLMResolver {
	r := &LLMResolver{store: store, codec: codec, fallback: &airuntime.EnvironmentProvider{}, cache: map[string]profileProviderEntry{}}
	r.health = llmpool.NewRegistry(func(id int64, _ llmpool.State) { r.persistHealth(id) }, func(id int64) { r.persistHealth(id) })
	return r
}

func (r *LLMResolver) persistHealth(id int64) {
	control, ok := r.store.(settingsapp.LLMControlStore)
	if !ok {
		return
	}
	r.healthWrite.Lock()
	defer r.healthWrite.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	current := r.health.Get(id)
	if current.LastAt.IsZero() {
		_ = control.ResetLLMHealth(ctx, id)
	} else {
		_ = control.SaveLLMHealth(ctx, id, current)
	}
}
func (r *LLMResolver) settings(ctx context.Context, user int, admin bool) (settingsapp.LLMRuntimeSettings, error) {
	if control, ok := r.store.(settingsapp.LLMControlStore); ok {
		state, err := control.RuntimeSettings(ctx, user, admin)
		if err != nil {
			return state, err
		}
		r.restore.Do(func() {
			rows, err := control.ReadLLMHealth(ctx)
			if err == nil {
				for id, item := range rows {
					r.health.Restore(id, item)
				}
			}
		})
		r.health.SetPolicy(state.Retry.Breaker.Attempts, state.Retry.Breaker.Interval())
		return state, nil
	}
	return settingsapp.LLMRuntimeSettings{}, nil
}
func (r *LLMResolver) Resolve(ctx context.Context, user int) (llm.Provider, string, bool, error) {
	return r.ResolveProfile(ctx, user, "")
}

// ResolveProfileExclusive leaves task-chain failover to the task router, even
// when the separately configurable global model pool is enabled.
func (r *LLMResolver) ResolveProfileExclusive(ctx context.Context, user int, id string) (llm.Provider, string, bool, error) {
	profile, err := r.store.GetForMember(ctx, user, id)
	if err != nil {
		return nil, "", false, err
	}
	state, err := r.settings(ctx, user, false)
	if err != nil {
		return nil, "", false, err
	}
	provider, cfg, ok, err := r.direct(profile, state)
	return r.bindUsageUser(provider, user), cfg.Model, ok, err
}
func (r *LLMResolver) ResolveProfile(ctx context.Context, user int, profileID string) (llm.Provider, string, bool, error) {
	var profile *settingsapp.LLMProfile
	var err error
	if profileID == "" {
		profile, err = r.store.GetDefault(ctx, user)
	} else {
		profile, err = r.store.GetForMember(ctx, user, profileID)
	}
	if errors.Is(err, settingsapp.ErrProfileNotFound) && profileID == "" {
		provider, model, ok, err := r.fallback.Resolve()
		provider = r.governor.Provider(provider, user)
		provider = airuntime.MeterLLMProvider(provider, r.usageSink(), "", "", model)
		return r.bindUsageUser(provider, user), model, ok, err
	}
	if err != nil {
		return nil, "", false, err
	}
	state, err := r.settings(ctx, user, false)
	if err != nil {
		return nil, "", false, err
	}
	provider, cfg, ok, err := r.direct(profile, state)
	if err != nil || !ok {
		return provider, cfg.Model, ok, err
	}
	if !state.PoolEnabled || profileID != "" && !state.PoolBindFallback {
		return r.bindUsageUser(provider, user), cfg.Model, true, nil
	}
	profiles, err := r.store.ListForMember(ctx, user)
	if err != nil {
		return nil, "", false, err
	}
	sortProfiles(profiles)
	members := []*llmpool.Member{{ID: profile.RuntimeID, Name: profile.Name, Model: cfg.Model, Format: profile.Provider, Priority: profile.Options.Priority, Active: profile.IsDefault, Rank: llmpool.RankActive, WindowTokens: cfg.CompactionWindow(), Prov: provider}}
	for _, candidate := range profiles {
		if candidate.ID == profile.ID || candidate.Options.PoolExclude && !candidate.IsDefault || !candidate.HasAPIKey {
			continue
		}
		full, err := r.store.GetForMember(ctx, user, candidate.ID)
		if err != nil {
			return nil, "", false, err
		}
		p, c, configured, err := r.direct(full, state)
		if err != nil || !configured {
			continue
		}
		rank := candidate.Options.Priority
		if candidate.IsDefault {
			rank = llmpool.RankActive - 1
		}
		members = append(members, &llmpool.Member{ID: candidate.RuntimeID, Name: candidate.Name, Model: c.Model, Format: candidate.Provider, Priority: candidate.Options.Priority, Active: candidate.IsDefault, Rank: rank, WindowTokens: c.CompactionWindow(), Prov: p})
	}
	if len(members) > 1 {
		options := airuntime.ModelOptions(provider)
		for _, member := range members {
			if member.WindowTokens > 0 && member.WindowTokens < options.ContextWindow {
				options.ContextWindow = member.WindowTokens
			}
		}
		provider = airuntime.WithModelOptions(llmpool.New(members, r.health), options)
	}
	return r.bindUsageUser(provider, user), cfg.Model, true, nil
}
func sortProfiles(items []settingsapp.LLMProfile) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.IsDefault != b.IsDefault {
			return a.IsDefault
		}
		if a.Options.Priority != b.Options.Priority {
			return a.Options.Priority > b.Options.Priority
		}
		return a.RuntimeID < b.RuntimeID
	})
}
func (r *LLMResolver) direct(profile *settingsapp.LLMProfile, state settingsapp.LLMRuntimeSettings) (llm.Provider, agent.Config, bool, error) {
	if profile == nil {
		return nil, agent.Config{}, false, settingsapp.ErrProfileNotFound
	}
	if len(profile.EncryptedKey) == 0 {
		return nil, agent.Config{}, false, nil
	}
	raw, _ := json.Marshal(struct {
		Profile  *settingsapp.LLMProfile
		Settings settingsapp.LLMRuntimeSettings
	}{profile, state})
	fingerprint := sha256.Sum256(raw)
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry, ok := r.cache[profile.ID]; ok && entry.fingerprint == fingerprint {
		return entry.provider, entry.config, true, nil
	}
	key, err := r.codec.Open(assetapp.DefaultTenantID+"/"+profile.ID, profile.EncryptedKey)
	if err != nil {
		return nil, agent.Config{}, false, err
	}
	proxy := ""
	if len(profile.EncryptedProxy) > 0 {
		proxy, err = r.codec.Open(assetapp.DefaultTenantID+"/"+profile.ID+"/proxy", profile.EncryptedProxy)
		if err != nil {
			return nil, agent.Config{}, false, err
		}
	}
	cfg := profile.Options.AgentConfig(profile.Provider, profile.Model, profile.BaseURL, key, proxy, state.Retry)
	cfg.ProxyResolver = httpoutbound.DefaultProxyController.WithProfileProxy(cfg.Proxy)
	value, _ := r.limiters.LoadOrStore(profile.ID, &agent.RequestLimiter{})
	provider, err := cfg.NewProviderWithLimiter(value.(*agent.RequestLimiter))
	if err != nil {
		return nil, cfg, false, err
	}
	provider = airuntime.MeterLLMProvider(provider, r.usage, profile.ID, profile.Name, profile.Model)
	provider = r.governor.ProfileProvider(provider, profile.ID)
	provider = sidequestion.Bind(provider, sidequestion.Model{ProfileID: profile.RuntimeID, Name: profile.Name, Format: profile.Provider, Model: profile.Model, Identity: fmt.Sprintf("%x", fingerprint), Streaming: profile.Options.Streaming == nil || *profile.Options.Streaming, WindowTokens: cfg.CompactionWindow()})
	provider = airuntime.ConfigureProvider(provider, cfg, state.Retry, state.NoaCompaction)
	r.cache[profile.ID] = profileProviderEntry{fingerprint: fingerprint, provider: provider, config: cfg}
	return provider, cfg, true, nil
}

// Side questions pin the concrete snapshot provider. They never advance the
// primary run's failover chain or silently reuse a changed model configuration.
func (r *LLMResolver) ResolveSideModel(ctx context.Context, user int, model sidequestion.Model) (llm.Provider, error) {
	profiles, err := r.store.ListForMember(ctx, user)
	if err != nil {
		return nil, err
	}
	for _, profile := range profiles {
		if profile.RuntimeID != model.ProfileID {
			continue
		}
		full, err := r.store.GetForMember(ctx, user, profile.ID)
		if err != nil {
			return nil, err
		}
		state, err := r.settings(ctx, user, false)
		if err != nil {
			return nil, err
		}
		provider, _, configured, err := r.direct(full, state)
		if err != nil {
			return nil, err
		}
		r.mu.Lock()
		entry := r.cache[profile.ID]
		r.mu.Unlock()
		if !configured || fmt.Sprintf("%x", entry.fingerprint) != model.Identity {
			return nil, errors.New("原模型配置已变更，请先在主会话继续一次对话")
		}
		return r.bindUsageUser(provider, user), nil
	}
	return nil, errors.New("原模型已删除，请先在主会话选择可用模型")
}
func (r *LLMResolver) PoolStatus(ctx context.Context, user int) (settingsapp.LLMPoolStatus, error) {
	profiles, err := r.store.List(ctx, user)
	if err != nil {
		return settingsapp.LLMPoolStatus{}, err
	}
	state, err := r.settings(ctx, user, true)
	if err != nil {
		return settingsapp.LLMPoolStatus{}, err
	}
	sortProfiles(profiles)
	out := settingsapp.LLMPoolStatus{Enabled: state.PoolEnabled, BindFallback: state.PoolBindFallback, Chain: []settingsapp.LLMPoolMember{}}
	for _, p := range profiles {
		health := r.health.Get(p.RuntimeID)
		status := "ok"
		if health.Open() {
			status = "tripped"
		} else if health.Fails > 0 {
			status = "degraded"
		}
		var at *time.Time
		if !health.LastAt.IsZero() {
			copy := health.LastAt
			at = &copy
		}
		out.Chain = append(out.Chain, settingsapp.LLMPoolMember{ProfileID: p.ID, Name: p.Name, Model: p.Model, Format: p.Provider, Priority: p.Options.Priority, Active: p.IsDefault, Excluded: p.Options.PoolExclude, State: status, Fails: health.Fails, Trips: health.Trips, CooldownSeconds: max(0, int64(time.Until(health.OpenUntil).Seconds())), LastError: health.LastError, LastAt: at})
	}
	return out, nil
}
func (r *LLMResolver) ResetPool(ctx context.Context, user int, id string) (settingsapp.LLMPoolStatus, error) {
	profiles, err := r.store.List(ctx, user)
	if err != nil {
		return settingsapp.LLMPoolStatus{}, err
	}
	var runtimeID int64
	if id != "" {
		for _, p := range profiles {
			if p.ID == id {
				runtimeID = p.RuntimeID
				break
			}
		}
		if runtimeID == 0 {
			return settingsapp.LLMPoolStatus{}, settingsapp.ErrProfileNotFound
		}
	}
	if control, ok := r.store.(settingsapp.LLMControlStore); ok {
		if err := control.ResetLLMHealth(ctx, runtimeID); err != nil {
			return settingsapp.LLMPoolStatus{}, err
		}
	}
	if id != "" {
		r.health.Reset(runtimeID)
	} else {
		for _, p := range profiles {
			r.health.Reset(p.RuntimeID)
		}
	}
	return r.PoolStatus(ctx, user)
}

var _ settingsapp.LLMPoolController = (*LLMResolver)(nil)

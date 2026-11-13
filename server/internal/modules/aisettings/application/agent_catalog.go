package application

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Autumn-27/artex/agent"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
)

var (
	ErrAgentCatalogNotFound = errors.New("AI agent not found")
	ErrAgentCatalogConflict = errors.New("AI agent key already exists")
	ErrAgentCatalogBuiltin  = errors.New("built-in AI agents cannot be changed")
	ErrAgentCatalogInvalid  = errors.New("invalid AI agent")
)

var agentCatalogKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type AgentCatalogStore interface {
	ListCustomAgents(context.Context, int) ([]airuntime.ChatAgentDefinition, error)
	GetCustomAgent(context.Context, int, string) (*airuntime.ChatAgentDefinition, error)
	CreateCustomAgent(context.Context, int, airuntime.ChatAgentDefinition) (*airuntime.ChatAgentDefinition, error)
	UpdateCustomAgent(context.Context, int, airuntime.ChatAgentDefinition) (*airuntime.ChatAgentDefinition, error)
	DeleteCustomAgent(context.Context, int, string) error
	ListAgentRuntimeSettings(context.Context, int) (map[string]AgentRuntimeSettings, error)
	UpdateBuiltinAgent(context.Context, int, airuntime.ChatAgentDefinition) error
	ListAgentPromptVersions(context.Context, int, string) ([]AgentPromptVersion, error)
}

type AgentPromptVersion struct {
	Version   int       `json:"version"`
	Template  string    `json:"template"`
	CreatedAt time.Time `json:"createdAt"`
}

type AgentRuntimeSettings struct {
	Options             *airuntime.AgentOptions
	MCPCount            int64
	SkillCount          int64
	ToolCount           int64
	Configured          bool
	Enabled             bool
	MaxTurns            int
	RunSeconds          int
	PromptTemplate      string
	LLMProfileID        *string
	WrapupPrompt        string
	WrapupMaxTurns      int
	TaskTimeoutPrompt   string
	TaskTimeoutMaxTurns int
}

type AgentCatalogFacade struct {
	store    AgentCatalogStore
	settings airuntime.AgentConfigResolver
}

func NewAgentCatalogFacade(store AgentCatalogStore, settings ...airuntime.AgentConfigResolver) *AgentCatalogFacade {
	if store == nil {
		panic("AI agent catalog store is required")
	}
	facade := &AgentCatalogFacade{store: store}
	if len(settings) > 0 {
		facade.settings = settings[0]
	}
	return facade
}

func (facade *AgentCatalogFacade) List(ctx context.Context, userID int) ([]airuntime.ChatAgentDefinition, error) {
	if userID <= 0 {
		return nil, ErrForbidden
	}
	custom, err := facade.store.ListCustomAgents(ctx, userID)
	if err != nil {
		return nil, err
	}
	items := airuntime.BuiltinChatAgents()
	items = append(items, custom...)
	runtimeSettings, err := facade.store.ListAgentRuntimeSettings(ctx, userID)
	if err != nil {
		return nil, err
	}
	for i := range items {
		applyAgentRuntimeSettings(&items[i], runtimeSettings[items[i].Key])
		if !inheritsLegacyAgentSetting(items[i]) || facade.settings == nil {
			continue
		}
		config, err := facade.settings(ctx, userID, items[i].SettingsKey)
		if err != nil {
			return nil, err
		}
		items[i].Enabled = items[i].Enabled && config.Enabled
		items[i].MaxTurns, items[i].RunSeconds = config.MaxTurns, config.RunSeconds
	}
	return items, nil
}

func inheritsLegacyAgentSetting(item airuntime.ChatAgentDefinition) bool {
	if !item.Builtin || item.RuntimeConfigured {
		return false
	}
	return item.Key == "assistant" || item.Key == "mainagent" || item.Key == "planner" || item.Key == "worker"
}

func (facade *AgentCatalogFacade) Get(ctx context.Context, userID int, key string) (*airuntime.ChatAgentDefinition, error) {
	if userID <= 0 {
		return nil, ErrForbidden
	}
	var item *airuntime.ChatAgentDefinition
	if builtin, ok := airuntime.BuiltinChatAgent(key); ok {
		item = &builtin
	} else {
		var err error
		item, err = facade.store.GetCustomAgent(ctx, userID, key)
		if err != nil {
			return nil, err
		}
		if item == nil {
			return nil, ErrAgentCatalogNotFound
		}
	}
	runtimeSettings, err := facade.store.ListAgentRuntimeSettings(ctx, userID)
	if err != nil {
		return nil, err
	}
	lookupKey := key
	if key == "assistant" {
		lookupKey = "mainagent"
	}
	applyAgentRuntimeSettings(item, runtimeSettings[lookupKey])
	if inheritsLegacyAgentSetting(*item) && facade.settings != nil {
		config, err := facade.settings(ctx, userID, item.SettingsKey)
		if err != nil {
			return nil, err
		}
		item.Enabled = item.Enabled && config.Enabled
		item.MaxTurns, item.RunSeconds = config.MaxTurns, config.RunSeconds
	}
	return item, nil
}

func applyAgentRuntimeSettings(item *airuntime.ChatAgentDefinition, settings AgentRuntimeSettings) {
	if item == nil {
		return
	}
	item.Options = airuntime.DefaultAgentOptions(item.Key)
	if settings.Options != nil {
		item.Options = *settings.Options
	}
	item.MCPCount, item.SkillCount, item.ToolCount = settings.MCPCount, settings.SkillCount, settings.ToolCount
	if item.Builtin && settings.Configured {
		item.Enabled, item.MaxTurns, item.RunSeconds = settings.Enabled, settings.MaxTurns, settings.RunSeconds
		item.RuntimeConfigured = true
		if settings.PromptTemplate != "" {
			item.PromptTemplate = settings.PromptTemplate
			item.SystemPrompt = settings.PromptTemplate
		}
	}
	item.LLMProfileID = settings.LLMProfileID
	item.WrapupPrompt, item.WrapupMaxTurns = settings.WrapupPrompt, settings.WrapupMaxTurns
	item.TaskTimeoutPrompt, item.TaskTimeoutMaxTurns = settings.TaskTimeoutPrompt, settings.TaskTimeoutMaxTurns
}

func (facade *AgentCatalogFacade) Create(ctx context.Context, userID int, input airuntime.ChatAgentDefinition) (*airuntime.ChatAgentDefinition, error) {
	if userID <= 0 {
		return nil, ErrForbidden
	}
	input.Key = strings.TrimSpace(input.Key)
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.SystemPrompt = strings.TrimSpace(input.SystemPrompt)
	if _, isBuiltin := airuntime.BuiltinChatAgent(input.Key); isBuiltin || !validAgentCatalogDefinition(input) {
		return nil, ErrAgentCatalogInvalid
	}
	input.Builtin = false
	input.Role = "assistant"
	input.SettingsKey = "assistant"
	input.Enabled = true
	if strings.TrimSpace(input.SystemPrompt) == "" {
		input.SystemPrompt = agent.DefaultAssistantPrompt
	}
	if err := airuntime.ValidateAgentPromptTemplate(input.Key, input.SystemPrompt); err != nil {
		return nil, ErrAgentCatalogInvalid
	}
	input.MaxTurns = 0
	input.RunSeconds = 1200
	if _, err := facade.store.CreateCustomAgent(ctx, userID, input); err != nil {
		return nil, err
	}
	return facade.Get(ctx, userID, input.Key)
}

func (facade *AgentCatalogFacade) Update(ctx context.Context, userID int, key string, input airuntime.ChatAgentDefinition) (*airuntime.ChatAgentDefinition, error) {
	if userID <= 0 {
		return nil, ErrForbidden
	}
	builtin, isBuiltin := airuntime.BuiltinChatAgent(key)
	if key == "assistant" {
		return nil, ErrAgentCatalogBuiltin
	}
	input.Key = strings.TrimSpace(key)
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.SystemPrompt = strings.TrimSpace(input.SystemPrompt)
	if input.UpdateLLMBinding && input.LLMProfileID != nil {
		parsed, err := uuid.Parse(*input.LLMProfileID)
		if err != nil || parsed.String() != *input.LLMProfileID {
			return nil, ErrAgentCatalogInvalid
		}
	}
	if input.UpdateWrapup && (len(input.WrapupPrompt) > 32768 || len(input.TaskTimeoutPrompt) > 32768 || !utf8.ValidString(input.WrapupPrompt) || !utf8.ValidString(input.TaskTimeoutPrompt) || strings.ContainsRune(input.WrapupPrompt, '\x00') || strings.ContainsRune(input.TaskTimeoutPrompt, '\x00') || input.WrapupMaxTurns < 0 || input.WrapupMaxTurns > 1000 || input.TaskTimeoutMaxTurns < 0 || input.TaskTimeoutMaxTurns > 1000) {
		return nil, ErrAgentCatalogInvalid
	}
	if isBuiltin {
		input.Name, input.Description, input.Role, input.SettingsKey, input.Builtin = builtin.Name, builtin.Description, builtin.Role, builtin.SettingsKey, true
	}
	if !validAgentCatalogDefinition(input) || input.MaxTurns < 0 || input.MaxTurns > 1000 || input.RunSeconds < 0 || input.RunSeconds > 86400 {
		return nil, ErrAgentCatalogInvalid
	}
	if !isBuiltin {
		input.Builtin = false
		input.Role = "assistant"
		input.SettingsKey = "assistant"
	}
	if strings.TrimSpace(input.SystemPrompt) == "" {
		if isBuiltin {
			input.SystemPrompt = builtin.PromptTemplate
		} else {
			input.SystemPrompt = agent.DefaultAssistantPrompt
		}
	}
	if err := airuntime.ValidateAgentPromptTemplate(key, input.SystemPrompt); err != nil {
		return nil, ErrAgentCatalogInvalid
	}
	if isBuiltin {
		if err := facade.store.UpdateBuiltinAgent(ctx, userID, input); err != nil {
			return nil, err
		}
	} else if _, err := facade.store.UpdateCustomAgent(ctx, userID, input); err != nil {
		return nil, err
	}
	return facade.Get(ctx, userID, key)
}

func (facade *AgentCatalogFacade) Delete(ctx context.Context, userID int, key string) error {
	if userID <= 0 {
		return ErrForbidden
	}
	if _, isBuiltin := airuntime.BuiltinChatAgent(key); isBuiltin {
		return ErrAgentCatalogBuiltin
	}
	return facade.store.DeleteCustomAgent(ctx, userID, key)
}

func (facade *AgentCatalogFacade) ResolveForChat(ctx context.Context, userID int, key string) (airuntime.ChatAgentDefinition, error) {
	item, err := facade.Get(ctx, userID, key)
	if err != nil {
		if errors.Is(err, ErrAgentCatalogNotFound) {
			return airuntime.ChatAgentDefinition{}, airuntime.ErrChatAgentNotFound
		}
		return airuntime.ChatAgentDefinition{}, err
	}
	if !item.Builtin && item.SystemPrompt == "" {
		item.SystemPrompt = agent.DefaultAssistantPrompt
	}
	return *item, nil
}

func validAgentCatalogDefinition(item airuntime.ChatAgentDefinition) bool {
	key := strings.TrimSpace(item.Key)
	name := strings.TrimSpace(item.Name)
	description := strings.TrimSpace(item.Description)
	prompt := strings.TrimSpace(item.SystemPrompt)
	return agentCatalogKeyPattern.MatchString(key) && name != "" && utf8.RuneCountInString(name) <= 120 &&
		!strings.ContainsAny(name, "\r\n\x00") && len(description) <= 4096 &&
		len(prompt) <= 65536 && utf8.ValidString(prompt) && !strings.ContainsRune(prompt, '\x00') && item.RunSeconds <= 86400 && item.MaxTurns <= 1000
}

func (facade *AgentCatalogFacade) RuntimeDefinition(ctx context.Context, userID int, key string) (airuntime.ChatAgentDefinition, error) {
	item, err := facade.ResolveForChat(ctx, userID, key)
	if err != nil {
		return airuntime.ChatAgentDefinition{}, fmt.Errorf("resolve AI chat agent: %w", err)
	}
	return item, nil
}

func (facade *AgentCatalogFacade) PromptVersions(ctx context.Context, userID int, key string) ([]AgentPromptVersion, error) {
	item, err := facade.Get(ctx, userID, key)
	if err != nil {
		return nil, err
	}
	if key == "assistant" {
		key = "mainagent"
	}
	versions, err := facade.store.ListAgentPromptVersions(ctx, userID, key)
	if err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		return []AgentPromptVersion{{Version: 1, Template: item.SystemPrompt}}, nil
	}
	return versions, nil
}

func (facade *AgentCatalogFacade) PreviewPrompt(ctx context.Context, userID int, key, prompt string) (string, error) {
	item, err := facade.Get(ctx, userID, key)
	if err != nil {
		return "", err
	}
	if prompt == "" {
		prompt = item.SystemPrompt
	}
	if len(prompt) > 65536 || airuntime.ValidateAgentPromptTemplate(key, prompt) != nil {
		return "", ErrAgentCatalogInvalid
	}
	return airuntime.RenderAgentPrompt(prompt, "/workspace")
}

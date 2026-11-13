package application

import (
	"context"
	"encoding/json"
	"github.com/Autumn-27/artex/agent"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"unicode/utf8"
)

type AgentConfigPatch struct {
	Enabled            *bool           `json:"enabled,omitempty"`
	MaxTurns           *int            `json:"max_turns,omitempty"`
	RunSeconds         *int            `json:"run_seconds,omitempty"`
	LLMProfileID       json.RawMessage `json:"llm_profile_id,omitempty"`
	WebSearch          *bool           `json:"web_search,omitempty"`
	InteractiveShell   *bool           `json:"interactive_shell,omitempty"`
	TriggerRunMode     *string         `json:"trigger_run_mode,omitempty"`
	TriggerMergeMode   *string         `json:"trigger_merge_mode,omitempty"`
	TriggerMaxParallel *int            `json:"trigger_max_parallel,omitempty"`
}

type AgentVisibility struct {
	MCP   []string `json:"mcp"`
	Skill []string `json:"skill"`
}
type AgentControlStore interface {
	RequireControlAdmin(context.Context, int) error
	PatchAgentConfig(context.Context, int, airuntime.ChatAgentDefinition, AgentConfigPatch) error
	WriteAgentPrompt(context.Context, int, airuntime.ChatAgentDefinition, string) (int, error)
	WriteAgentWrapup(context.Context, int, string, bool, string, int) error
	ReadAgentVisibility(context.Context, int, string) (AgentVisibility, error)
	WriteAgentVisibility(context.Context, int, string, AgentVisibility) error
	ListAgentTriggers(context.Context, int, string) ([]artexdb.AgentTrigger, error)
	GetAgentTrigger(context.Context, int, int64) (*artexdb.AgentTrigger, error)
	SaveAgentTrigger(context.Context, int, string, artexdb.AgentTrigger, bool) (*artexdb.AgentTrigger, error)
	DeleteAgentTrigger(context.Context, int, int64) error
}
type AgentControlFacade struct {
	catalog  *AgentCatalogFacade
	store    AgentControlStore
	profiles ProfileStore
}

func NewAgentControlFacade(catalog *AgentCatalogFacade, store AgentControlStore, profiles ProfileStore) *AgentControlFacade {
	return &AgentControlFacade{catalog: catalog, store: store, profiles: profiles}
}

func (f *AgentControlFacade) PatchConfig(ctx context.Context, user int, key string, input AgentConfigPatch) error {
	item, err := f.catalog.Get(ctx, user, key)
	if err != nil {
		return err
	}
	if input.MaxTurns != nil && (*input.MaxTurns < 0 || *input.MaxTurns > 1000) || input.RunSeconds != nil && (*input.RunSeconds < 0 || *input.RunSeconds > 86400) {
		return ErrAgentCatalogInvalid
	}
	if input.LLMProfileID != nil {
		var id *string
		if err := json.Unmarshal(input.LLMProfileID, &id); err != nil {
			return ErrAgentCatalogInvalid
		}
		if id != nil {
			parsed, err := uuid.Parse(*id)
			if err != nil || parsed.String() != *id {
				return ErrAgentCatalogInvalid
			}
		}
	}
	if input.TriggerRunMode != nil && *input.TriggerRunMode != "serial" && *input.TriggerRunMode != "parallel" || input.TriggerMergeMode != nil && *input.TriggerMergeMode != "by_task" && *input.TriggerMergeMode != "all" && *input.TriggerMergeMode != "none" || input.TriggerMaxParallel != nil && (*input.TriggerMaxParallel < 0 || *input.TriggerMaxParallel > 1000) {
		return ErrAgentCatalogInvalid
	}
	if item.Builtin && (input.TriggerRunMode != nil || input.TriggerMergeMode != nil || input.TriggerMaxParallel != nil) {
		return ErrAgentCatalogBuiltin
	}
	return f.store.PatchAgentConfig(ctx, user, *item, input)
}
func (f *AgentControlFacade) SavePrompt(ctx context.Context, user int, key, prompt string, reset bool) (int, error) {
	item, err := f.catalog.Get(ctx, user, key)
	if err != nil {
		return 0, err
	}
	if reset {
		builtin, ok := airuntime.BuiltinChatAgent(key)
		if !ok {
			return 0, ErrAgentCatalogBuiltin
		}
		prompt = builtin.PromptTemplate
	}
	if len(prompt) > 65536 || !utf8.ValidString(prompt) || airuntime.ValidateAgentPromptTemplate(key, prompt) != nil {
		return 0, ErrAgentCatalogInvalid
	}
	return f.store.WriteAgentPrompt(ctx, user, *item, prompt)
}
func (f *AgentControlFacade) SaveWrapup(ctx context.Context, user int, key string, taskTimeout bool, prompt string, turns int) error {
	if _, err := f.catalog.Get(ctx, user, key); err != nil {
		return err
	}
	if len(prompt) > 32768 || !utf8.ValidString(prompt) || turns < 0 || turns > 1000 {
		return ErrAgentCatalogInvalid
	}
	if taskTimeout && agent.TaskTimeoutWrapupDefault(key) == "" {
		return ErrAgentCatalogInvalid
	}
	return f.store.WriteAgentWrapup(ctx, user, key, taskTimeout, prompt, turns)
}
func (f *AgentControlFacade) Visibility(ctx context.Context, user int, key string) (AgentVisibility, error) {
	if _, err := f.catalog.Get(ctx, user, key); err != nil {
		return AgentVisibility{}, err
	}
	return f.store.ReadAgentVisibility(ctx, user, key)
}
func (f *AgentControlFacade) SetVisibility(ctx context.Context, user int, key string, input AgentVisibility) error {
	if _, err := f.catalog.Get(ctx, user, key); err != nil {
		return err
	}
	if len(input.MCP) > 1000 || len(input.Skill) > 1000 {
		return ErrAgentCatalogInvalid
	}
	seen := map[string]bool{}
	for _, id := range input.MCP {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != id || seen[id] {
			return ErrAgentCatalogInvalid
		}
		seen[id] = true
	}
	seen = map[string]bool{}
	for _, name := range input.Skill {
		if name == "" || len(name) > 200 || seen[name] {
			return ErrAgentCatalogInvalid
		}
		seen[name] = true
	}
	return f.store.WriteAgentVisibility(ctx, user, key, input)
}
func (f *AgentControlFacade) Triggers(ctx context.Context, user int, key string) ([]artexdb.AgentTrigger, error) {
	if _, err := f.catalog.Get(ctx, user, key); err != nil {
		return nil, err
	}
	return f.store.ListAgentTriggers(ctx, user, key)
}
func (f *AgentControlFacade) SaveTrigger(ctx context.Context, user int, key string, input artexdb.AgentTrigger, create bool) (*artexdb.AgentTrigger, error) {
	if !create {
		old, err := f.store.GetAgentTrigger(ctx, user, input.ID)
		if err != nil {
			return nil, err
		}
		key = old.AgentKey
	}
	item, err := f.catalog.Get(ctx, user, key)
	if err != nil {
		return nil, err
	}
	if item.Builtin {
		return nil, ErrAgentCatalogBuiltin
	}
	if input.IntervalSec < 0 || input.IntervalSec > 31536000 || input.OnToolCall && len(input.ToolNames) == 0 || len(input.ToolNames) > 200 {
		return nil, ErrAgentCatalogInvalid
	}
	if input.IntervalSec == 0 && !input.OnFinding && !input.OnGoalMet && !input.OnTaskTimeout && !input.OnToolCall && !input.OnTaskCreate {
		return nil, ErrAgentCatalogInvalid
	}
	for _, message := range []string{input.IntervalMessage, input.FindingMessage, input.GoalMessage, input.TaskTimeoutMessage, input.ToolCallMessage, input.TaskCreateMessage} {
		if len(message) > 32768 || !utf8.ValidString(message) {
			return nil, ErrAgentCatalogInvalid
		}
	}
	input.AgentKey = key
	return f.store.SaveAgentTrigger(ctx, user, key, input, create)
}
func (f *AgentControlFacade) DeleteTrigger(ctx context.Context, user int, id int64) error {
	if id <= 0 {
		return ErrAgentCatalogInvalid
	}
	return f.store.DeleteAgentTrigger(ctx, user, id)
}

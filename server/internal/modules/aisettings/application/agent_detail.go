package application

import (
	"bytes"
	"context"
	"github.com/Autumn-27/artex/agent"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"text/template"
	"time"
)

type NativeAgent struct {
	ID           string  `json:"id"`
	Key          string  `json:"key"`
	Name         string  `json:"name"`
	Description  string  `json:"description"`
	Role         string  `json:"role"`
	Builtin      bool    `json:"builtin"`
	Enabled      bool    `json:"enabled"`
	LLMProfileID *string `json:"llm_profile_id"`
	MaxTurns     int     `json:"max_turns"`
	RunSeconds   int     `json:"run_seconds"`
	airuntime.AgentOptions
	MCPCount   int64 `json:"mcp_count"`
	SkillCount int64 `json:"skill_count"`
	ToolCount  int64 `json:"tool_count"`
}

func nativeAgent(item airuntime.ChatAgentDefinition) NativeAgent {
	return NativeAgent{ID: item.Key, Key: item.Key, Name: item.Name, Description: item.Description, Role: item.Role, Builtin: item.Builtin, Enabled: item.Enabled, LLMProfileID: item.LLMProfileID, MaxTurns: item.MaxTurns, RunSeconds: item.RunSeconds, AgentOptions: item.Options, MCPCount: item.MCPCount, SkillCount: item.SkillCount, ToolCount: item.ToolCount}
}

type PromptVariable struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Example     string `json:"example"`
	Source      string `json:"source"`
}
type NativePromptVersion struct {
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"ts"`
	Note      string    `json:"note"`
	Template  string    `json:"template_text"`
}
type NativeModelChoice struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Model     string `json:"model"`
	IsDefault bool   `json:"is_default"`
}
type NativeAgentDetail struct {
	Agent                      NativeAgent           `json:"agent"`
	Prompt                     string                `json:"prompt"`
	Variables                  []PromptVariable      `json:"variables"`
	Versions                   []NativePromptVersion `json:"versions"`
	Visibility                 AgentVisibility       `json:"visibility"`
	Models                     []NativeModelChoice   `json:"llm_profiles"`
	WrapupPrompt               string                `json:"wrapup_prompt"`
	WrapupDefault              string                `json:"wrapup_default"`
	WrapupMaxTurns             int                   `json:"wrapup_max_turns"`
	WrapupMaxTurnsDefault      int                   `json:"wrapup_max_turns_default"`
	TaskTimeoutSupported       bool                  `json:"task_timeout_wrapup_supported"`
	TaskTimeoutPrompt          string                `json:"task_timeout_wrapup_prompt"`
	TaskTimeoutDefault         string                `json:"task_timeout_wrapup_default"`
	TaskTimeoutMaxTurns        int                   `json:"task_timeout_wrapup_max_turns"`
	TaskTimeoutMaxTurnsDefault int                   `json:"task_timeout_wrapup_max_turns_default"`
}

func AgentPromptVariables(key string) []PromptVariable {
	items := []PromptVariable{}
	switch key {
	case "goals":
		items = append(items, PromptVariable{"EngagementDescription", "任务描述（测试对象/背景）", "测试 example.com 站点", "exploration"})
	case "planner":
		items = append(items, PromptVariable{"Goal", "任务总目标", "拿下 example.com 的管理员权限", "exploration"}, PromptVariable{"AssetSummary", "资产计数/类型分布摘要（可选）", "domain:3 ip:5 site:2", "distilled"})
	case "worker":
		items = append(items, PromptVariable{"ProxyAddr", "记录代理地址", "127.0.0.1:8080", "runtime"}, PromptVariable{"WorkerName", "worker 自我标识（可选）", "worker-1", "runtime"})
	case "mainagent", "assistant":
		items = append(items, PromptVariable{"Goal", "当前任务目标", "拿下 example.com 的管理员权限", "exploration"}, PromptVariable{"AssetSummary", "开局态势摘要（可选）", "domain:3 ip:5", "distilled"}, PromptVariable{"FindingsSummary", "已确认漏洞摘要（可选）", "high:1 medium:2", "distilled"})
	}
	return append(items, PromptVariable{"Now", "服务端当前时间（每次运行实时刷新）", time.Now().Format("2006-01-02 15:04:05 MST"), "runtime"}, PromptVariable{"DataDir", "服务端数据根目录", "/app/data", "runtime"})
}

func (f *AgentControlFacade) Details(ctx context.Context, user int, key string) (NativeAgentDetail, error) {
	if err := f.store.RequireControlAdmin(ctx, user); err != nil {
		return NativeAgentDetail{}, err
	}
	item, err := f.catalog.Get(ctx, user, key)
	if err != nil {
		return NativeAgentDetail{}, err
	}
	versions, err := f.catalog.PromptVersions(ctx, user, key)
	if err != nil {
		return NativeAgentDetail{}, err
	}
	visibility, err := f.store.ReadAgentVisibility(ctx, user, key)
	if err != nil {
		return NativeAgentDetail{}, err
	}
	models, err := f.profiles.ListForMember(ctx, user)
	if err != nil {
		return NativeAgentDetail{}, err
	}
	out := NativeAgentDetail{Agent: nativeAgent(*item), Prompt: item.SystemPrompt, Variables: AgentPromptVariables(key), Visibility: visibility, Versions: []NativePromptVersion{}, Models: []NativeModelChoice{}, WrapupPrompt: item.WrapupPrompt, WrapupDefault: agent.WrapupDefault(key), WrapupMaxTurns: item.WrapupMaxTurns, WrapupMaxTurnsDefault: agent.WrapupTurnsDefault(key), TaskTimeoutSupported: agent.TaskTimeoutWrapupDefault(key) != "", TaskTimeoutPrompt: item.TaskTimeoutPrompt, TaskTimeoutDefault: agent.TaskTimeoutWrapupDefault(key), TaskTimeoutMaxTurns: item.TaskTimeoutMaxTurns, TaskTimeoutMaxTurnsDefault: agent.WrapupTurnsDefault(key)}
	if item.Builtin {
		if store, ok := f.store.(interface {
			ReadSavedAgentPrompt(context.Context, int, string) (string, bool, error)
		}); ok {
			prompt, found, err := store.ReadSavedAgentPrompt(ctx, user, key)
			if err != nil {
				return NativeAgentDetail{}, err
			}
			if found {
				out.Prompt = prompt
			}
		}
	}
	for _, version := range versions {
		out.Versions = append(out.Versions, NativePromptVersion{Version: version.Version, CreatedAt: version.CreatedAt, Template: version.Template})
	}
	for _, model := range models {
		out.Models = append(out.Models, NativeModelChoice{ID: model.ID, Name: model.Name, Model: model.Model, IsDefault: model.IsDefault})
	}
	return out, nil
}
func (f *AgentControlFacade) Preview(ctx context.Context, user int, key, prompt string, sample map[string]string) (string, error) {
	if err := f.store.RequireControlAdmin(ctx, user); err != nil {
		return "", err
	}
	if _, err := f.catalog.Get(ctx, user, key); err != nil {
		return "", err
	}
	if len(prompt) > 65536 || airuntime.ValidateAgentPromptTemplate(key, prompt) != nil {
		return "", ErrAgentCatalogInvalid
	}
	values := map[string]string{}
	for _, variable := range AgentPromptVariables(key) {
		values[variable.Name] = variable.Example
	}
	if key == "planner" {
		values["Scope"] = ""
	}
	for name, value := range sample {
		if _, exists := values[name]; !exists || len(value) > 4096 {
			return "", ErrAgentCatalogInvalid
		}
		values[name] = value
	}
	parsed, err := template.New("preview").Option("missingkey=error").Parse(prompt)
	if err != nil {
		return "", ErrAgentCatalogInvalid
	}
	var output bytes.Buffer
	if err := parsed.Execute(&output, values); err != nil {
		return "", ErrAgentCatalogInvalid
	}
	return output.String(), nil
}

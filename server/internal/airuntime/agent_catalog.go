package airuntime

import (
	"bytes"
	"errors"
	"text/template"
	"time"

	"github.com/Autumn-27/artex/agent"
)

var ErrChatAgentNotFound = errors.New("AI chat agent not found")

// ChatAgentDefinition is the catalog projection used by the AI conversation
// runtime. SettingsKey maps ARTEX Agent identities to the platform's existing
// assistant/planner/worker setting rows.
type ChatAgentDefinition struct {
	Options             AgentOptions
	MCPCount            int64
	SkillCount          int64
	ToolCount           int64
	Key                 string
	Name                string
	Description         string
	Role                string
	SettingsKey         string
	SystemPrompt        string
	PromptTemplate      string
	LLMProfileID        *string
	UpdateLLMBinding    bool
	WrapupPrompt        string
	WrapupMaxTurns      int
	TaskTimeoutPrompt   string
	TaskTimeoutMaxTurns int
	UpdateWrapup        bool
	RuntimeConfigured   bool
	Builtin             bool
	Enabled             bool
	MaxTurns            int
	RunSeconds          int
}

func AgentWrapupPolicy(definition ChatAgentDefinition) agent.WrapupPolicy {
	return agent.WrapupPolicy{Prompt: definition.WrapupPrompt, MaxTurns: definition.WrapupMaxTurns,
		TaskTimeoutPrompt: definition.TaskTimeoutPrompt, TaskTimeoutMaxTurns: definition.TaskTimeoutMaxTurns}
}

var builtinChatAgents = []ChatAgentDefinition{
	{Key: "goals", Name: "目标拆解", Description: "把渗透任务目标拆解成若干独立、可验证的子目标。", Role: "goals", SettingsKey: "planner", Builtin: true, Enabled: true, RunSeconds: 1200},
	{Key: "planner", Name: "规划", Description: "读取态势、判定目标，只在确有未覆盖的新方向时补充探索意图（每任务一个规划循环）。", Role: "planner", SettingsKey: "planner", Builtin: true, Enabled: true, RunSeconds: 1200},
	{Key: "mainagent", Name: "主", Description: "人机接口：观察进展，把人的意图落成 hint 或高优先级意图。", Role: "main", SettingsKey: "assistant", Builtin: true, Enabled: true, RunSeconds: 1200},
	{Key: "worker", Name: "执行", Description: "领取一条意图执行，把发现的事实和漏洞写回知识图谱后停止。", Role: "worker", SettingsKey: "worker", Builtin: true, Enabled: true, RunSeconds: 1200},
	{Key: "auto", Name: "Auto", Description: "平台操作助手：用工具管理任务、资产、技能、自定义工具和 MCP。", Role: "assistant", SettingsKey: "assistant", Builtin: true, Enabled: true, RunSeconds: 1200},
	{Key: "pentest", Name: "渗透测试", Description: "独立渗透 agent：一人从侦察→找攻击面→深入利用→验证→收尾走完整条链，自己规划、自己执行、自己对抗式验证。", Role: "assistant", SettingsKey: "assistant", Builtin: true, Enabled: true},
}

func BuiltinChatAgents() []ChatAgentDefinition {
	items := make([]ChatAgentDefinition, len(builtinChatAgents))
	copy(items, builtinChatAgents)
	for i := range items {
		items[i].Options = DefaultAgentOptions(items[i].Key)
		items[i].PromptTemplate = agent.BuiltinPromptSeeds()[items[i].Key]
		items[i].SystemPrompt = items[i].PromptTemplate
	}
	return items
}

func BuiltinChatAgent(key string) (ChatAgentDefinition, bool) {
	for _, item := range BuiltinChatAgents() {
		if item.Key == key {
			return item, true
		}
	}
	// Existing Tianqiong conversations were created with `assistant`; keep them
	// resolving to ARTEX's mainagent role after the catalog migration.
	if key == "assistant" {
		item, _ := BuiltinChatAgent("mainagent")
		item.Key = "assistant"
		return item, true
	}
	return ChatAgentDefinition{}, false
}

// RenderAgentPrompt renders ARTEX's prompt variables for a conversation run.
func RenderAgentPrompt(prompt, dataDir string) (string, error) {
	vars := map[string]string{
		"Goal": "", "Scope": "", "AssetSummary": "", "DataDir": dataDir,
		"Now": time.Now().Format("2006-01-02 15:04:05 MST"), "FindingsSummary": "",
		"EngagementDescription": "", "ProxyAddr": "", "WorkerName": "",
	}
	tmpl, err := template.New("agent").Option("missingkey=error").Parse(prompt)
	if err != nil {
		return "", err
	}
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, vars); err != nil {
		return "", err
	}
	return rendered.String(), nil
}

func ValidateAgentPromptTemplate(key string, prompt string) error {
	var vars any = struct{ DataDir, Now string }{}
	switch key {
	case "goals":
		vars = agent.GoalsVars{}
	case "planner":
		vars = agent.PlannerVars{}
	case "worker":
		vars = agent.WorkerVars{}
	case "mainagent", "assistant":
		vars = agent.MainVars{}
	}
	tmpl, err := template.New("agent").Option("missingkey=error").Parse(prompt)
	if err != nil {
		return err
	}
	return tmpl.Execute(&bytes.Buffer{}, vars)
}

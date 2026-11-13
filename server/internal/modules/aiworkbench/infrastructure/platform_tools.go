package infrastructure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
)

type PlatformMutationGate interface {
	Apply(context.Context, int, string, string, string, json.RawMessage, func(context.Context) (string, error)) (string, error)
}
type PlatformSkills interface {
	Create(context.Context, int, settings.SkillPackage) error
	Write(context.Context, int, string, settings.SkillFile) error
}
type PlatformMCP interface {
	List(context.Context, int) ([]settings.MCPServer, error)
	Save(context.Context, int, string, settings.MCPInput) (*settings.MCPServer, error)
}
type PlatformToolCatalog interface {
	List(context.Context, int) ([]settings.ToolRecord, error)
	Save(context.Context, int, string, settings.ToolRecord, bool) (settings.ToolRecord, error)
}

var platformSkillName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func strictPlatformInput(raw json.RawMessage, dest any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		return err
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return settings.ErrInvalidProfile
	}
	return nil
}

// AutoPlatformTools mirrors ARTEX's configuration tools while preserving the
// product's admin authorization, live run lease, and tool-call replay fence.
func AutoPlatformTools(gate PlatformMutationGate, skills PlatformSkills, mcp PlatformMCP, catalog func() PlatformToolCatalog, user int) []tool.CoreTool {
	if gate == nil || skills == nil || mcp == nil || catalog == nil || user <= 0 {
		return nil
	}
	property := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	object := func(props map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": required}
	}
	makeTool := func(name, description string, schema map[string]any, run func(context.Context, json.RawMessage) (string, error)) tool.CoreTool {
		return tool.Build(tool.Spec{Name: name, Description: description, Schema: schema,
			ReadOnly: func(json.RawMessage) bool { return false }, Concurrent: func(json.RawMessage) bool { return false },
			Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
				return permission.Allowed()
			},
			Run: func(ctx context.Context, raw json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
				if tc == nil || tc.ToolUseID == "" || airuntime.ExecutionID(ctx) == "" {
					return tool.Errorf("缺少有效对话运行上下文"), nil
				}
				message, err := gate.Apply(ctx, user, airuntime.ExecutionID(ctx), tc.ToolUseID, name, raw, func(inner context.Context) (string, error) { return run(inner, raw) })
				if err != nil {
					return tool.Errorf("操作失败，请检查管理员权限、运行状态、参数及现有配置"), nil
				}
				return tool.Text(message), nil
			},
		})
	}
	createSkill := makeTool("create_skill", "创建一个 Skill 包，名称使用小写字母、数字和连字符。", object(map[string]any{"name": property("Skill 名称"), "description": property("描述"), "instructions": property("Markdown 正文")}, "name", "description"), func(ctx context.Context, raw json.RawMessage) (string, error) {
		var in struct {
			Name         string `json:"name"`
			Description  string `json:"description"`
			Instructions string `json:"instructions"`
		}
		if err := strictPlatformInput(raw, &in); err != nil {
			return "", err
		}
		in.Name = strings.TrimSpace(in.Name)
		if !platformSkillName.MatchString(in.Name) {
			return "", settings.ErrInvalidProfile
		}
		if err := skills.Create(ctx, user, settings.SkillPackage{Name: in.Name, Description: in.Description, Instructions: in.Instructions}); err != nil {
			return "", err
		}
		return "skill created: " + in.Name, nil
	})
	updateSkill := makeTool("update_skill_file", "写入已有 Skill 的文件；默认写入 SKILL.md。", object(map[string]any{"name": property("Skill 名称"), "file": property("相对文件路径"), "content": property("完整文件内容")}, "name", "content"), func(ctx context.Context, raw json.RawMessage) (string, error) {
		var in struct {
			Name    string  `json:"name"`
			File    string  `json:"file"`
			Content *string `json:"content"`
		}
		if err := strictPlatformInput(raw, &in); err != nil {
			return "", err
		}
		in.Name = strings.TrimSpace(in.Name)
		in.File = strings.TrimSpace(in.File)
		if in.File == "" {
			in.File = "SKILL.md"
		}
		if !platformSkillName.MatchString(in.Name) || in.Content == nil {
			return "", settings.ErrInvalidProfile
		}
		if err := skills.Write(ctx, user, in.Name, settings.SkillFile{Path: in.File, Content: []byte(*in.Content), Mode: 0644}); err != nil {
			return "", err
		}
		return "skill file written: " + in.Name + "/" + in.File, nil
	})
	type toolInput struct {
		Key         string          `json:"key"`
		Description string          `json:"description"`
		Kind        string          `json:"kind"`
		Exec        json.RawMessage `json:"exec"`
		Schema      json.RawMessage `json:"schema"`
		Agents      []string        `json:"agents"`
		Deferred    bool            `json:"deferred"`
		Enabled     *bool           `json:"enabled"`
	}
	toolSchema := object(map[string]any{"key": property("工具 key"), "description": property("工具说明"), "kind": property("shell / command / script / http"), "exec": map[string]any{"type": "object"}, "schema": map[string]any{"type": "object"}, "agents": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "deferred": map[string]any{"type": "boolean"}, "enabled": map[string]any{"type": "boolean"}}, "key", "kind")
	saveTool := func(create bool) func(context.Context, json.RawMessage) (string, error) {
		return func(ctx context.Context, raw json.RawMessage) (string, error) {
			var in toolInput
			if err := strictPlatformInput(raw, &in); err != nil {
				return "", err
			}
			in.Key = strings.TrimSpace(in.Key)
			service := catalog()
			if service == nil {
				return "", settings.ErrToolCatalogInvalid
			}
			if !create {
				records, err := service.List(ctx, user)
				if err != nil {
					return "", err
				}
				found := false
				for _, record := range records {
					if record.Key == in.Key && !record.System {
						found = true
						break
					}
				}
				if !found {
					return "", settings.ErrToolCatalogNotFound
				}
			}
			enabled := true
			if in.Enabled != nil {
				enabled = *in.Enabled
			}
			item := settings.ToolRecord{Key: in.Key, Description: in.Description, Kind: in.Kind, Exec: in.Exec, Schema: in.Schema, Agents: in.Agents, Deferred: in.Deferred, Enabled: enabled}
			if _, err := service.Save(ctx, user, in.Key, item, create); err != nil {
				return "", err
			}
			verb := "updated"
			if create {
				verb = "created"
			}
			return "custom tool " + verb + ": " + in.Key, nil
		}
	}
	createTool := makeTool("create_custom_tool", "创建自定义 shell、command、script 或 HTTP 工具。", toolSchema, saveTool(true))
	updateTool := makeTool("update_custom_tool", "修改已有自定义工具，不修改内置工具。", toolSchema, saveTool(false))
	type mcpInput struct {
		ID        string             `json:"id"`
		Name      string             `json:"name"`
		Transport string             `json:"transport"`
		Command   string             `json:"command"`
		Args      []string           `json:"args"`
		Env       *map[string]string `json:"env"`
		URL       string             `json:"url"`
		Enabled   *bool              `json:"enabled"`
		Insecure  *bool              `json:"insecure"`
	}
	mcpProps := func(withID bool) map[string]any {
		props := map[string]any{"name": property("MCP 名称"), "transport": property("stdio / http / sse"), "command": property("stdio 命令"), "args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "env": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}, "url": property("HTTP/SSE 地址"), "enabled": map[string]any{"type": "boolean"}, "insecure": map[string]any{"type": "boolean"}}
		if withID {
			props["id"] = property("MCP ID")
		}
		return props
	}
	saveMCP := func(create bool) func(context.Context, json.RawMessage) (string, error) {
		return func(ctx context.Context, raw json.RawMessage) (string, error) {
			var in mcpInput
			if err := strictPlatformInput(raw, &in); err != nil {
				return "", err
			}
			if create && in.ID != "" || !create && in.ID == "" {
				return "", settings.ErrInvalidProfile
			}
			enabled := true
			if in.Enabled != nil {
				enabled = *in.Enabled
			}
			insecure := false
			if in.Insecure != nil {
				insecure = *in.Insecure
			}
			env := map[string]string{}
			if in.Env != nil {
				env = *in.Env
			}
			clearHeaders, transportChanged := false, false
			if !create {
				servers, err := mcp.List(ctx, user)
				if err != nil {
					return "", err
				}
				var current *settings.MCPServer
				for i := range servers {
					if servers[i].ID == in.ID {
						current = &servers[i]
						break
					}
				}
				if current == nil {
					return "", settings.ErrProfileNotFound
				}
				transportChanged = current.Transport != in.Transport
				destinationChanged := transportChanged || current.Endpoint != in.URL || current.Command != in.Command || !slices.Equal(current.Args, in.Args) || current.Insecure != insecure
				if destinationChanged && (current.HasEnv || current.HasHeaders) && in.Env == nil {
					return "", settings.ErrInvalidProfile
				}
				clearHeaders = current.Transport != "stdio" && in.Transport == "stdio"
			}
			item, err := mcp.Save(ctx, user, in.ID, settings.MCPInput{Name: in.Name, Transport: in.Transport, Command: in.Command, Args: in.Args, Env: env, ReplaceEnv: in.Env != nil || transportChanged, ClearHeaders: clearHeaders, PreserveAgents: !create, Endpoint: in.URL, Enabled: enabled, Insecure: insecure})
			if err != nil {
				return "", err
			}
			verb := "updated"
			if create {
				verb = "created"
			}
			return fmt.Sprintf("mcp %s: id=%s name=%s", verb, item.ID, item.Name), nil
		}
	}
	createMCP := makeTool("create_mcp", "创建 MCP 服务器，创建后需为 Agent 配置可见性。", object(mcpProps(false), "name", "transport"), saveMCP(true))
	updateMCP := makeTool("update_mcp", "修改已有 MCP 服务器并保留现有 Agent 可见性。", object(mcpProps(true), "id", "name", "transport"), saveMCP(false))
	return []tool.CoreTool{createSkill, updateSkill, createTool, updateTool, createMCP, updateMCP}
}

package infrastructure

import (
	"context"
	"encoding/json"

	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
)

type MemberProfileReader interface {
	ListForMember(context.Context, int) ([]settings.LLMProfile, error)
}

// AutoLLMProfileTools exposes only the fields needed to choose a task model;
// endpoint URLs, credentials, proxy settings, and key hints stay out of the
// model transcript.
func AutoLLMProfileTools(reader MemberProfileReader, user int) []tool.CoreTool {
	if reader == nil || user <= 0 {
		return nil
	}
	return []tool.CoreTool{tool.Build(tool.Spec{
		Name: "list_llm_profiles", Description: "列出可用模型配置的 ID、名称、模型、协议和默认状态，可将 ID 传给 spawn_task。不会返回密钥。",
		Schema:   map[string]any{"type": "object", "properties": map[string]any{}},
		ReadOnly: func(json.RawMessage) bool { return true }, Concurrent: func(json.RawMessage) bool { return true },
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(ctx context.Context, raw json.RawMessage, _ *tool.ToolContext) (tool.Result, error) {
			var in struct{}
			if err := strictPlatformInput(raw, &in); err != nil {
				return tool.Errorf("查询参数无效"), nil
			}
			items, err := reader.ListForMember(ctx, user)
			if err != nil {
				return tool.Errorf("无法读取可用模型配置"), nil
			}
			profiles := make([]map[string]any, 0, len(items))
			for _, item := range items {
				profiles = append(profiles, map[string]any{"id": item.ID, "name": item.Name, "model": item.Model, "format": item.Provider, "is_active": item.IsDefault})
			}
			output, err := json.Marshal(map[string]any{"profiles": profiles})
			if err != nil {
				return tool.Errorf("无法编码模型配置"), nil
			}
			return tool.Text(string(output)), nil
		},
	})}
}

package airuntime

import (
	"context"
	"encoding/json"

	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

func AssetToolResolver(reader *assetapp.Facade, writers ...*assetapp.RegistrationFacade) func(int) []tool.CoreTool {
	return func(userID int) []tool.CoreTool {
		tools := AssetTools(reader, userID)
		if len(writers) > 0 && writers[0] != nil {
			tools = append(tools, insertAssetsTool(writers[0], userID))
		}
		return tools
	}
}

// AssetTools returns read-only platform tools bound to the authenticated user.
func AssetTools(reader *assetapp.Facade, userID int) []tool.CoreTool {
	return []tool.CoreTool{tool.Build(tool.Spec{
		Name:        "search_platform_assets",
		Description: "搜索当前账号可访问的天穹资产，返回资产标识、类型、名称和来源数量。",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "资产名称或 URL 中包含的文本"},
				"type":  map[string]any{"type": "string", "enum": []string{"root_domain", "subdomain", "ip", "app", "service", "endpoint"}},
			},
		},
		ReadOnly:   func(json.RawMessage) bool { return true },
		Concurrent: func(json.RawMessage) bool { return true },
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(ctx context.Context, input json.RawMessage, _ *tool.ToolContext) (tool.Result, error) {
			var request struct {
				Query string `json:"query"`
				Type  string `json:"type"`
			}
			if err := json.Unmarshal(input, &request); err != nil {
				return tool.Errorf("资产查询参数无效"), nil
			}
			result, err := reader.List(ctx, assetapp.ListInput{UserID: userID, Query: request.Query, AssetType: request.Type, PageSize: 25, TargetID: taskTarget(ctx)})
			if err != nil {
				return tool.Errorf("资产查询失败，请检查权限和查询条件"), nil
			}
			data, err := json.Marshal(result.Items)
			if err != nil {
				return tool.Errorf("资产结果编码失败"), nil
			}
			return tool.Text(string(data)), nil
		},
	})}
}

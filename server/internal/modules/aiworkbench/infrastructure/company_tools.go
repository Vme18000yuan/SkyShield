package infrastructure

import (
	"context"
	"encoding/json"

	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

type AgentCompanyReader interface {
	AgentCompanies(context.Context, int, int64, string, string) (assets.AgentCompanyPage, error)
}

func CompanyTools(reader AgentCompanyReader, user int) []tool.CoreTool {
	if reader == nil || user <= 0 {
		return nil
	}
	return []tool.CoreTool{tool.Build(tool.Spec{
		Name: "list_companies", Description: "列出当前账号可访问的组织、目标范围和资产数；关联目标的任务只查看该目标范围。",
		Schema:   map[string]any{"type": "object", "properties": map[string]any{"search": map[string]any{"type": "string", "description": "组织名称关键词"}}},
		ReadOnly: func(json.RawMessage) bool { return true }, Concurrent: func(json.RawMessage) bool { return true }, Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(ctx context.Context, raw json.RawMessage, _ *tool.ToolContext) (tool.Result, error) {
			var input struct {
				Search string `json:"search"`
			}
			if err := strictPlatformInput(raw, &input); err != nil {
				return tool.Errorf("组织查询参数无效"), nil
			}
			taskID := int64(0)
			if task, ok := airuntime.TaskExecutionFromContext(ctx); ok {
				if task.UserID != user || task.RunID != airuntime.ExecutionID(ctx) {
					return tool.Errorf("任务运行上下文无效"), nil
				}
				taskID = task.ID
			}
			result, err := reader.AgentCompanies(ctx, user, taskID, airuntime.ExecutionID(ctx), input.Search)
			if err != nil {
				return tool.Errorf("无法读取组织，请检查运行状态和权限"), nil
			}
			rawResult, err := json.Marshal(result)
			if err != nil {
				return tool.Errorf("无法编码组织数据"), nil
			}
			return tool.Text(string(rawResult)), nil
		},
	})}
}

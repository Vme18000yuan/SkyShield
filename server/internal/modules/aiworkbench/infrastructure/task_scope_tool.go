package infrastructure

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
)

type TaskScopeWriter interface {
	AddAgentScope(context.Context, int, int64, string, aiapp.TaskScopeInput) (aiapp.TaskScope, error)
}

func TaskScopeTools(writer TaskScopeWriter, user int) []tool.CoreTool {
	if writer == nil || user <= 0 {
		return nil
	}
	return []tool.CoreTool{tool.Build(tool.Spec{
		Name: "add_task_scope", Description: "把授权范围加入当前任务。支持 company、root_domain、subdomain、ip、cidr、icp、keyword；填写 reason 说明依据，可用 entries 批量提交。",
		Schema: map[string]any{"type": "object", "properties": map[string]any{
			"entries": map[string]any{"type": "array", "items": map[string]any{"type": "object"}, "maxItems": 100},
			"kind":    map[string]any{"type": "string"}, "value": map[string]any{"type": "string"}, "reason": map[string]any{"type": "string"},
		}},
		ReadOnly: func(json.RawMessage) bool { return false }, Concurrent: func(json.RawMessage) bool { return false },
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(ctx context.Context, raw json.RawMessage, _ *tool.ToolContext) (tool.Result, error) {
			info := agent.RunInfoFrom(ctx)
			if info.TaskID <= 0 || airuntime.ExecutionID(ctx) == "" {
				return tool.Errorf("add_task_scope 需要有效的任务运行上下文"), nil
			}
			var input struct {
				Entries []aiapp.TaskScopeInput `json:"entries"`
				Kind    string                 `json:"kind"`
				Value   string                 `json:"value"`
				Reason  string                 `json:"reason"`
			}
			if err := json.Unmarshal(raw, &input); err != nil || len(input.Entries) > 100 {
				return tool.Errorf("任务范围参数无效"), nil
			}
			if len(input.Entries) == 0 {
				input.Entries = []aiapp.TaskScopeInput{{Kind: input.Kind, Value: input.Value}}
			}
			added := []aiapp.TaskScope{}
			errs := map[string]string{}
			for i, entry := range input.Entries {
				entry.Reason = input.Reason
				item, err := writer.AddAgentScope(ctx, user, info.TaskID, airuntime.ExecutionID(ctx), entry)
				if err != nil {
					errs[strconv.Itoa(i)] = "范围无效、不可访问或运行已结束"
					continue
				}
				added = append(added, item)
			}
			out := map[string]any{"added": added}
			if len(errs) > 0 {
				out["errors"] = errs
			}
			encoded, err := json.Marshal(out)
			if err != nil {
				return tool.Errorf("任务范围结果编码失败"), nil
			}
			return tool.Text(string(encoded)), nil
		},
	})}
}

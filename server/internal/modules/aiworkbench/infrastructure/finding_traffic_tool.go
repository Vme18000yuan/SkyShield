package infrastructure

import (
	"context"
	"encoding/json"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
)

type FindingTrafficBinder interface {
	FindingTraffic(context.Context, int, int64) (aiapp.FindingTrafficRecord, error)
	BindReporter(context.Context, int, int64, int64, string, []artexdb.TrafficRef) (*artexdb.FindingTraffic, error)
}

func FindingTrafficTools(access FindingTrafficBinder, user int) []tool.CoreTool {
	if access == nil || user <= 0 {
		return nil
	}
	return []tool.CoreTool{tool.Build(tool.Spec{
		Name: "bind_finding_traffic", Description: "将已逐条核实的真实流量按顺序关联到本次触发的漏洞。finding_id 是独立漏洞记录 ID；只能绑定当前报告任务的流量。",
		Schema:   map[string]any{"type": "object", "properties": map[string]any{"finding_id": map[string]any{"type": "string"}, "traffic_refs": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"traffic_id": map[string]any{"type": "string"}, "role": map[string]any{"type": "string", "enum": []string{"baseline", "proof", "verification", "supporting"}}, "note": map[string]any{"type": "string"}}, "required": []string{"traffic_id"}}}}, "required": []string{"finding_id", "traffic_refs"}},
		ReadOnly: func(json.RawMessage) bool { return false }, Concurrent: func(json.RawMessage) bool { return false }, Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(ctx context.Context, raw json.RawMessage, _ *tool.ToolContext) (tool.Result, error) {
			var input struct {
				FindingID json.RawMessage      `json:"finding_id"`
				Refs      []artexdb.TrafficRef `json:"traffic_refs"`
			}
			if json.Unmarshal(raw, &input) != nil {
				return tool.Errorf("证据参数无效"), nil
			}
			id, err := parseAutoTaskID(input.FindingID)
			if err != nil {
				return tool.Errorf("漏洞编号无效"), nil
			}
			record, err := access.FindingTraffic(ctx, user, id)
			if err != nil {
				return tool.Errorf("漏洞不可访问"), nil
			}
			output, err := access.BindReporter(ctx, user, record.TaskID, id, airuntime.ExecutionID(ctx), input.Refs)
			if err != nil {
				return tool.Errorf("关联失败，请核对当前任务、流量和运行状态"), nil
			}
			encoded, err := json.Marshal(output)
			if err != nil {
				return tool.Errorf("关联结果编码失败"), nil
			}
			return tool.Text(string(encoded)), nil
		},
	})}
}

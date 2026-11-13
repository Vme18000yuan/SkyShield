package infrastructure

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
)

type FindingRetestAccess interface {
	RetestContext(context.Context, int, string) (*aiapp.FindingRetestContext, error)
	SaveRetestResult(context.Context, int, string, string, string, string) error
}

func FindingRetestTools(access FindingRetestAccess, user int) []tool.CoreTool {
	if access == nil || user <= 0 {
		return nil
	}
	allowed := func(context.Context, json.RawMessage, permission.Context) permission.Decision {
		return permission.Allowed()
	}
	return []tool.CoreTool{
		tool.Build(tool.Spec{Name: "get_finding_retest_context", Description: "读取当前复测会话关联的漏洞原始证据快照、资产、任务约束和补充说明。无参数，只能读取本次会话。", Schema: map[string]any{"type": "object", "properties": map[string]any{}}, ReadOnly: func(json.RawMessage) bool { return true }, Concurrent: func(json.RawMessage) bool { return true }, Permissions: allowed, Run: func(ctx context.Context, _ json.RawMessage, _ *tool.ToolContext) (tool.Result, error) {
			item, err := access.RetestContext(ctx, user, airuntime.ExecutionID(ctx))
			if err != nil {
				return tool.Errorf("当前会话未关联正在运行的复测，请从漏洞详情发起"), nil
			}
			raw, err := json.Marshal(item)
			if err != nil {
				return tool.Errorf("复测上下文编码失败"), nil
			}
			return tool.Text(string(raw)), nil
		}}),
		tool.Build(tool.Spec{Name: "record_finding_retest_result", Description: "为当前复测保存唯一结论。verdict 为 reproduced、fixed 或 inconclusive；必须提供本次实际检查的摘要和证据。成功完成且结论为 fixed 时系统自动标记原漏洞已修复。", Schema: map[string]any{"type": "object", "properties": map[string]any{"verdict": map[string]any{"type": "string", "enum": []string{"reproduced", "fixed", "inconclusive"}}, "summary": map[string]any{"type": "string"}, "evidence": map[string]any{"type": "string"}}, "required": []string{"verdict", "summary", "evidence"}}, ReadOnly: func(json.RawMessage) bool { return false }, Concurrent: func(json.RawMessage) bool { return false }, Permissions: allowed, Run: func(ctx context.Context, raw json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
			if tc == nil || tc.ToolUseID == "" {
				return tool.Errorf("缺少有效复测运行上下文"), nil
			}
			var request struct {
				Verdict  string `json:"verdict"`
				Summary  string `json:"summary"`
				Evidence string `json:"evidence"`
			}
			if json.Unmarshal(raw, &request) != nil {
				return tool.Errorf("复测结果参数无效"), nil
			}
			err := access.SaveRetestResult(ctx, user, airuntime.ExecutionID(ctx), request.Verdict, request.Summary, request.Evidence)
			if errors.Is(err, aiapp.ErrRetestNotRunning) {
				return tool.Errorf("复测未运行或已经保存其他结论"), nil
			}
			if err != nil {
				return tool.Errorf("复测结果保存失败，请检查参数与运行权限"), nil
			}
			return tool.Text(`{"saved":true,"verdict":"` + request.Verdict + `"}`), nil
		}}),
	}
}

package aivuln

import (
	"context"
	"encoding/json"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
)

func (s *Service) Tool(user int) tool.CoreTool {
	text := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	return tool.Build(tool.Spec{Name: "report_vulnerability", Description: "独立 AI 对话将有证据的发现同步到漏洞管理，返回 vulnerability_id 才表示入库成功。必须在最终报告前逐项调用；人工复核状态保持待复核。同类重试幂等。任务图请使用 report_finding。", Schema: map[string]any{"type": "object", "properties": map[string]any{
		"target_id": map[string]any{"type": "integer", "description": "已有目标 ID；省略时 URL 必须唯一匹配已有目标。"},
		"url":       text("实际受影响的 HTTP(S) URL"), "vuln_type": text("稳定的漏洞类型名称，不包含待复核等状态"), "severity": map[string]any{"type": "string", "enum": []string{"unknown", "info", "low", "medium", "high", "critical"}}, "summary": text("发现、影响、复现条件及限制"), "evidence": text("已观察到的证据，区分确认事实与待核实推测"),
		"evidence_paths": map[string]any{"type": "array", "maxItems": 8, "items": text("当前会话工作目录内的相对路径；系统保存证据快照，单文件最多展示 128 KiB")},
		"traffic_ids":    map[string]any{"type": "array", "maxItems": 8, "items": text("当前会话已采集的 HTTP 流量 ID，优先使用")},
		"request":        text("实际采集的原始 HTTP 请求；未采集则留空，不得推测补写"), "response": text("实际采集的原始 HTTP 响应；未采集则留空，不得推测补写"),
	}, "required": []string{"url", "vuln_type", "severity", "summary", "evidence"}, "additionalProperties": false}, ReadOnly: func(json.RawMessage) bool { return false }, Concurrent: func(json.RawMessage) bool { return false }, Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
		return permission.Allowed()
	}, Run: func(ctx context.Context, raw json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
		var input Report
		if len(raw) > 1<<20 || json.Unmarshal(raw, &input) != nil || tc == nil {
			return tool.Errorf("漏洞报告参数无效"), nil
		}
		result, err := s.Register(ctx, user, airuntime.ExecutionID(ctx), tc.ToolUseID, input)
		if err != nil {
			return tool.Errorf("漏洞未入库：" + err.Error()), nil
		}
		body, _ := json.Marshal(result)
		return tool.Text(string(body)), nil
	}})
}

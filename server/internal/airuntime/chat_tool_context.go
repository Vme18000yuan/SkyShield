package airuntime

import "github.com/Autumn-27/norma/tool"

// Only graph-backed handlers from the task runner may implement these names.
// Independent chats retain the explicit-task-ID platform read/handoff tools.
func independentChatTools(items []tool.CoreTool) []tool.CoreTool {
	result := make([]tool.CoreTool, 0, len(items))
	for _, item := range items {
		if item != nil && !RequiresTaskContext(item.Name()) {
			result = append(result, item)
		}
	}
	return result
}

func independentChatGuidance(items []tool.CoreTool) string {
	names := map[string]bool{}
	for _, item := range items {
		names[item.Name()] = true
	}
	guidance := "\n\n【独立对话上下文】本会话未绑定 AI 任务或探索图，report_finding、list_findings 等任务内工具不可用。不能猜测任务 ID，也不能把文字总结当作已登记的漏洞。"
	if names["report_vulnerability"] {
		guidance += "发现有证据支持的漏洞或风险后，必须在最终报告前逐项调用 report_vulnerability，携带实际 URL、类型、严重程度及证据；优先绑定当前会话采集的 traffic_ids，或提交 evidence_paths 保存证据文件快照。只有返回 vulnerability_id 才表示已同步到漏洞管理；失败须明确说明并修正参数重试。未采集的 HTTP 请求或响应不得推测补写。"
	}
	if names["list_task_findings"] {
		guidance += "查询已有任务的发现时，使用 list_task_findings 并提供明确的 task_id。"
	}
	if names["add_task_hint"] {
		guidance += "将已有证据交接给任务时，可对明确归属的任务调用 add_task_hint，由任务 Agent 核实登记。"
	}
	return guidance
}

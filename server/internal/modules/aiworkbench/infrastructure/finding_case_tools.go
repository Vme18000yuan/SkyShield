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

type ReporterFindingCaseAccess interface {
	ReporterFindingDuplicates(context.Context, int, string, int64, int, int) (aiapp.FindingDuplicatePage, error)
	ReporterFindingRecord(context.Context, int, string, int64) (*aiapp.TaskFinding, error)
	ReporterFindingCase(context.Context, int, string, int64) (*aiapp.TaskFindingCase, error)
	SaveReporterFindingCaseReport(context.Context, int, string, string, int64, aiapp.FindingCaseReport) (bool, error)
	ReporterSuggestFindingCase(context.Context, int, string, string, []int64, string, string) (int64, bool, error)
	ReporterMergeFindingCase(context.Context, int, string, string, int64, []int64, string, string) (int64, bool, error)
	CompleteCaseReview(context.Context, int, string, string, aiapp.FindingCaseReviewConclusion) (bool, error)
}

// ReporterFindingCaseTools expose only read operations and resolve the task
// from the leased Reporter job. The Agent cannot choose another task.
func ReporterFindingCaseTools(access ReporterFindingCaseAccess, user int) []tool.CoreTool {
	if access == nil || user <= 0 {
		return nil
	}
	allow := func(context.Context, json.RawMessage, permission.Context) permission.Decision {
		return permission.Allowed()
	}
	idSchema := map[string]any{"type": "integer", "minimum": 1, "description": "独立漏洞记录 ID，不是探索节点 ID"}
	pageSchema := map[string]any{"type": "integer", "minimum": 1, "maximum": 10001, "description": "页码，从 1 开始，每页 20 条"}
	return []tool.CoreTool{
		tool.Build(tool.Spec{
			Name: "search_finding_duplicates", Description: "只读检索本次 Reporter 来源任务中的相似发现。strong 为同类型且资产重叠，weak 为较弱线索；均须核对实际根因，人工否决的组合不会返回。",
			Schema:   map[string]any{"type": "object", "properties": map[string]any{"finding_id": idSchema, "page": pageSchema}, "required": []string{"finding_id"}},
			ReadOnly: func(json.RawMessage) bool { return true }, Concurrent: func(json.RawMessage) bool { return true }, Permissions: allow,
			Run: func(ctx context.Context, raw json.RawMessage, _ *tool.ToolContext) (tool.Result, error) {
				var req struct {
					FindingID json.RawMessage `json:"finding_id"`
					Page      int             `json:"page"`
				}
				if json.Unmarshal(raw, &req) != nil {
					return tool.Errorf("相似发现参数无效"), nil
				}
				id, err := parseAutoTaskID(req.FindingID)
				if err != nil || req.Page < 0 || req.Page > 10001 {
					return tool.Errorf("相似发现参数无效"), nil
				}
				page := req.Page
				if page == 0 {
					page = 1
				}
				result, err := access.ReporterFindingDuplicates(ctx, user, airuntime.ExecutionID(ctx), id, page-1, 20)
				if err != nil {
					return tool.Errorf("无法读取相似发现，请检查来源任务及运行状态"), nil
				}
				encoded, err := json.Marshal(map[string]any{"items": result.Items, "total": result.Total, "strong_total": result.StrongTotal, "page": page, "page_size": result.Size, "note": "候选仅供核对，不能仅凭类型、名称或资产认定同一根因。"})
				if err != nil {
					return tool.Errorf("相似发现结果编码失败"), nil
				}
				return tool.Text(string(encoded)), nil
			},
		}),
		tool.Build(tool.Spec{
			Name: "get_finding_record", Description: "读取本次 Reporter 来源任务中独立漏洞的原始证据、报告及探索节点编号。",
			Schema:   map[string]any{"type": "object", "properties": map[string]any{"finding_id": idSchema}, "required": []string{"finding_id"}},
			ReadOnly: func(json.RawMessage) bool { return true }, Concurrent: func(json.RawMessage) bool { return true }, Permissions: allow,
			Run: func(ctx context.Context, raw json.RawMessage, _ *tool.ToolContext) (tool.Result, error) {
				var req struct {
					FindingID json.RawMessage `json:"finding_id"`
				}
				if json.Unmarshal(raw, &req) != nil {
					return tool.Errorf("漏洞参数无效"), nil
				}
				id, err := parseAutoTaskID(req.FindingID)
				if err != nil {
					return tool.Errorf("漏洞编号无效"), nil
				}
				item, err := access.ReporterFindingRecord(ctx, user, airuntime.ExecutionID(ctx), id)
				if err != nil || item == nil {
					return tool.Errorf("无法读取漏洞，请检查来源任务及运行状态"), nil
				}
				encoded, err := json.Marshal(map[string]any{"finding": item, "finding_node_id": item.NodeID})
				if err != nil {
					return tool.Errorf("漏洞结果编码失败"), nil
				}
				return tool.Text(string(encoded)), nil
			},
		}),
		tool.Build(tool.Spec{
			Name: "get_finding_case", Description: "读取本次 Reporter 来源任务的归并组、成员和版本；需要完整原始证据时逐条调用 get_finding_record。",
			Schema:   map[string]any{"type": "object", "properties": map[string]any{"case_id": map[string]any{"type": "integer", "minimum": 1}}, "required": []string{"case_id"}},
			ReadOnly: func(json.RawMessage) bool { return true }, Concurrent: func(json.RawMessage) bool { return true }, Permissions: allow,
			Run: func(ctx context.Context, raw json.RawMessage, _ *tool.ToolContext) (tool.Result, error) {
				var req struct {
					CaseID json.RawMessage `json:"case_id"`
				}
				if json.Unmarshal(raw, &req) != nil {
					return tool.Errorf("归并组参数无效"), nil
				}
				id, err := parseAutoTaskID(req.CaseID)
				if err != nil {
					return tool.Errorf("归并组编号无效"), nil
				}
				item, err := access.ReporterFindingCase(ctx, user, airuntime.ExecutionID(ctx), id)
				if err != nil || item == nil {
					return tool.Errorf("无法读取归并组，请检查来源任务及运行状态"), nil
				}
				encoded, err := json.Marshal(map[string]any{"case": item, "members": item.Members, "total": item.MemberCount})
				if err != nil {
					return tool.Errorf("归并组结果编码失败"), nil
				}
				return tool.Text(string(encoded)), nil
			},
		}),
		tool.Build(tool.Spec{
			Name: "update_finding_case_report", Description: "保存当前 Reporter 来源任务的统一报告和评级依据。version 必须来自实际读取；报告需包含概述、影响、前置条件、复现、证据、PoC、根因和修复建议。",
			Schema:   map[string]any{"type": "object", "properties": map[string]any{"case_id": map[string]any{"type": "integer", "minimum": 1}, "version": map[string]any{"type": "integer", "minimum": 1}, "title": map[string]any{"type": "string"}, "severity": map[string]any{"type": "string", "enum": []string{"critical", "high", "medium", "low"}}, "severity_reason": map[string]any{"type": "string"}, "report": map[string]any{"type": "string"}}, "required": []string{"case_id", "version", "title", "severity", "severity_reason", "report"}},
			ReadOnly: func(json.RawMessage) bool { return false }, Concurrent: func(json.RawMessage) bool { return false }, Permissions: allow,
			Run: func(ctx context.Context, raw json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
				if tc == nil || tc.ToolUseID == "" {
					return tool.Errorf("缺少有效工具调用上下文"), nil
				}
				var req struct {
					CaseID         json.RawMessage `json:"case_id"`
					Version        int64           `json:"version"`
					Title          string          `json:"title"`
					Severity       string          `json:"severity"`
					SeverityReason string          `json:"severity_reason"`
					Report         string          `json:"report"`
				}
				if json.Unmarshal(raw, &req) != nil {
					return tool.Errorf("统一报告参数无效"), nil
				}
				id, err := parseAutoTaskID(req.CaseID)
				if err != nil {
					return tool.Errorf("归并组编号无效"), nil
				}
				input := aiapp.FindingCaseReport{Version: req.Version, Title: req.Title, Severity: req.Severity, SeverityReason: req.SeverityReason, Report: req.Report}
				replayed, err := access.SaveReporterFindingCaseReport(ctx, user, airuntime.ExecutionID(ctx), tc.ToolUseID, id, input)
				if errors.Is(err, aiapp.ErrIncompleteFindingCaseReport) {
					return tool.Errorf(err.Error()), nil
				}
				if errors.Is(err, aiapp.ErrTaskControlConflict) {
					return tool.Errorf("归并组已变化，请重新读取成员和版本后生成报告"), nil
				}
				if err != nil {
					return tool.Errorf("统一报告保存失败，请检查参数及运行权限"), nil
				}
				encoded, _ := json.Marshal(map[string]bool{"ok": true, "replayed": replayed})
				return tool.Text(string(encoded)), nil
			},
		}),
		tool.Build(tool.Spec{
			Name: "suggest_finding_merge", Description: "当证据存在具体共同点但缺少确认根因的关键材料时，为同一来源任务的 2 至 20 条发现提交一个待人工确认的建议；仅类型或资产相同不足以提出建议。",
			Schema:   map[string]any{"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "integer", "minimum": 1}, "finding_ids": map[string]any{"type": "array", "items": idSchema, "minItems": 2, "maxItems": 20}, "title": map[string]any{"type": "string"}, "reason": map[string]any{"type": "string"}}, "required": []string{"task_id", "finding_ids", "title", "reason"}},
			ReadOnly: func(json.RawMessage) bool { return false }, Concurrent: func(json.RawMessage) bool { return false }, Permissions: allow,
			Run: func(ctx context.Context, raw json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
				if tc == nil || tc.ToolUseID == "" {
					return tool.Errorf("缺少有效工具调用上下文"), nil
				}
				var req struct {
					TaskID     json.RawMessage   `json:"task_id"`
					FindingIDs []json.RawMessage `json:"finding_ids"`
					Title      string            `json:"title"`
					Reason     string            `json:"reason"`
				}
				if json.Unmarshal(raw, &req) != nil || len(req.FindingIDs) < 2 || len(req.FindingIDs) > 20 {
					return tool.Errorf("建议需要 2 至 20 条不同的发现"), nil
				}
				taskID, err := parseAutoTaskID(req.TaskID)
				if err != nil {
					return tool.Errorf("来源任务编号无效"), nil
				}
				ids := make([]int64, 0, len(req.FindingIDs))
				for _, value := range req.FindingIDs {
					id, parseErr := parseAutoTaskID(value)
					if parseErr != nil {
						return tool.Errorf("发现编号无效"), nil
					}
					ids = append(ids, id)
				}
				run := airuntime.ExecutionID(ctx)
				finding, err := access.ReporterFindingRecord(ctx, user, run, ids[0])
				if err != nil || finding == nil || finding.TaskID != taskID {
					return tool.Errorf("建议只能针对当前 Reporter 来源任务"), nil
				}
				id, replayed, err := access.ReporterSuggestFindingCase(ctx, user, run, tc.ToolUseID, ids, req.Title, req.Reason)
				if errors.Is(err, aiapp.ErrTaskControlConflict) {
					return tool.Errorf("这些发现已在同组、被占用或被人工否决，请重新核对"), nil
				}
				if err != nil {
					return tool.Errorf("建议保存失败，请检查发现、理由及运行权限"), nil
				}
				encoded, _ := json.Marshal(map[string]any{"suggestion_id": id, "finding_ids": ids, "replayed": replayed})
				return tool.Text(string(encoded)), nil
			},
		}),
		tool.Build(tool.Spec{
			Name: "merge_finding_records", Description: "仅当证据明确证明同一实际根因时归并同一来源任务的发现；保留所有原始记录与报告，不能跨任务或绕过人工否决。",
			Schema:   map[string]any{"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "integer", "minimum": 1}, "finding_ids": map[string]any{"type": "array", "items": idSchema, "minItems": 2, "maxItems": 100}, "title": map[string]any{"type": "string"}, "reason": map[string]any{"type": "string"}}, "required": []string{"task_id", "finding_ids", "title", "reason"}},
			ReadOnly: func(json.RawMessage) bool { return false }, Concurrent: func(json.RawMessage) bool { return false }, Permissions: allow,
			Run: func(ctx context.Context, raw json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
				if tc == nil || tc.ToolUseID == "" {
					return tool.Errorf("缺少有效工具调用上下文"), nil
				}
				var req struct {
					TaskID     json.RawMessage   `json:"task_id"`
					FindingIDs []json.RawMessage `json:"finding_ids"`
					Title      string            `json:"title"`
					Reason     string            `json:"reason"`
				}
				if json.Unmarshal(raw, &req) != nil || len(req.FindingIDs) < 2 || len(req.FindingIDs) > 100 {
					return tool.Errorf("归并需要 2 至 100 条发现"), nil
				}
				taskID, err := parseAutoTaskID(req.TaskID)
				if err != nil {
					return tool.Errorf("来源任务编号无效"), nil
				}
				ids := make([]int64, 0, len(req.FindingIDs))
				for _, value := range req.FindingIDs {
					id, parseErr := parseAutoTaskID(value)
					if parseErr != nil {
						return tool.Errorf("发现编号无效"), nil
					}
					ids = append(ids, id)
				}
				id, replayed, err := access.ReporterMergeFindingCase(ctx, user, airuntime.ExecutionID(ctx), tc.ToolUseID, taskID, ids, req.Title, req.Reason)
				if errors.Is(err, aiapp.ErrTaskControlConflict) {
					return tool.Errorf("发现已被占用或包含人工否决的组合，请重新核对"), nil
				}
				if err != nil {
					return tool.Errorf("归并失败，请检查发现、理由及来源任务权限"), nil
				}
				encoded, _ := json.Marshal(map[string]any{"case_id": id, "replayed": replayed})
				return tool.Text(string(encoded)), nil
			},
		}),
		tool.Build(tool.Spec{
			Name: "complete_finding_case_review", Description: "历史整理结束前提交每条所选发现的判断并验收；必须先读取全部所选记录，merged 须已归并，suggested 须有待确认建议，independent 不得已在归并组中。",
			Schema:   map[string]any{"type": "object", "properties": map[string]any{"summary": map[string]any{"type": "string"}, "decisions": map[string]any{"type": "array", "minItems": 1, "maxItems": 200, "items": map[string]any{"type": "object", "properties": map[string]any{"finding_id": idSchema, "verdict": map[string]any{"type": "string", "enum": []string{"independent", "merged", "suggested"}}, "reason": map[string]any{"type": "string"}, "root_cause_evidence": map[string]any{"type": "string"}}, "required": []string{"finding_id", "verdict", "reason", "root_cause_evidence"}}}}, "required": []string{"summary", "decisions"}},
			ReadOnly: func(json.RawMessage) bool { return false }, Concurrent: func(json.RawMessage) bool { return false }, Permissions: allow,
			Run: func(ctx context.Context, raw json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
				if tc == nil || tc.ToolUseID == "" {
					return tool.Errorf("缺少有效工具调用上下文"), nil
				}
				var req struct {
					Summary   string `json:"summary"`
					Decisions []struct {
						FindingID         json.RawMessage `json:"finding_id"`
						Verdict           string          `json:"verdict"`
						Reason            string          `json:"reason"`
						RootCauseEvidence string          `json:"root_cause_evidence"`
					} `json:"decisions"`
				}
				if json.Unmarshal(raw, &req) != nil || len(req.Decisions) < 1 || len(req.Decisions) > 200 {
					return tool.Errorf("审查结论参数无效"), nil
				}
				conclusion := aiapp.FindingCaseReviewConclusion{Summary: req.Summary, Decisions: make([]aiapp.FindingCaseReviewDecision, 0, len(req.Decisions))}
				for _, item := range req.Decisions {
					id, err := parseAutoTaskID(item.FindingID)
					if err != nil {
						return tool.Errorf("发现编号无效"), nil
					}
					conclusion.Decisions = append(conclusion.Decisions, aiapp.FindingCaseReviewDecision{FindingID: id, Verdict: item.Verdict, Reason: item.Reason, RootCauseEvidence: item.RootCauseEvidence})
				}
				replayed, err := access.CompleteCaseReview(ctx, user, airuntime.ExecutionID(ctx), tc.ToolUseID, conclusion)
				if errors.Is(err, aiapp.ErrFindingCaseReviewConclusion) {
					return tool.Errorf("验收未通过：请先逐条读取全部所选发现，并确保每条判断与当前归并或待确认建议一致"), nil
				}
				if err != nil {
					return tool.Errorf("审查结论保存失败，请检查参数及运行权限"), nil
				}
				encoded, _ := json.Marshal(map[string]bool{"accepted": true, "replayed": replayed})
				return tool.Text(string(encoded)), nil
			},
		}),
	}
}

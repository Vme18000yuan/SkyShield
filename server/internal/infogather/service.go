package infogather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
)

type Service struct {
	store             *Store
	executor          Executor
	assetWriteAllowed func(context.Context, int) (bool, error)
}

func NewService(store *Store, executor Executor, allowed func(context.Context, int) (bool, error)) *Service {
	return &Service{store: store, executor: executor, assetWriteAllowed: allowed}
}

type ExecutionResult struct {
	Run                  Run             `json:"run"`
	Matches              []string        `json:"matchingTargets"`
	Sync                 *ImportResult   `json:"synchronization,omitempty"`
	AssetCount           int             `json:"assetCount"`
	VulnerabilityCount   int             `json:"vulnerabilityCount"`
	AssetSamples         []Asset         `json:"assetSamples,omitempty"`
	VulnerabilitySamples []Vulnerability `json:"vulnerabilitySamples,omitempty"`
	ObservationSamples   []Observation   `json:"observationSamples,omitempty"`
	Message              string          `json:"message"`
	ReviewURL            string          `json:"reviewUrl"`
}

func (s *Service) Execute(ctx context.Context, user int, execution, toolID, rawTarget, mode string, progress func(string)) (ExecutionResult, error) {
	result := ExecutionResult{Matches: []string{}, ReviewURL: "/ai/info-gather/"}
	if s == nil || s.store == nil || s.executor == nil {
		return result, errors.New("信息收集服务不可用")
	}
	target, err := parseScope(rawTarget)
	if err != nil {
		return result, err
	}
	if mode == "" {
		mode = "normal"
	}
	if mode != "fast" && mode != "normal" && mode != "deep" {
		return result, errors.New("采集模式无效")
	}
	id, created, err := s.store.start(ctx, user, execution, toolID, target, mode)
	if err != nil {
		return result, err
	}
	if !created {
		result.Run, err = s.store.Run(ctx, user, id)
		result.Message = "该工具调用已有采集记录，可在信息收集页面查看。"
		if err == nil {
			if summary := coverageSummary(result.Run.Coverage); summary != "" {
				result.Message += " " + summary
			}
		}
		return result, err
	}
	if progress != nil {
		progress(fmt.Sprintf("信息收集已创建：%s。", id))
	}
	raw, coverage, runErr := s.executor.Execute(ctx, user, id, target, mode, progress)
	// Preserve a terminal status even when the user stops the parent conversation.
	cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if runErr != nil {
		status := "failed"
		message := "信息收集执行失败，未导入资产。"
		if ctx.Err() != nil {
			status = "interrupted"
			message = "信息收集已中断，未导入未完成的结果。"
		}
		if finishErr := s.store.finish(cleanup, user, id, status, message, nil, nil); finishErr != nil {
			return result, finishErr
		}
		result.Run, err = s.store.Run(cleanup, user, id)
		result.Message = message
		if err != nil {
			return result, err
		}
		return result, runErr
	}
	report, err := decodeEnvelope(raw, id, target, mode)
	if err != nil {
		_ = s.store.finish(cleanup, user, id, "failed", "采集结果无效，未导入资产。", nil, coverage)
		return result, err
	}
	return s.completeReport(ctx, cleanup, user, id, target, report, coverage, progress)
}

func (s *Service) completeReport(ctx, cleanup context.Context, user int, id string, target Scope, report *Envelope, coverage json.RawMessage, progress func(string)) (ExecutionResult, error) {
	result := ExecutionResult{Matches: []string{}, ReviewURL: "/ai/info-gather/"}
	var err error
	if err = s.store.finish(cleanup, user, id, report.Run.Status, "", report, coverage); err != nil {
		return result, err
	}
	result.AssetCount, result.VulnerabilityCount = len(report.Assets), len(report.Vulnerabilities)
	perType := map[string]int{}
	for _, item := range report.Assets {
		if perType[item.Type] < 5 {
			result.AssetSamples = append(result.AssetSamples, item)
			perType[item.Type]++
		}
	}
	result.VulnerabilitySamples = report.Vulnerabilities[:min(10, len(report.Vulnerabilities))]
	result.ObservationSamples = report.Observations[:min(10, len(report.Observations))]
	matches, err := s.store.findTargets(ctx, target)
	if err != nil {
		return result, err
	}
	for _, match := range matches {
		result.Matches = append(result.Matches, match.Name)
	}
	allowed := true
	if s.assetWriteAllowed != nil {
		allowed, err = s.assetWriteAllowed(ctx, user)
		if err != nil {
			return result, err
		}
	}
	if len(matches) == 1 && allowed && (report.Run.Status == "completed" || report.Run.Status == "partial") {
		if progress != nil {
			progress("采集已结束，正在校验目标并同步范围内结果。")
		}
		synced, syncErr := s.store.syncAutomatic(ctx, user, id, matches[0].ID)
		result.Sync = &synced
		if syncErr != nil {
			result.Message = "结果已保存，自动同步未完成；可在信息收集页面继续处理。"
		} else if synced.Failed > 0 {
			result.Message = fmt.Sprintf("已同步 %d 条记录，%d 条因范围或其他原因待处理；可在信息收集页面选择归属或标记处理。", synced.Imported, synced.Failed)
		} else {
			result.Message = "已同步可归集的记录，其他线索保留待确认。"
		}
	} else if !allowed {
		result.Message = "AI 资产登记已禁用，结果已保留待用户确认。"
	} else {
		result.Message = "未唯一匹配到系统目标，结果已保留；请在信息收集页面选择目标。"
	}
	result.Run, err = s.store.Run(ctx, user, id)
	if err == nil {
		if summary := coverageSummary(result.Run.Coverage); summary != "" {
			result.Message += " " + summary
		}
	}
	return result, err
}

func (s *Service) Tool(user int) tool.CoreTool {
	return tool.Build(tool.Spec{
		Name:        "run_info_gather",
		Description: "对用户明确授权的单个域名、HTTP(S) URL、IP 或 CIDR 运行 info-gath。完成后系统校验目标并自动同步范围内资产和待复核风险，返回不含 URL 查询参数的分类样例；未匹配或归属不明的记录保留供用户在信息收集页面选择目标。不能将关联线索当作新扫描范围。",
		Schema: map[string]any{"type": "object", "properties": map[string]any{
			"target": map[string]any{"type": "string", "description": "用户明确授权的目标；URL 不包含凭据或查询参数。"},
			"mode":   map[string]any{"type": "string", "enum": []string{"fast", "normal", "deep"}, "description": "fast 被动、normal 常规、deep 深入；默认 normal。"},
		}, "required": []string{"target"}, "additionalProperties": false},
		ReadOnly: func(json.RawMessage) bool { return false }, Concurrent: func(json.RawMessage) bool { return false },
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(ctx context.Context, raw json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
			var request struct {
				Target string `json:"target"`
				Mode   string `json:"mode"`
			}
			if err := json.Unmarshal(raw, &request); err != nil || tc == nil || tc.ToolUseID == "" {
				return tool.Errorf("信息收集参数无效"), nil
			}
			var emit func(string)
			if tc.Emit != nil {
				emit = func(value string) { tc.Emit(tool.ProgressInfo{Message: value}) }
			}
			result, err := s.Execute(ctx, user, airuntime.ExecutionID(ctx), tc.ToolUseID, request.Target, request.Mode, emit)
			body, _ := json.Marshal(result)
			if err != nil {
				return tool.Errorf(string(body) + "\n采集失败：" + err.Error()), nil
			}
			return tool.Text(string(body)), nil
		},
	})
}

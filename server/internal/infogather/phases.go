package infogather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
)

type phaseSession struct {
	Input           string `json:"input"`
	Kind            string `json:"kind"`
	ActiveExecution string `json:"active_execution,omitempty"`
	ResumePending   bool   `json:"resume_pending,omitempty"`
}

func phaseCoverage(raw json.RawMessage, input string, execution ...string) json.RawMessage {
	active := ""
	if len(execution) > 0 {
		active = execution[0]
	}
	return phaseCoverageState(raw, input, active, false)
}

func phaseCoverageState(raw json.RawMessage, input, execution string, pending bool) json.RawMessage {
	values := map[string]json.RawMessage{}
	_ = json.Unmarshal(raw, &values)
	if values == nil {
		values = map[string]json.RawMessage{}
	}
	state := phaseSession{Input: input, Kind: "agentic", ActiveExecution: execution, ResumePending: pending}
	session, _ := json.Marshal(state)
	values["session"] = session
	result, _ := json.Marshal(values)
	return result
}

func (s *Service) preparePhases(ctx context.Context, user int, execution, toolID, rawTarget, mode string) (Run, error) {
	if s == nil || s.store == nil {
		return Run{}, errors.New("信息收集服务不可用")
	}
	if _, ok := s.executor.(PhaseExecutor); !ok {
		return Run{}, errors.New("当前信息收集执行器不支持分阶段运行")
	}
	target, err := parseScope(rawTarget)
	if err != nil {
		return Run{}, err
	}
	if mode == "" {
		mode = "normal"
	}
	if mode != "fast" && mode != "normal" && mode != "deep" {
		return Run{}, errors.New("采集模式无效")
	}
	id, _, err := s.store.startWithCoverage(ctx, user, execution, toolID, target, mode, phaseCoverage(nil, target.Input, execution))
	if err != nil {
		return Run{}, err
	}
	run, err := s.store.Run(ctx, user, id)
	if err != nil {
		return Run{}, err
	}
	input, err := phaseTarget(run, execution)
	if err != nil || input.Input != target.Input {
		return Run{}, errors.New("该工具调用已用于其他采集目标")
	}
	return run, nil
}

func phaseTarget(run Run, execution string) (Scope, error) {
	if run.Status != "running" || activeExecution(run) != execution || execution == "" {
		return Scope{}, errors.New("采集已结束或不属于当前对话")
	}
	var values struct {
		Session phaseSession `json:"session"`
	}
	if json.Unmarshal(run.Coverage, &values) != nil || values.Session.Kind != "agentic" {
		return Scope{}, errors.New("该采集不是分阶段运行")
	}
	target, err := parseScope(values.Session.Input)
	if err != nil || target.Name != run.Target || target.Kind != run.TargetKind {
		return Scope{}, errors.New("采集范围状态无效")
	}
	return target, nil
}

// The original execution remains immutable for audit; a resumed phase uses a
// separately authorized execution from the same conversation.
func activeExecution(run Run) string {
	var value struct {
		Session phaseSession `json:"session"`
	}
	if json.Unmarshal(run.Coverage, &value) != nil || value.Session.Kind != "agentic" || value.Session.ActiveExecution == "" {
		return run.ExecutionID
	}
	parsed, err := uuid.Parse(value.Session.ActiveExecution)
	if err != nil || parsed.String() != value.Session.ActiveExecution {
		return ""
	}
	return value.Session.ActiveExecution
}

func (s *Service) phaseScope(ctx context.Context, user int, execution, id string) (Run, Scope, bool, error) {
	run, err := s.store.Run(ctx, user, id)
	if err != nil {
		return Run{}, Scope{}, false, err
	}
	if target, err := phaseTarget(run, execution); err == nil {
		var value struct {
			Session phaseSession `json:"session"`
		}
		_ = json.Unmarshal(run.Coverage, &value)
		if value.Session.ResumePending {
			if _, ok := s.executor.(ResumePhaseExecutor); !ok {
				return Run{}, Scope{}, false, errors.New("当前采集无法续接")
			}
		}
		return run, target, value.Session.ResumePending, nil
	}
	if _, ok := s.executor.(ResumePhaseExecutor); !ok {
		return Run{}, Scope{}, false, errors.New("当前采集无法续接")
	}
	run, err = s.store.resumeAgenticRun(ctx, user, execution, id)
	if err != nil {
		return Run{}, Scope{}, false, err
	}
	target, err := phaseTarget(run, execution)
	return run, target, true, err
}

type phaseResult struct {
	Message           string          `json:"message"`
	RunID             string          `json:"runId"`
	Phase             string          `json:"phase"`
	Status            string          `json:"status"`
	ArtifactDirectory string          `json:"artifactDirectory"`
	Assets            int             `json:"assetCount"`
	AssetTypes        map[string]int  `json:"assetTypes"`
	Vulnerabilities   int             `json:"vulnerabilityCount"`
	Observations      int             `json:"observationCount"`
	Samples           []string        `json:"assetSamples"`
	Coverage          json.RawMessage `json:"coverage"`
}

// Only emit validated in-scope addresses to the model, never arbitrary page
// content, response bodies, query strings, or unverified associated domains.
func phaseSamples(report *Envelope, target Scope) []string {
	result := []string{}
	for _, asset := range report.Assets {
		if asset.Scope != "in_scope" {
			continue
		}
		if _, err := toAssetInput(asset, target.target(), false, report.Run.ID); err != nil {
			continue
		}
		value := asset.Value
		if asset.Type == "service" && asset.Port > 0 {
			value = net.JoinHostPort(asset.Value, strconv.Itoa(asset.Port))
		}
		result = append(result, asset.Type+": "+value)
		if len(result) >= 12 {
			break
		}
	}
	return result
}

func (s *Service) phase(ctx context.Context, user int, execution, id, phase string, maxWeb int, progress func(string)) (phaseResult, error) {
	result := phaseResult{RunID: id, Phase: phase, Samples: []string{}, AssetTypes: map[string]int{}}
	if _, ok := phaseLabels[phase]; !ok || phase == "finish" {
		return result, errors.New("采集阶段无效")
	}
	if maxWeb == 0 {
		maxWeb = 5
	}
	if maxWeb < 1 || maxWeb > 50 || phase != "web" && maxWeb != 5 {
		return result, errors.New("Web 入口数量无效")
	}
	if err := s.store.authorize(ctx, user, execution); err != nil {
		return result, err
	}
	run, target, resumed, err := s.phaseScope(ctx, user, execution, id)
	if err != nil {
		return result, err
	}
	executor, ok := s.executor.(PhaseExecutor)
	if !ok {
		return result, errors.New("当前信息收集执行器不支持分阶段运行")
	}
	var raw []byte
	var coverage json.RawMessage
	if resumed {
		raw, coverage, err = executor.(ResumePhaseExecutor).ExecuteResumedPhase(ctx, user, id, target, run.Mode, phase, maxWeb, progress)
	} else {
		raw, coverage, err = executor.ExecutePhase(ctx, user, id, target, run.Mode, phase, maxWeb, progress)
	}
	if err != nil {
		return result, err
	}
	report, err := decodeEnvelope(raw, id, target, run.Mode)
	if err != nil {
		return result, err
	}
	var state struct {
		Steps map[string]string `json:"steps"`
	}
	if json.Unmarshal(coverage, &state) != nil || state.Steps[phase] == "" || state.Steps[phase] == "running" {
		return result, errors.New("采集阶段未返回有效的执行状态")
	}
	if err = s.store.checkpoint(ctx, user, execution, id, report, phaseCoverage(coverage, target.Input, execution)); err != nil {
		return result, err
	}
	result.Status = state.Steps[phase]
	result.ArtifactDirectory = executor.ArtifactDirectory(user, run.ID)
	result.Assets, result.Vulnerabilities, result.Observations = len(report.Assets), len(report.Vulnerabilities), len(report.Observations)
	for _, asset := range report.Assets {
		switch asset.Type {
		case "domain", "ip", "service", "endpoint":
			result.AssetTypes[asset.Type]++
		}
	}
	result.Samples = phaseSamples(report, target)
	result.Coverage = coverage
	statusName := map[string]string{"success": "完成", "partial": "部分完成", "failed": "失败", "timeout": "超时", "skipped": "跳过", "interrupted": "中断"}[result.Status]
	result.Message = fmt.Sprintf("%s：%s。已保存 %d 条资产、%d 条待复核风险、%d 条观察线索。", phaseLabels[phase], statusName, result.Assets, result.Vulnerabilities, result.Observations)
	if resumed {
		result.Message = "已续接同一会话中的采集结果。" + result.Message
	}
	if summary := coverageSummary(coverage); summary != "" {
		result.Message += " " + summary
	}
	return result, nil
}

func (s *Service) finishPhases(ctx context.Context, user int, execution, id string, progress func(string)) (ExecutionResult, error) {
	if err := s.store.authorize(ctx, user, execution); err != nil {
		return ExecutionResult{}, err
	}
	run, target, resumed, err := s.phaseScope(ctx, user, execution, id)
	if err != nil {
		return ExecutionResult{}, err
	}
	executor, ok := s.executor.(PhaseExecutor)
	if !ok {
		return ExecutionResult{}, errors.New("当前信息收集执行器不支持分阶段运行")
	}
	var raw []byte
	var coverage json.RawMessage
	if resumed {
		raw, coverage, err = executor.(ResumePhaseExecutor).ExecuteResumedPhase(ctx, user, id, target, run.Mode, "finish", 5, progress)
	} else {
		raw, coverage, err = executor.ExecutePhase(ctx, user, id, target, run.Mode, "finish", 5, progress)
	}
	if err != nil {
		return ExecutionResult{}, err
	}
	report, err := decodeEnvelope(raw, id, target, run.Mode)
	if err != nil {
		return ExecutionResult{}, err
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return s.completeReport(ctx, cleanup, user, id, target, report, phaseCoverage(coverage, target.Input, execution), progress)
}

func phaseProgress(tc *tool.ToolContext) func(string) {
	if tc == nil || tc.Emit == nil {
		return nil
	}
	return func(message string) { tc.Emit(tool.ProgressInfo{Message: message}) }
}

func phaseToolResult(value any, err error) tool.Result {
	if err != nil {
		return tool.Errorf("信息收集操作未完成：" + err.Error())
	}
	data, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		return tool.Errorf("信息收集结果无法编码")
	}
	return tool.Text(string(data))
}

// PhaseTools return genuine model-invoked calls, not synthetic tool events.
func (s *Service) PhaseTools(user int) []tool.CoreTool {
	allow := func(context.Context, json.RawMessage, permission.Context) permission.Decision {
		return permission.Allowed()
	}
	mutates := func(json.RawMessage) bool { return false }
	prepare := tool.Build(tool.Spec{
		Name: "prepare_info_gather", Description: "为用户授权的单一目标创建分阶段信息收集。先调用 Skill 读取 info-gath；本工具只建立受控范围，不执行探测。取得 run.id 后根据结果自行选择阶段，最后调用 finish_info_gather 同步。阶段开始后，可使用 LS/Read 检查 artifactDirectory 内的报告和证据。",
		Schema: map[string]any{"type": "object", "properties": map[string]any{
			"target": map[string]any{"type": "string", "description": "用户明确授权的域名、HTTP(S) URL、IP 或 CIDR；URL 不含凭据和查询参数。"},
			"mode":   map[string]any{"type": "string", "enum": []string{"fast", "normal", "deep"}, "description": "fast 仅被动；normal 常规；deep 深入。默认 normal。"},
		}, "required": []string{"target"}, "additionalProperties": false},
		ReadOnly: mutates, Concurrent: mutates, Permissions: allow,
		Run: func(ctx context.Context, raw json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
			var input struct {
				Target string `json:"target"`
				Mode   string `json:"mode"`
			}
			if json.Unmarshal(raw, &input) != nil || tc == nil || tc.ToolUseID == "" {
				return tool.Errorf("采集参数无效"), nil
			}
			run, err := s.preparePhases(ctx, user, airuntime.ExecutionID(ctx), tc.ToolUseID, input.Target, input.Mode)
			directory := ""
			if err == nil {
				directory = s.executor.(PhaseExecutor).ArtifactDirectory(user, run.ID)
			}
			return phaseToolResult(struct {
				Message           string `json:"message"`
				Run               Run    `json:"run"`
				ArtifactDirectory string `json:"artifactDirectory"`
			}{"采集范围已建立。请根据目标类型逐阶段执行并分析结果，最后完成归集。", run, directory}, err), nil
		},
	})
	step := tool.Build(tool.Spec{
		Name: "run_info_gather_phase", Description: "只执行一个 info-gath 阶段并返回已保存的发现、覆盖和状态；你应阅读结果后决定下一步，可对失败阶段重试一次。普通模式端口阶段会复用已完成目标，只补扫未完成目标。模型中断后在原会话沿用已有 run_id 续接，不要重新创建采集或重复已完成阶段。passive 被动子域、organization 组织线索、dns_brute 字典枚举（仅 deep）、dns 解析、axfr 区域传送、cdn CDN/ASN、http 存活探测、ports 端口、web 深入 Web。依次优先考虑被动、DNS、HTTP，再按结果决定端口和 Web。仅使用已建立范围，关联线索不能扩展扫描范围。",
		Schema: map[string]any{"type": "object", "properties": map[string]any{
			"run_id":        map[string]any{"type": "string", "description": "prepare_info_gather 返回的 run.id。"},
			"phase":         map[string]any{"type": "string", "enum": []string{"passive", "organization", "dns_brute", "dns", "axfr", "cdn", "http", "ports", "web"}},
			"max_web_hosts": map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "description": "仅 web 阶段可设置，默认 5；根据用户要求和发现数量决定，最多 50。"},
		}, "required": []string{"run_id", "phase"}, "additionalProperties": false},
		ReadOnly: mutates, Concurrent: mutates, Permissions: allow,
		Run: func(ctx context.Context, raw json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
			var input struct {
				ID     string `json:"run_id"`
				Phase  string `json:"phase"`
				MaxWeb int    `json:"max_web_hosts"`
			}
			if json.Unmarshal(raw, &input) != nil || tc == nil || tc.ToolUseID == "" {
				return tool.Errorf("采集参数无效"), nil
			}
			value, err := s.phase(ctx, user, airuntime.ExecutionID(ctx), input.ID, input.Phase, input.MaxWeb, phaseProgress(tc))
			return phaseToolResult(value, err), nil
		},
	})
	finish := tool.Build(tool.Spec{
		Name: "finish_info_gather", Description: "完成一次分阶段采集，按系统目标范围校验并同步可归集的资产与待复核风险；未匹配或归属不明的记录保留待确认。未执行的适用阶段会真实标记为未完成。",
		Schema:   map[string]any{"type": "object", "properties": map[string]any{"run_id": map[string]any{"type": "string"}}, "required": []string{"run_id"}, "additionalProperties": false},
		ReadOnly: mutates, Concurrent: mutates, Permissions: allow,
		Run: func(ctx context.Context, raw json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
			var input struct {
				ID string `json:"run_id"`
			}
			if json.Unmarshal(raw, &input) != nil || tc == nil || tc.ToolUseID == "" {
				return tool.Errorf("采集参数无效"), nil
			}
			value, err := s.finishPhases(ctx, user, airuntime.ExecutionID(ctx), input.ID, phaseProgress(tc))
			return phaseToolResult(value, err), nil
		},
	})
	return []tool.CoreTool{prepare, step, finish}
}

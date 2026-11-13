package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/tool"
)

type Service struct {
	store     Store
	capture   ReviewCapture
	reviewer  Reviewer
	blacklist PlatformBlacklistResolver
}

func NewService(store Store) *Service { return &Service{store: store} }
func (s *Service) SetJudgeDependencies(capture ReviewCapture, reviewer Reviewer) {
	s.capture = capture
	s.reviewer = reviewer
}
func (s *Service) Rules(ctx context.Context, user int) ([]Rule, error) {
	if user <= 0 {
		return nil, ErrForbidden
	}
	return s.store.ListRules(ctx, user, true)
}
func (s *Service) SaveRule(ctx context.Context, user int, rule Rule, create bool) (Rule, error) {
	rule.Name = strings.TrimSpace(rule.Name)
	if rule.TimeoutSeconds == 0 {
		rule.TimeoutSeconds = 60
	}
	if rule.TimeoutAction == "" {
		rule.TimeoutAction = "deny"
	}
	if user <= 0 || !validRule(rule) || (!create && rule.ID <= 0) {
		return Rule{}, ErrInvalidInput
	}
	return s.store.SaveRule(ctx, user, rule, create)
}
func (s *Service) DeleteRule(ctx context.Context, user int, id int64) error {
	if user <= 0 || id <= 0 {
		return ErrInvalidInput
	}
	return s.store.DeleteRule(ctx, user, id)
}
func (s *Service) ToggleRule(ctx context.Context, user int, id int64, enabled bool) error {
	if user <= 0 || id <= 0 {
		return ErrInvalidInput
	}
	return s.store.ToggleRule(ctx, user, id, enabled)
}
func (s *Service) EnabledTools(ctx context.Context, user int) ([]string, error) {
	if user <= 0 {
		return nil, ErrForbidden
	}
	return s.store.EnabledTools(ctx, user, true)
}
func (s *Service) SetEnabledTools(ctx context.Context, user int, items []string) error {
	if user <= 0 || len(items) > 500 {
		return ErrInvalidInput
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, name := range items {
		name = strings.TrimSpace(name)
		if name == "" || len(name) > 200 || strings.ContainsAny(name, "\x00\r\n") {
			return ErrInvalidInput
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return s.store.SetEnabledTools(ctx, user, out)
}
func (s *Service) Get(ctx context.Context, user int, id int64) (*Approval, error) {
	if user <= 0 || id <= 0 {
		return nil, ErrInvalidInput
	}
	return s.store.ReadApproval(ctx, user, id)
}
func (s *Service) Execution(ctx context.Context, user int, id int64) (*Execution, error) {
	if user <= 0 || id <= 0 {
		return nil, ErrInvalidInput
	}
	return s.store.ReadExecution(ctx, user, id)
}
func (s *Service) ExecutionContext(ctx context.Context, user int, id, before, after int64, limit int) (ExecutionContextPage, error) {
	if user <= 0 || id <= 0 || before < 0 || after < 0 || before > 0 && after > 0 || limit < 1 || limit > 100 {
		return ExecutionContextPage{}, ErrInvalidInput
	}
	store, ok := s.store.(ExecutionContextStore)
	if !ok {
		return ExecutionContextPage{}, ErrExecutionUnavailable
	}
	return store.ReadExecutionContext(ctx, user, id, before, after, limit)
}
func (s *Service) List(ctx context.Context, user int, filter Filter) ([]Approval, int64, error) {
	if user <= 0 || filter.Page < 1 || filter.Page > 1000000 || filter.Size < 1 || filter.Size > 100 || filter.TaskID < 0 || len(filter.Query) > 1000 || !utf8.ValidString(filter.Query) || strings.ContainsRune(filter.Query, '\x00') {
		return nil, 0, ErrInvalidInput
	}
	if filter.Status != "" && filter.Status != "pending" && filter.Status != "allowed" && filter.Status != "denied" && filter.Status != "timeout" {
		return nil, 0, ErrInvalidInput
	}
	for _, value := range []string{filter.From, filter.To} {
		if value != "" {
			if _, err := time.Parse("2006-01-02", value); err != nil {
				return nil, 0, ErrInvalidInput
			}
		}
	}
	if filter.Source != "" && filter.Source != "rule" && filter.Source != "model" && filter.Source != "manual" && filter.Source != "unknown" {
		return nil, 0, ErrInvalidInput
	}
	return s.store.ListApprovals(ctx, user, filter)
}
func (s *Service) Decide(ctx context.Context, user int, id int64, decision string) error {
	if user <= 0 || id <= 0 || (decision != "allowed" && decision != "denied") {
		return ErrInvalidInput
	}
	return s.store.DecideApproval(ctx, user, id, decision)
}

type Emit func(kind, summary, detail string)
type interceptedTool struct {
	tool.CoreTool
	service *Service
	call    Call
	emit    Emit
}

func (s *Service) Wrap(item tool.CoreTool, call Call, emit Emit) tool.CoreTool {
	if s == nil || s.store == nil || item == nil {
		return item
	}
	return &interceptedTool{CoreTool: item, service: s, call: call, emit: emit}
}
func (t *interceptedTool) IsDeferredTool() bool {
	v, ok := t.CoreTool.(interface{ IsDeferredTool() bool })
	return ok && v.IsDeferredTool()
}
func (t *interceptedTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
	call := t.call
	if ctx.Err() != nil {
		return tool.Errorf("运行已停止，工具未执行"), nil
	}
	call.ToolName = t.Name()
	call.Input = append(json.RawMessage(nil), input...)
	if tc != nil {
		call.ToolUseID = tc.ToolUseID
	}
	if audit, ok := intercept.AuditSnapshotForTool(ctx, call.ToolUseID, call.ToolName, input); ok {
		call.TraceAudit, _ = json.Marshal(audit)
	}
	names, err := t.service.store.EnabledTools(ctx, call.UserID, false)
	if err != nil {
		return tool.Errorf("审批配置不可用，工具未执行"), nil
	}
	enabled := false
	for _, name := range names {
		if name == call.ToolName {
			enabled = true
			break
		}
	}
	if !enabled {
		return t.CoreTool.Call(ctx, input, tc)
	}
	if len(input) > 1<<20 {
		return tool.Errorf("工具输入过大，无法进行审批"), nil
	}
	rules, err := t.service.store.ListRules(ctx, call.UserID, false)
	if err != nil {
		return tool.Errorf("审批规则不可用，工具未执行"), nil
	}
	rule := MatchRules(rules, call.ToolName, input)
	if rule == nil {
		judged, enabled, judgeErr := t.service.Judge(ctx, call.UserID, call.ToolName, input, tc)
		if judgeErr != nil {
			return tool.Errorf("模型审批配置不可用，工具未执行"), nil
		}
		if !enabled {
			return t.CoreTool.Call(ctx, input, tc)
		}
		rule = judged
	}
	record, err := t.service.store.CreateApproval(ctx, call, *rule, rule.Action)
	if err != nil {
		if record != nil && record.ID > 0 {
			cancelCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = t.service.store.CancelApproval(cancelCtx, call, record.ID)
			cancel()
		}
		return tool.Errorf("审批记录创建失败，工具未执行"), nil
	}
	if rule.Action == "deny" {
		return tool.Errorf(blockMessage(record.Reason)), nil
	}
	if rule.Action == "allow" && (record.Status != "allowed" || record.ExecutionStatus != "pending") {
		return tool.Errorf("运行已停止，工具未执行"), nil
	}
	if rule.Action == "ask" {
		var eventInput any = json.RawMessage(input)
		if !json.Valid(input) {
			eventInput = string(input)
		}
		detail, _ := json.Marshal(map[string]any{"pending_id": record.ID, "tool": call.ToolName, "input": eventInput})
		if t.emit != nil {
			t.emit("intercept_request", fmt.Sprintf("工具 %s 请求审批 (#%d)", call.ToolName, record.ID), string(detail))
		}
		if !t.wait(ctx, call, record.ID, rule) {
			return tool.Errorf(blockMessage("人工审批未通过或运行已停止")), nil
		}
	}
	if ctx.Err() != nil {
		cancelCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = t.service.store.CancelApproval(cancelCtx, call, record.ID)
		cancel()
		return tool.Errorf("运行已停止，工具未执行"), nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			finishCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = t.service.store.FinishApproval(finishCtx, call, record.ID, false, "工具执行异常", false)
			cancel()
			panic(recovered)
		}
	}()
	result, runErr := t.CoreTool.Call(ctx, input, tc)
	output := result.Flatten()
	if !utf8.ValidString(output) {
		output = strings.ToValidUTF8(output, "�")
	}
	truncated := len(output) > 65536
	if len(output) > 65536 {
		output = output[:65536]
		for !utf8.ValidString(output) {
			output = output[:len(output)-1]
		}
	}
	finishCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = t.service.store.FinishApproval(finishCtx, call, record.ID, runErr == nil && !result.IsError, output, truncated)
	return result, runErr
}
func blockMessage(reason string) string {
	return "【ARTEX 平台管控·非目标防御】此调用被平台拦截。原因：" + reason + "。此操作被禁止。"
}
func (t *interceptedTool) wait(ctx context.Context, call Call, id int64, rule *Rule) bool {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	var timeout <-chan time.Time
	var timer *time.Timer
	if rule.TimeoutEnabled {
		timer = time.NewTimer(time.Duration(rule.TimeoutSeconds) * time.Second)
		timeout = timer.C
		defer timer.Stop()
	}
	for {
		row, err := t.service.store.ReadApproval(ctx, call.UserID, id)
		if err != nil {
			cancelCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = t.service.store.CancelApproval(cancelCtx, call, id)
			cancel()
			return false
		}
		if row.Status != "pending" {
			return row.ExecutionStatus == "pending" && (row.Status == "allowed" || row.Status == "timeout" && row.EffectiveAction == "allow")
		}
		select {
		case <-ctx.Done():
			cancelCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = t.service.store.CancelApproval(cancelCtx, call, id)
			cancel()
			return false
		case <-timeout:
			resolved, err := t.service.store.ResolveTimeout(ctx, call, id)
			if err != nil {
				return false
			}
			if resolved == "pending" {
				timer.Reset(200 * time.Millisecond)
				timeout = timer.C
				continue
			}
			return resolved == "allow"
		case <-ticker.C:
		}
	}
}

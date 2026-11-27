package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"

	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/compaction"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
)

// Tool implementations supplied by the host/plugins must not bring down the
// server when a callback panics. The failure is returned to the model exactly
// once; no tool is retried here, since side effects may already have happened.
type guardedTool struct {
	tool.CoreTool
	policyFailed atomic.Bool
}

func guardPanic(item tool.CoreTool) tool.CoreTool {
	if _, guarded := item.(*guardedTool); guarded {
		return item
	}
	return &guardedTool{CoreTool: item}
}

func newToolSafeSession(ctx context.Context, options agentcore.Options) *agentcore.Session {
	window := 256000
	if options.Compaction != nil && options.Compaction.ContextWindow > 0 {
		window = options.Compaction.ContextWindow
	}
	windowFn := func() int { return window }
	if provider, ok := options.Provider.(contextWindowProvider); ok {
		windowFn = provider.ContextWindowTokens
	} else if manager, ok := options.Compactor.(contextWindowProvider); ok {
		windowFn = manager.ContextWindowTokens
	}
	if provider, ok := options.Provider.(contextWindowProvider); ok && options.Compactor == nil && options.Compaction != nil {
		options.Compactor = &adaptiveCompactor{config: *options.Compaction, window: provider.ContextWindowTokens,
			summarize: compaction.ProviderSummarizer(options.Provider, options.MaxTokens, options.NonStreaming)}
		options.Compaction = nil
	}
	if options.Compactor == nil && options.Compaction != nil {
		options.Compactor = compaction.New(*options.Compaction, compaction.ProviderSummarizer(options.Provider, options.MaxTokens, options.NonStreaming))
		options.Compaction = nil
	}
	if options.Compactor != nil && filepath.IsAbs(options.WorkingDir) {
		options.Compactor = &contextBudgetCompactor{Compactor: options.Compactor, window: windowFn,
			directory: filepath.Join(options.WorkingDir, ".context"), warn: options.OnWarn}
	}
	if policy, ok := ctx.Value(hostPolicyKey{}).(HostPolicy); ok && policy.ExternalBackgroundTasks {
		options.DisableBackgroundTasks = true
	}
	if options.Tools != nil {
		tools := append([]tool.CoreTool{}, options.Tools...)
		for i, item := range tools {
			tools[i] = guardPanic(item)
		}
		options.Tools = tools
	}
	return agentcore.NewSession(options)
}

func (t *guardedTool) AcceptsRawInput() (raw bool) {
	defer func() {
		if recover() != nil {
			t.policyFailed.Store(true)
			raw = false
		}
	}()
	item, ok := t.CoreTool.(interface{ AcceptsRawInput() bool })
	return ok && item.AcceptsRawInput()
}

func (t *guardedTool) IsDeferredTool() bool {
	item, ok := t.CoreTool.(interface{ IsDeferredTool() bool })
	return ok && item.IsDeferredTool()
}

func (t *guardedTool) IsReadOnly(input json.RawMessage) (readOnly bool) {
	defer func() {
		if recover() != nil {
			t.policyFailed.Store(true)
			readOnly = false
		}
	}()
	return t.CoreTool.IsReadOnly(input)
}

func (t *guardedTool) IsConcurrencySafe(input json.RawMessage) (safe bool) {
	defer func() {
		if recover() != nil {
			t.policyFailed.Store(true)
			safe = false
		}
	}()
	return t.CoreTool.IsConcurrencySafe(input)
}

func (t *guardedTool) CheckPermissions(ctx context.Context, input json.RawMessage, pc permission.Context) (decision permission.Decision) {
	defer func() {
		if recover() != nil {
			t.policyFailed.Store(true)
			decision = permission.Denied("工具权限检查异常，本次调用未执行")
		}
	}()
	if t.policyFailed.Load() {
		return permission.Denied("工具权限或调度信息异常，本次调用未执行")
	}
	return t.CoreTool.CheckPermissions(ctx, input, pc)
}

func (t *guardedTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (result tool.Result, err error) {
	if ctx.Err() != nil {
		return tool.Result{}, ctx.Err()
	}
	if t.policyFailed.Load() {
		return tool.Errorf("工具权限或调度信息异常，本次调用未执行"), nil
	}
	defer func() {
		if recover() != nil {
			if ctx.Err() != nil {
				result, err = tool.Result{}, ctx.Err()
				return
			}
			result = tool.Errorf("工具发生内部异常（panic），本次调用失败，已将错误返回给 Agent。操作可能已部分执行，请核对现有结果后决定后续步骤，不要直接重复有副作用的操作。")
			err = nil
		}
	}()
	return t.CoreTool.Call(ctx, input, tc)
}

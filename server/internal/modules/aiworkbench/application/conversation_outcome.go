package application

import (
	"context"
	"errors"
	"github.com/Autumn-27/artex/agent"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
)

// Keep provider/storage failure separate from a normal budget stop.
func conversationOutcome(reason string, err error) (string, string) {
	if errors.Is(err, airuntime.ErrTranscriptMissing) {
		return "failed", airuntime.ErrTranscriptMissing.Error()
	}
	if errors.Is(err, agent.ErrRunBudget) && (reason == "" || reason == "model_error") {
		return "exhausted", agent.ErrRunBudget.Error()
	}
	switch reason {
	case "run_budget":
		if err != nil {
			return "exhausted", "本轮模型预算已用尽，收尾未完整结束；已保存的结果仍保留。"
		}
		return "exhausted", "本轮模型预算已用尽，已结束收尾；可在当前会话继续发消息。"
	case "persistence_error":
		return "failed", "会话检查点保存失败；已停止执行，请检查存储后恢复。"
	case "empty_response":
		return "failed", "模型未能生成文字总结；已完成的工具结果仍保留，可在当前会话要求仅汇总已有结果。"
	case "timeout":
		return "timeout", "本轮运行时间预算已用尽，已保存现有结果。"
	case "max_turns":
		return "exhausted", "本轮执行步数预算已用尽，已保存现有结果。"
	case "blocking_limit", "prompt_too_long", "image_error":
		return "failed", "模型上下文或输入限制导致本轮停止，请检查模型配置。"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", "执行超过运行时限。"
	}
	if errors.Is(err, context.Canceled) {
		return "interrupted", "本轮执行已中断，已保存的结果仍保留。"
	}
	if err != nil {
		return "failed", "执行未能完成，请检查模型、工具或存储状态；已保存的结果仍保留。"
	}
	return "completed", ""
}

type ConversationOutcomeStore interface {
	FinishRunOutcome(context.Context, int, string, string, string, string, string, string) error
}

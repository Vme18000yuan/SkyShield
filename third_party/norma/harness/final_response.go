package harness

import (
	"errors"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
)

var ErrEmptyFinalResponse = errors.New("model did not provide a final response after text-only recovery")

// beginFinalResponse reuses the recorded conversation to request an answer,
// without resuming exploration or replaying any completed tool call. Only the
// query-local registry is replaced; the Session keeps its tools for later turns.
func (l *loop) beginFinalResponse(schemas *[]llm.ToolSchema) bool {
	msg := llm.UserText(l.in.FinalResponsePrompt)
	l.messages = append(l.messages, msg)
	l.record(msg, llm.Usage{})
	l.finalizing = true
	l.in.Tools = tool.NewRegistry()
	l.in.Todos = nil
	*schemas = nil
	// Keep the configured provider and run deadlines, and parent cancellation.
	// The single recovery phase does not impose a new execution-time budget.
	l.turnCount++
	l.lastContinue = ContinueFinalResponse
	return l.emit(Event{Kind: KindProgress, Text: "模型未返回文字总结，正在基于本轮已有结果补写总结"})
}

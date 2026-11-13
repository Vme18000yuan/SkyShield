package airuntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Autumn-27/artex/agent"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/llmpool"
	"github.com/Autumn-27/norma/llm"
	"io"
	"iter"
	"log/slog"
	"net/url"
	"strings"
	"time"
)

// RuntimeModelOptions carries ARTEX harness settings alongside the provider,
// so model settings change actual runs rather than only the editor.
type RuntimeModelOptions struct {
	ContextWindow  int
	MaxTokens      int
	NonStreaming   bool
	NoaCompaction  bool
	IntentRetry    artexdb.RetryRule
	StreamAttempts int
	StreamInterval time.Duration
}
type runtimeModel interface{ RuntimeModelOptions() RuntimeModelOptions }
type modelOptionsProvider struct {
	llm.Provider
	options RuntimeModelOptions
}

func (p modelOptionsProvider) RuntimeModelOptions() RuntimeModelOptions { return p.options }
func (p modelOptionsProvider) ContextWindowTokens() int                 { return p.options.ContextWindow }
func WithModelOptions(provider llm.Provider, options RuntimeModelOptions) llm.Provider {
	return modelOptionsProvider{Provider: provider, options: options}
}
func ModelOptions(provider llm.Provider) RuntimeModelOptions {
	if configured, ok := provider.(runtimeModel); ok {
		return configured.RuntimeModelOptions()
	}
	return RuntimeModelOptions{ContextWindow: 32_000}
}

type configuredProvider struct {
	base    llm.Provider
	config  agent.Config
	options RuntimeModelOptions
}

func ConfigureProvider(base llm.Provider, config agent.Config, policy artexdb.LLMRetryPolicy, noa bool) llm.Provider {
	return &configuredProvider{base: base, config: config, options: RuntimeModelOptions{ContextWindow: config.CompactionWindow(), MaxTokens: config.MaxTokens, NonStreaming: !config.Stream, NoaCompaction: noa, IntentRetry: policy.Intent, StreamAttempts: config.Retry.StreamAttempts, StreamInterval: config.Retry.StreamInterval}}
}
func (p *configuredProvider) RuntimeModelOptions() RuntimeModelOptions { return p.options }
func (p *configuredProvider) ContextWindowTokens() int                 { return p.options.ContextWindow }
func (p *configuredProvider) request(req llm.CompletionRequest) llm.CompletionRequest {
	if p.config.MaxTokens > 0 {
		if req.MaxTokens <= 0 || req.MaxTokens > p.config.MaxTokens {
			req.MaxTokens = p.config.MaxTokens
		}
	} else {
		cap := min(8192, p.options.ContextWindow/4)
		if req.MaxTokens <= 0 || req.MaxTokens > cap {
			req.MaxTokens = cap
		}
	}
	return req
}

func (p *configuredProvider) checkWindow(req llm.CompletionRequest) error {
	if p.options.ContextWindow > 0 && llmpool.EstimateRequestTokens(req)+req.MaxTokens > p.options.ContextWindow {
		return fmt.Errorf("context length exceeds configured model window (%d tokens); compact before retry", p.options.ContextWindow)
	}
	return nil
}
func (p *configuredProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	req = p.request(req)
	if err := p.checkWindow(req); err != nil {
		return llm.Message{}, "", llm.Usage{}, err
	}
	message, reason, usage, err := p.base.Complete(ctx, req)
	return message, reason, usage, p.redact(err)
}

type redactedModelError struct {
	cause   error
	message string
}

func (e redactedModelError) Error() string { return e.message }
func (e redactedModelError) Unwrap() error { return e.cause }
func (p *configuredProvider) redact(err error) error {
	if err == nil {
		return nil
	}
	value := err.Error()
	for _, secret := range []string{p.config.APIKey, p.config.Proxy} {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	if proxy, parseErr := url.Parse(p.config.Proxy); parseErr == nil && proxy.User != nil {
		if password, ok := proxy.User.Password(); ok && password != "" {
			value = strings.ReplaceAll(value, password, "[REDACTED]")
		}
	}
	return redactedModelError{cause: err, message: value}
}

// Mirrors ARTEX's same-provider safe-window retry. No output or tool action is
// replayed once a visible delta or tool call has reached the caller.
func (p *configuredProvider) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		req = p.request(req)
		if err := p.checkWindow(req); err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}
		retries := 2
		fallbackUsed := false
		if p.config.Retry.StreamAttempts != 0 {
			retries = max(0, p.config.Retry.StreamAttempts)
		}
		for attempt := 0; ; attempt++ {
			committed := false
			var pending []llm.StreamEvent
			var failure error
			for event, err := range p.base.Stream(ctx, p.request(req)) {
				if err != nil {
					failure = p.redact(err)
					break
				}
				if !committed && !streamCommitsOutput(event) && len(pending) < 256 {
					pending = append(pending, event)
					continue
				}
				if !committed {
					for _, ev := range pending {
						if !yield(ev, nil) {
							return
						}
					}
					pending = nil
					committed = true
				}
				if !yield(event, nil) {
					return
				}
			}
			if failure != nil && !committed && ctx.Err() == nil && attempt < retries && retryableStreamError(failure) {
				// Some OpenAI-compatible gateways intermittently close a streaming
				// POST before sending headers. Before any tool or text was delivered,
				// one non-streaming attempt can safely preserve the agent turn. It
				// uses the same provider, proxy, limiter, and usage recorder.
				if !fallbackUsed && p.config.Format == llm.FormatOpenAI && (errors.Is(failure, io.EOF) || errors.Is(failure, io.ErrUnexpectedEOF)) {
					fallbackUsed = true
					slog.Warn("LLM 流式请求在输出前断开，尝试非流式恢复")
					fallbackCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
					message, reason, usage, completeErr := p.base.Complete(fallbackCtx, p.request(req))
					cancel()
					if completeErr == nil {
						events, eventErr := completionAsStream(message, reason, usage)
						if eventErr == nil {
							for _, event := range events {
								if !yield(event, nil) {
									return
								}
							}
							return
						}
					}
					if ctx.Err() != nil {
						yield(llm.StreamEvent{}, ctx.Err())
						return
					}
				}
				delay := min(500*time.Millisecond*time.Duration(1<<min(attempt, 4)), 4*time.Second)
				if p.config.Retry.StreamInterval > 0 {
					delay = p.config.Retry.StreamInterval
				}
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					yield(llm.StreamEvent{}, ctx.Err())
					return
				case <-timer.C:
				}
				continue
			}
			if failure == nil || committed {
				for _, event := range pending {
					if !yield(event, nil) {
						return
					}
				}
			}
			if failure != nil {
				yield(llm.StreamEvent{}, failure)
			}
			return
		}
	}
}

// completionAsStream converts one complete turn only after validating every
// tool call, before exposing any event to the harness. This prevents a malformed
// fallback response from launching a partial or unintended tool call.
func completionAsStream(message llm.Message, reason string, usage llm.Usage) ([]llm.StreamEvent, error) {
	if message.Role != "" && message.Role != llm.RoleAssistant || len(message.Content) == 0 {
		return nil, errors.New("模型非流式响应为空或角色无效")
	}
	events := []llm.StreamEvent{{Type: llm.SEMessageStart}}
	seen := map[string]bool{}
	tools := 0
	content := false
	for _, block := range message.Content {
		switch block.Type {
		case llm.BlockText:
			if block.Text != "" {
				content = true
				events = append(events, llm.StreamEvent{Type: llm.SETextDelta, Text: block.Text})
			}
		case llm.BlockThinking:
			if block.Thinking != "" {
				content = true
				events = append(events, llm.StreamEvent{Type: llm.SEThinkingDelta, Text: block.Thinking})
			}
			if block.Signature != "" {
				events = append(events, llm.StreamEvent{Type: llm.SEThinkingSignature, Text: block.Signature})
			}
		case llm.BlockToolUse:
			input := block.Input
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			if block.ID == "" || block.Name == "" || seen[block.ID] || !json.Valid(input) {
				return nil, errors.New("模型非流式工具调用无效")
			}
			seen[block.ID] = true
			tools++
			content = true
			events = append(events, llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: block.ID, ToolName: block.Name}, llm.StreamEvent{Type: llm.SEToolInputJSON, Text: string(input)})
		default:
			return nil, errors.New("模型非流式响应包含未知内容")
		}
	}
	if !content {
		return nil, errors.New("模型非流式响应没有有效内容")
	}
	if reason == "" {
		reason = "end_turn"
		if tools > 0 {
			reason = "tool_use"
		}
	}
	events = append(events, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: reason, Usage: usage}, llm.StreamEvent{Type: llm.SEMessageStop})
	return events, nil
}
func streamCommitsOutput(event llm.StreamEvent) bool {
	switch event.Type {
	case llm.SETextDelta, llm.SEThinkingDelta, llm.SEToolInputJSON:
		return event.Text != ""
	case llm.SEToolUseStart, llm.SEMessageDelta, llm.SEMessageStop:
		return true
	}
	return false
}
func retryableStreamError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, llm.ErrRunBudget) || agent.IsQuotaExhaustedMessage(err.Error()) {
		return false
	}
	value := strings.ToLower(err.Error())
	for _, marker := range []string{"too long", "context length", "context_length", "maximum context", "status 400", "status 401", "status 403", "status 404", "status 413", "status 422"} {
		if strings.Contains(value, marker) {
			return false
		}
	}
	return true
}

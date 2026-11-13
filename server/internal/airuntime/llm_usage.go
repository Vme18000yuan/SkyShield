// Per-call model usage metering is adapted from ARTEX; AGPL-3.0-only.
package airuntime

import (
	"context"
	"encoding/json"
	"iter"
	"strings"
	"time"

	"github.com/Autumn-27/artex/llmrec"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/transcript"
	"github.com/yyhuni/lunafox/server/internal/pkg"
)

type LLMUsageRecord struct {
	UserID                                 int
	TaskID, ExplorationID                  int64
	ConversationID, ExecutionID, SessionID string
	Worker, Model, ProfileID, ProfileName  string
	Duration                               time.Duration
	Usage                                  llm.Usage
	Status                                 string
}
type LLMUsageSink interface {
	RecordLLMUsage(context.Context, LLMUsageRecord) error
}
type LLMRecordingPayload struct {
	RequestBody        string `json:"request_body"`
	ResponseBody       string `json:"response_body"`
	NormalizedRequest  string `json:"normalized_request"`
	NormalizedResponse string `json:"normalized_response"`
	Truncated          bool   `json:"truncated"`
}
type LLMRecordingSink interface {
	StartLLMRecording(context.Context, LLMUsageRecord) (bool, LLMRecordingLease, error)
	RecordLLMRecording(context.Context, LLMUsageRecord, LLMRecordingLease, LLMRecordingPayload) error
}
type LLMRecordingLease struct {
	TaskID, ExplorationID, Generation int64
}
type LLMUsageScope struct {
	UserID                 int
	TaskID, ExplorationID  int64
	ConversationID, Worker string
}
type usageOwnerKey struct{}
type usageScopeKey struct{}
type usageRoleKey struct{}

func WithLLMUsageScope(ctx context.Context, scope LLMUsageScope) context.Context {
	return context.WithValue(ctx, usageScopeKey{}, scope)
}
func WithLLMUsageRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, usageRoleKey{}, role)
}

// Bind the server-resolved owner per request, not to a cached provider. The
// same model instance may be shared concurrently by different accounts.
type usageOwnerProvider struct {
	inner llm.Provider
	user  int
}

func BindLLMUsageUser(provider llm.Provider, user int) llm.Provider {
	if provider == nil || user <= 0 {
		return provider
	}
	return &usageOwnerProvider{inner: provider, user: user}
}
func (p *usageOwnerProvider) RuntimeModelOptions() RuntimeModelOptions { return ModelOptions(p.inner) }
func (p *usageOwnerProvider) ContextWindowTokens() int                 { return ModelOptions(p.inner).ContextWindow }
func (p *usageOwnerProvider) Stream(ctx context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return p.inner.Stream(context.WithValue(ctx, usageOwnerKey{}, p.user), request)
}
func (p *usageOwnerProvider) Complete(ctx context.Context, request llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	return p.inner.Complete(context.WithValue(ctx, usageOwnerKey{}, p.user), request)
}

type meteredProvider struct {
	inner                         llm.Provider
	sink                          LLMUsageSink
	profileID, profileName, model string
}

// Wrap the concrete provider inside retry and pool routers, so each actual
// attempt is counted against the model that served it.
func MeterLLMProvider(provider llm.Provider, sink LLMUsageSink, profileID, profileName, model string) llm.Provider {
	if provider == nil || sink == nil {
		return provider
	}
	return &meteredProvider{inner: provider, sink: sink, profileID: profileID, profileName: profileName, model: model}
}
func usageWorker(ctx context.Context, fallback string) string {
	if role, _ := ctx.Value(usageRoleKey{}).(string); role != "" {
		return role
	}
	session := transcript.SessionIDFrom(ctx)
	if strings.HasPrefix(session, "exp") {
		_, role, found := strings.Cut(session, "-")
		if found {
			role, _, _ = strings.Cut(role, "-")
			if role == "main" {
				return "mainagent"
			}
			return role
		}
	}
	return fallback
}

const maxLLMRecordingBytes = 8 << 20

func (p *meteredProvider) usageEntry(ctx context.Context) (LLMUsageRecord, bool) {
	user, _ := ctx.Value(usageOwnerKey{}).(int)
	if user <= 0 {
		return LLMUsageRecord{}, false
	}
	entry := LLMUsageRecord{UserID: user, Worker: usageWorker(ctx, ""), Model: p.model, ProfileID: p.profileID, ProfileName: p.profileName, ExecutionID: ExecutionID(ctx), SessionID: transcript.SessionIDFrom(ctx)}
	if scope, ok := ctx.Value(usageScopeKey{}).(LLMUsageScope); ok {
		if scope.UserID != user {
			return LLMUsageRecord{}, false
		}
		entry.TaskID, entry.ExplorationID, entry.ConversationID = scope.TaskID, scope.ExplorationID, scope.ConversationID
		entry.Worker = usageWorker(ctx, scope.Worker)
	}
	if task, ok := TaskExecutionFromContext(ctx); ok {
		if task.UserID != user {
			return LLMUsageRecord{}, false
		}
		entry.TaskID, entry.ExplorationID = task.ID, task.ExplorationID
	}
	return entry, true
}
func (p *meteredProvider) capture(ctx context.Context) (context.Context, *llmrec.Capture, LLMRecordingLease) {
	entry, valid := p.usageEntry(ctx)
	sink, ok := p.sink.(LLMRecordingSink)
	if !ok || !valid {
		return ctx, nil, LLMRecordingLease{}
	}
	checkCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	enabled, lease, err := sink.StartLLMRecording(checkCtx, entry)
	if err != nil || !enabled {
		return ctx, nil, LLMRecordingLease{}
	}
	withCapture, capture := llmrec.NewBoundedCapture(ctx, maxLLMRecordingBytes)
	return withCapture, capture, lease
}
func boundedJSON(value any) (string, bool) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	if len(raw) > maxLLMRecordingBytes {
		return string(raw[:maxLLMRecordingBytes]), true
	}
	return string(raw), false
}
func (p *meteredProvider) record(ctx context.Context, start time.Time, usage llm.Usage, status string, lease LLMRecordingLease, payload *LLMRecordingPayload) {
	entry, valid := p.usageEntry(ctx)
	if !valid {
		return
	}
	entry.Duration, entry.Usage, entry.Status = time.Since(start), usage, status
	writeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := p.sink.RecordLLMUsage(writeCtx, entry); err != nil {
		pkg.Warn("AI model usage recording failed")
	}
	if payload != nil {
		if sink, ok := p.sink.(LLMRecordingSink); ok {
			if err := sink.RecordLLMRecording(writeCtx, entry, lease, *payload); err != nil {
				pkg.Warn("AI model request recording failed")
			}
		}
	}
}
func (p *meteredProvider) Stream(ctx context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		start, usage, status, complete := time.Now(), llm.Usage{}, "ok", false
		ctx, capture, lease := p.capture(ctx)
		var payload *LLMRecordingPayload
		var text, thinking strings.Builder
		var responseBytes int
		var stopReason string
		if capture != nil {
			payload = &LLMRecordingPayload{}
			payload.NormalizedRequest, payload.Truncated = boundedJSON(request)
		}
		defer func() {
			if ctx.Err() != nil || status == "ok" && !complete {
				status = "interrupted"
			}
			if capture != nil {
				payload.RequestBody, payload.ResponseBody = capture.RawRequest(), capture.RawResponse()
				var responseTruncated bool
				payload.NormalizedResponse, responseTruncated = boundedJSON(map[string]any{"text": text.String(), "thinking": thinking.String(), "stop_reason": stopReason, "usage": usage})
				payload.Truncated = payload.Truncated || capture.Truncated() || responseTruncated
			}
			p.record(ctx, start, usage, status, lease, payload)
		}()
		for event, err := range p.inner.Stream(ctx, request) {
			if err != nil {
				status = "error"
			} else if event.Type == llm.SEMessageStart || event.Type == llm.SEMessageDelta {
				usage.Add(event.Usage)
			}
			if payload != nil && err == nil {
				if event.Type == llm.SEMessageDelta && event.StopReason != "" {
					stopReason = event.StopReason
				}
				var dest *strings.Builder
				if event.Type == llm.SETextDelta {
					dest = &text
				} else if event.Type == llm.SEThinkingDelta {
					dest = &thinking
				}
				if dest != nil {
					length := min(len(event.Text), maxLLMRecordingBytes-responseBytes)
					_, _ = dest.WriteString(event.Text[:length])
					responseBytes += length
					payload.Truncated = payload.Truncated || length < len(event.Text)
				}
			}
			if !yield(event, err) || err != nil {
				return
			}
		}
		complete = true
	}
}
func (p *meteredProvider) Complete(ctx context.Context, request llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	start := time.Now()
	ctx, capture, lease := p.capture(ctx)
	message, reason, usage, err := p.inner.Complete(ctx, request)
	status := "ok"
	if ctx.Err() != nil {
		status = "interrupted"
	} else if err != nil {
		status = "error"
	}
	var payload *LLMRecordingPayload
	if capture != nil {
		payload = &LLMRecordingPayload{RequestBody: capture.RawRequest(), ResponseBody: capture.RawResponse(), Truncated: capture.Truncated()}
		var requestTruncated, responseTruncated bool
		payload.NormalizedRequest, requestTruncated = boundedJSON(request)
		var thinking strings.Builder
		for _, block := range message.Content {
			if block.Type == llm.BlockThinking {
				thinking.WriteString(block.Thinking)
			}
		}
		payload.NormalizedResponse, responseTruncated = boundedJSON(map[string]any{"text": message.Text(), "thinking": thinking.String(), "stop_reason": reason, "usage": usage})
		payload.Truncated = payload.Truncated || requestTruncated || responseTruncated
	}
	p.record(ctx, start, usage, status, lease, payload)
	return message, reason, usage, err
}

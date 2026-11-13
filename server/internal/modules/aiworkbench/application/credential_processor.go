package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Autumn-27/artex/agent"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/pkg"
)

type CredentialJob struct {
	IntelligenceID, TaskID                 int64
	UserID                                 int
	TenantID                               string
	Lease, Fingerprint                     string
	Content, Evidence                      string
	Sealed, PreviousSealed, LegacyAnalysis []byte
	AssetIDs                               []int64
	Force                                  bool
}
type CredentialJobStore interface {
	Claim(context.Context) (*CredentialJob, error)
	Enabled(context.Context, *CredentialJob) (bool, error)
	Finish(context.Context, *CredentialJob, string, []byte, json.RawMessage) error
	Retry(context.Context, int, int64, int64) error
}

var ErrCredentialBindingStateChanged = errors.New("credential traffic binding setting changed")

type CredentialTrafficStore interface {
	TrafficState(context.Context, *CredentialJob) (bool, int64, error)
	TrafficAssets(context.Context, *CredentialJob) ([]CredentialTrafficAsset, error)
}
type CredentialTrafficAsset struct{ Type, Label string }
type CredentialTrafficMatcher interface {
	Match(context.Context, *CredentialJob, string, string, []artexdb.CredentialAccount) ([]artexdb.CredentialTraffic, string, string, *int64, error)
}

type CredentialModelResolver func(context.Context, int) (llm.Provider, string, string, bool, error)

type CredentialProcessor struct {
	store   CredentialJobStore
	codec   IntelligenceCodec
	model   CredentialModelResolver
	traffic CredentialTrafficMatcher
	once    sync.Once
	done    chan struct{}
}

func NewCredentialProcessor(store CredentialJobStore, codec IntelligenceCodec, model CredentialModelResolver) *CredentialProcessor {
	if store == nil || codec == nil {
		panic("credential processor requires storage and encryption")
	}
	return &CredentialProcessor{store: store, codec: codec, model: model, done: make(chan struct{})}
}
func (p *CredentialProcessor) SetTrafficMatcher(matcher CredentialTrafficMatcher) {
	p.traffic = matcher
}
func (p *CredentialProcessor) Start(ctx context.Context) { p.once.Do(func() { go p.run(ctx) }) }
func (p *CredentialProcessor) Done() <-chan struct{}     { return p.done }
func (p *CredentialProcessor) run(ctx context.Context) {
	defer close(p.done)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		job, err := p.store.Claim(ctx)
		if err == nil && job != nil {
			p.process(ctx, job)
			continue
		}
		if err != nil && ctx.Err() == nil {
			pkg.Warn("AI credential queue unavailable")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (p *CredentialProcessor) Retry(ctx context.Context, user int, taskID, id int64) error {
	if user <= 0 || taskID <= 0 || id <= 0 {
		return ErrInvalidInput
	}
	return p.store.Retry(ctx, user, taskID, id)
}

func (p *CredentialProcessor) process(parent context.Context, job *CredentialJob) {
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	monitorDone := make(chan struct{})
	var interrupted atomic.Bool
	go func() {
		defer close(monitorDone)
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				enabled, err := p.store.Enabled(ctx, job)
				if err != nil || !enabled {
					interrupted.Store(true)
					cancel()
					return
				}
			}
		}
	}()
	result := artexdb.CredentialAnalysis{State: "ready", Accounts: []artexdb.CredentialAccount{}, Traffic: []artexdb.CredentialTraffic{}, TrafficState: "off"}
	content, evidenceText := job.Content, job.Evidence
	if len(job.Sealed) > 0 {
		value, err := p.codec.Open(intelligenceScope(job.TaskID, "credential")+"/"+job.Fingerprint, job.Sealed)
		if err != nil {
			result.State = "error"
			result.Error = "无法读取原始情报，请检查 AI 加密密钥"
		} else {
			var secret struct{ Content, Evidence string }
			if json.Unmarshal([]byte(value), &secret) != nil {
				result.State = "error"
				result.Error = "原始情报格式不可用"
			} else {
				content, evidenceText = secret.Content, secret.Evidence
			}
		}
	}
	var previous artexdb.CredentialAnalysis
	previousCorrupt := false
	if len(job.PreviousSealed) > 0 {
		value, err := p.codec.Open(intelligenceScope(job.TaskID, "credential")+"/"+job.Fingerprint+"/analysis", job.PreviousSealed)
		if err != nil || json.Unmarshal([]byte(value), &previous) != nil {
			previousCorrupt = true
			result.State = "error"
			result.Error = "无法读取已有整理结果，请检查 AI 加密密钥"
		}
	} else if len(job.LegacyAnalysis) > 0 && string(job.LegacyAnalysis) != "null" {
		_ = json.Unmarshal(job.LegacyAnalysis, &previous)
	}
	if result.State == "ready" {
		result.SourceHash = artexdb.CredentialSourceHash(content)
		if !job.Force && previous.SourceHash == result.SourceHash && previous.Method != "" && previous.Error == "" {
			result.Method, result.Model = previous.Method, previous.Model
			result.Accounts = append(result.Accounts, previous.Accounts...)
			result.InputTokens, result.OutputTokens = previous.InputTokens, previous.OutputTokens
		} else if accounts, ok := agent.ParseCredentialJSON(content); ok {
			result.Method = "rules"
			result.Accounts = accounts
		} else {
			result.Method = "agent"
			if p.model == nil {
				result.State = "error"
				result.Error = "未配置可用的凭据整理模型"
			} else {
				provider, model, prompt, ok, err := p.model(ctx, job.UserID)
				if err != nil || !ok || provider == nil {
					result.State = "error"
					result.Error = "未配置可用的凭据整理模型"
				} else {
					modelCtx := airuntime.WithLLMUsageScope(ctx, airuntime.LLMUsageScope{UserID: job.UserID, TaskID: job.TaskID, Worker: "credential_processor"})
					accounts, usage, extractErr := agent.ExtractCredentials(modelCtx, provider, prompt, content, airuntime.ModelOptions(provider).MaxTokens)
					result.Model = model
					result.InputTokens = usage.InputTokens
					result.OutputTokens = usage.OutputTokens
					if extractErr != nil {
						result.State = "error"
						result.Error = extractErr.Error()
					} else {
						result.Accounts = accounts
					}
				}
			}
		}
	}
	if result.State == "ready" && len(result.Accounts) == 0 {
		result.Note = "未识别到可独立展示的凭据，原文已保留"
	}
	if result.State == "ready" && len(result.Accounts) == 100 {
		result.Note = "单条最多整理 100 组凭据，完整信息仍可在原始上报中查看"
	}
	var trafficEpoch *int64
	if previous.SourceHash != "" && previous.SourceHash == result.SourceHash {
		result.Traffic = append(result.Traffic, previous.Traffic...)
	}
	if result.State == "ready" && p.traffic != nil {
		matched, state, note, epoch, matchErr := p.traffic.Match(ctx, job, content, evidenceText, result.Accounts)
		trafficEpoch = epoch
		result.TrafficState = state
		result.TrafficNote = note
		if matchErr != nil && ctx.Err() == nil {
			result.TrafficState = "error"
			result.TrafficNote = "流量核对失败，可稍后重试"
		}
		seen := map[string]bool{}
		merged := make([]artexdb.CredentialTraffic, 0, 12)
		for _, ref := range append(result.Traffic, matched...) {
			if !seen[ref.ID] && len(merged) < 12 {
				seen[ref.ID] = true
				merged = append(merged, ref)
			}
		}
		result.Traffic = merged
		if state != "off" && len(merged) > 0 {
			result.TrafficState = "ready"
			if len(matched) == 0 {
				result.TrafficNote = "已保留既有流量引用与匹配片段"
			}
		}
	}
	finished := time.Now().UTC()
	result.ProcessedAt = &finished
	cancel()
	<-monitorDone
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer finishCancel()
	enabled, err := p.store.Enabled(finishCtx, job)
	if err != nil || !enabled || parent.Err() != nil || interrupted.Load() {
		if err := p.store.Finish(finishCtx, job, "queued", nil, nil); err != nil && !errors.Is(err, ErrRunLeaseLost) {
			pkg.Warn("AI credential requeue failed")
		}
		return
	}
	sealed, err := json.Marshal(result)
	if previousCorrupt {
		sealed = job.PreviousSealed
		err = nil
	} else if err == nil {
		sealed, err = p.codec.Seal(intelligenceScope(job.TaskID, "credential")+"/"+job.Fingerprint+"/analysis", string(sealed))
	}
	if err != nil {
		result.State = "error"
		result.Error = "无法加密整理结果"
		sealed = nil
	}
	metadata, _ := json.Marshal(map[string]any{"state": result.State, "method": result.Method, "error": result.Error, "note": result.Note, "model": result.Model, "input_tokens": result.InputTokens, "output_tokens": result.OutputTokens, "processed_at": finished, "traffic_state": result.TrafficState, "traffic_epoch": trafficEpoch})
	if result.State == "ready" && len(sealed) == 0 {
		result.State = "error"
	}
	if err := p.store.Finish(finishCtx, job, result.State, sealed, metadata); errors.Is(err, ErrCredentialBindingStateChanged) {
		if requeueErr := p.store.Finish(finishCtx, job, "queued", nil, nil); requeueErr != nil && !errors.Is(requeueErr, ErrRunLeaseLost) {
			pkg.Warn("AI credential traffic requeue failed")
		}
	} else if err != nil && !errors.Is(err, ErrRunLeaseLost) {
		pkg.Warn("AI credential result persistence failed")
	}
}

func (f *IntelligenceFacade) openCredentialAnalysis(item *TaskIntelligence) error {
	if len(item.AnalysisSealed) == 0 {
		return nil
	}
	if f.codec == nil {
		return errors.New("credential encryption unavailable")
	}
	value, err := f.codec.Open(intelligenceScope(item.TaskID, "credential")+"/"+item.Fingerprint+"/analysis", item.AnalysisSealed)
	if err != nil {
		return err
	}
	var result artexdb.CredentialAnalysis
	if err = json.Unmarshal([]byte(value), &result); err != nil {
		return fmt.Errorf("invalid credential analysis")
	}
	item.Credentials = &result
	return nil
}

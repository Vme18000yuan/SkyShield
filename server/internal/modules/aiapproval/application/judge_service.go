package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
)

func DefaultJudgeConfig() JudgeConfig {
	return JudgeConfig{Prompt: intercept.DefaultJudgePrompt, TimeoutSeconds: 15, RetryCount: 3, RetryIntervalMS: 1000, FailAction: "ask", AskTimeoutSeconds: 300, AskTimeoutAction: "deny", IncompleteScriptAction: "model"}
}
func validJudge(c JudgeConfig) bool {
	if c.ProfileID != nil {
		id, err := uuid.Parse(*c.ProfileID)
		if err != nil || id.String() != *c.ProfileID {
			return false
		}
	}
	return len(c.Prompt) <= 128*1024 && utf8.ValidString(c.Prompt) && !strings.ContainsRune(c.Prompt, '\x00') && c.TimeoutSeconds >= 1 && c.TimeoutSeconds <= 2147483647 && c.RetryCount >= 0 && c.RetryCount <= intercept.MaxJudgeRetryCount && c.RetryIntervalMS >= 0 && c.RetryIntervalMS <= intercept.MaxJudgeRetryIntervalMS && (c.FailAction == "allow" || c.FailAction == "ask" || c.FailAction == "deny") && c.AskTimeoutSeconds >= 1 && c.AskTimeoutSeconds <= 2147483647 && (c.AskTimeoutAction == "allow" || c.AskTimeoutAction == "deny") && (c.IncompleteScriptAction == "ask" || c.IncompleteScriptAction == "model")
}
func (s *Service) JudgeConfig(ctx context.Context, user int) (JudgeConfig, error) {
	if user <= 0 {
		return JudgeConfig{}, ErrForbidden
	}
	return s.store.ReadJudge(ctx, user, true)
}
func (s *Service) SaveJudge(ctx context.Context, user int, c JudgeConfig) error {
	if user <= 0 || !validJudge(c) {
		return ErrInvalidInput
	}
	// Persist an empty override for the built-in template so future ARTEX
	// prompt updates reach installations that never customized it.
	if strings.TrimSpace(c.Prompt) == "" || strings.TrimSpace(c.Prompt) == strings.TrimSpace(intercept.DefaultJudgePrompt) {
		c.Prompt = ""
	}
	return s.store.SaveJudge(ctx, user, c)
}
func (s *Service) JudgeUsage(ctx context.Context, user, days int) (JudgeUsage, error) {
	if user <= 0 || days < 1 || days > 365 {
		return JudgeUsage{}, ErrInvalidInput
	}
	return s.store.JudgeUsage(ctx, user, days)
}
func (s *Service) Judge(ctx context.Context, user int, name string, input json.RawMessage, tc *tool.ToolContext) (*Rule, bool, error) {
	cfg, err := s.store.ReadJudge(ctx, user, false)
	if err != nil {
		return nil, false, err
	}
	if !cfg.Enabled {
		return nil, false, nil
	}
	rule := &Rule{Name: "模型兜底审批", Action: "ask", TimeoutEnabled: true, TimeoutSeconds: cfg.AskTimeoutSeconds, TimeoutAction: cfg.AskTimeoutAction, DecisionSource: "model", JudgeProfileID: cfg.ProfileID}
	cfgJSON, _ := json.Marshal(cfg)
	cfgHash := sha256.Sum256(cfgJSON)
	rule.ConfigDigest = hex.EncodeToString(cfgHash[:])
	var review intercept.ReviewInput
	if s.capture == nil {
		err = errors.New("review capture unavailable")
	} else {
		captureCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutSeconds)*time.Second)
		review, err = s.capture(captureCtx, user, name, input, tc)
		cancel()
	}
	if err != nil {
		rule.Action = "ask"
		rule.Message = "[模型] 审查上下文不完整，需要人工确认"
		rule.ModelFallback = true
		return rule, true, nil
	}
	rule.ModelInput, _ = json.Marshal(review)
	hash := sha256.Sum256(rule.ModelInput)
	rule.ModelInputDigest = hex.EncodeToString(hash[:])
	verdict := intercept.Verdict{}
	var reviewErr error
	for attempt := 0; attempt <= cfg.RetryCount; attempt++ {
		if ctx.Err() != nil {
			return nil, true, ctx.Err()
		}
		if attempt > 0 && cfg.RetryIntervalMS > 0 {
			timer := time.NewTimer(time.Duration(cfg.RetryIntervalMS) * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, true, ctx.Err()
			case <-timer.C:
			}
		}
		var text string
		var usage llm.Usage
		attempted := false
		if s.reviewer == nil {
			reviewErr = errors.New("review model unavailable")
		} else {
			attemptCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutSeconds)*time.Second)
			text, usage, attempted, reviewErr = s.reviewer(attemptCtx, user, cfg.ProfileID, intercept.EffectiveJudgePrompt(cfg.Prompt), review)
			if attemptCtx.Err() != nil {
				reviewErr = attemptCtx.Err()
			}
			cancel()
		}
		if attempted {
			usageCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = s.store.AddJudgeUsage(usageCtx, user, cfg.ProfileID, usage)
			cancel()
		}
		if reviewErr == nil {
			verdict = intercept.ParseVerdict(text)
			if verdict.Action != "" {
				break
			}
			reviewErr = errors.New("invalid model verdict")
		}
	}
	if reviewErr != nil {
		rule.Action = cfg.FailAction
		rule.Message = "[模型] 模型审批失败，按失败策略处理"
		rule.ModelFallback = true
	} else {
		rule.Action = verdict.Action
		rule.Message = "[模型] " + verdict.Reason
	}
	if review.ScriptCaptureStatus == "incomplete" && cfg.IncompleteScriptAction == "ask" && rule.Action == "allow" {
		rule.Action = "ask"
		rule.Message = "[模型] 脚本未能完整附加，需要人工确认"
	}
	return rule, true, nil
}

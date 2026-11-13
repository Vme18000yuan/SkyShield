package airuntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/llm"
)

type JudgeModelResolver func(context.Context, int, *string) (llm.Provider, error)

func NewApprovalReviewer(resolve JudgeModelResolver) func(context.Context, int, *string, string, intercept.ReviewInput) (string, llm.Usage, bool, error) {
	return func(ctx context.Context, user int, profile *string, prompt string, input intercept.ReviewInput) (string, llm.Usage, bool, error) {
		if resolve == nil {
			return "", llm.Usage{}, false, errors.New("judge model unavailable")
		}
		provider, err := resolve(ctx, user, profile)
		if err != nil || provider == nil {
			return "", llm.Usage{}, false, errors.New("judge model unavailable")
		}
		raw, err := json.Marshal(input)
		if err != nil {
			return "", llm.Usage{}, false, err
		}
		temperature := 0.0
		request := llm.CompletionRequest{System: []string{prompt}, Messages: []llm.Message{llm.UserText(string(raw))}, MaxTokens: 1024, Temperature: &temperature, Thinking: "disabled"}
		var text strings.Builder
		acc := llm.NewAccumulator()
		for event, err := range provider.Stream(WithLLMUsageRole(ctx, "judge"), request) {
			if err != nil {
				return "", acc.Usage, true, err
			}
			acc.Add(event)
			if event.Type == llm.SETextDelta {
				if text.Len()+len(event.Text) > 128*1024 {
					return "", acc.Usage, true, errors.New("judge response too large")
				}
				text.WriteString(event.Text)
			}
		}
		if ctx.Err() != nil {
			return "", acc.Usage, true, ctx.Err()
		}
		return text.String(), acc.Usage, true, nil
	}
}

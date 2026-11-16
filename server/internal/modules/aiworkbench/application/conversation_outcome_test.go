package application

import (
	"context"
	"errors"
	"testing"
)

func TestConversationTerminalReasonsRemainDistinct(t *testing.T) {
	for _, tc := range []struct {
		reason string
		err    error
		status string
	}{{"completed", nil, "completed"}, {"timeout", nil, "timeout"}, {"max_turns", nil, "exhausted"}, {"run_budget", nil, "exhausted"}, {"run_budget", errors.New("settlement API failure"), "exhausted"}, {"persistence_error", errors.New("disk"), "failed"}, {"", context.Canceled, "interrupted"}} {
		status, _ := conversationOutcome(tc.reason, tc.err)
		if status != tc.status {
			t.Fatalf("%s mapped to %s, want %s", tc.reason, status, tc.status)
		}
	}
}

func TestMissingSummaryIsNotSuccessfulCompletionOrBudgetExhaustion(t *testing.T) {
	for _, err := range []error{nil, errors.New("empty model response"), context.DeadlineExceeded} {
		status, message := conversationOutcome("empty_response", err)
		if status != "failed" || message != "模型未能生成文字总结；已完成的工具结果仍保留，可在当前会话要求仅汇总已有结果。" {
			t.Fatalf("empty summary outcome: status=%s message=%q", status, message)
		}
	}
}

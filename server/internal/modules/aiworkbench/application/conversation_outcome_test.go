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

package llm

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

type budgetRejectTransport struct{ calls int }

func (t *budgetRejectTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls++
	return nil, &RunBudgetError{Kind: "tokens", Used: 101, Limit: 100}
}
func TestLocalBudgetErrorIsNeverRetried(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "stream"}[stream], func(t *testing.T) {
			transport := &budgetRejectTransport{}
			p, err := NewProvider(Config{Format: FormatOpenAI, BaseURL: "http://budget.invalid/v1", APIKey: "fixture", Model: "fixture", HTTPClient: &http.Client{Transport: transport}, MaxRetries: 9, RetryInterval: time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			if stream {
				for _, e := range p.Stream(context.Background(), CompletionRequest{Messages: []Message{UserText("fixture")}}) {
					if e != nil {
						err = e
					}
				}
			} else {
				_, _, _, err = p.Complete(context.Background(), CompletionRequest{Messages: []Message{UserText("fixture")}})
			}
			if !errors.Is(err, ErrRunBudget) || transport.calls != 1 {
				t.Fatalf("local budget was retried %d times: %v", transport.calls, err)
			}
		})
	}
}

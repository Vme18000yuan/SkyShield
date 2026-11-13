package llmpool

import (
	"context"
	"fmt"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

func TestLocalRunBudgetCannotTripFailover(t *testing.T) {
	if shouldFailover(context.Background(), fmt.Errorf("wrapped: %w", llm.ErrRunBudget)) {
		t.Fatal("local budget treated as unhealthy API")
	}
}

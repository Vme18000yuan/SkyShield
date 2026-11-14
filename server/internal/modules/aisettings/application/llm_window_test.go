package application

import (
	"testing"

	artexdb "github.com/Autumn-27/artex/db"
)

func TestDefaultAndConfiguredProfileWindows(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configured int
		maxTokens  int
		window     int
		valid      bool
	}{
		{"default", 0, 50_000, 256_000, true},
		{"custom 32K rejects larger output", 32, 50_000, 32_000, false},
		{"custom 64K", 64, 50_000, 64_000, true},
		{"custom 800K", 800, 50_000, 800_000, true},
		{"output cannot equal window", 0, 256_000, 256_000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := DefaultLLMOptions()
			options.ContextWindowK, options.MaxTokens = tc.configured, tc.maxTokens
			err := options.Normalize("openai")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
			config := options.AgentConfig("openai", "fixture", "", "", "", artexdb.LLMRetryPolicy{})
			if got := config.CompactionWindow(); got != tc.window {
				t.Fatalf("window = %d, want %d", got, tc.window)
			}
		})
	}
}

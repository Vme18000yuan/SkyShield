package agent

import "testing"

func TestCompactionWindowDefaultsAndExplicitOverrides(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configured int
		want       int
	}{
		{"unset", 0, 256_000},
		{"negative", -1, 256_000},
		{"minimum", 32, 32_000},
		{"custom", 64, 64_000},
		{"large custom", 800, 800_000},
		{"maximum", 1200, 1_000_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := (Config{ContextWindowK: tc.configured}).CompactionWindow(); got != tc.want {
				t.Fatalf("window = %d, want %d", got, tc.want)
			}
		})
	}
	if got := compactionConfig(0).ContextWindow; got != 256_000 {
		t.Fatalf("fallback compaction window = %d", got)
	}
}

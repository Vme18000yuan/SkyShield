package protocol

import "testing"

func TestExecutionRoots(t *testing.T) {
	tests := []struct {
		name, input, runtime, workspace string
	}{
		{"container defaults", "", "/run/lunafox", "/workspace"},
		{"native execution", "/tmp/skyshield/exec-1", "/tmp/skyshield/exec-1/runtime", "/tmp/skyshield/exec-1/workspace"},
		{"relative path", "relative", "", ""},
		{"unclean path", "/tmp/../tmp/engine", "", ""},
		{"filesystem root", "/", "", ""},
		{"whitespace", " /tmp/engine", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime, workspace := executionRoots(tt.input)
			if runtime != tt.runtime || workspace != tt.workspace {
				t.Fatalf("executionRoots(%q) = (%q, %q), want (%q, %q)", tt.input, runtime, workspace, tt.runtime, tt.workspace)
			}
		})
	}
}

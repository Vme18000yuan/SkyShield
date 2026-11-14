package airuntime

import "testing"

func TestTaskGoalAgentTurnLimit(t *testing.T) {
	definitions := map[string]ChatAgentDefinition{}
	if got := taskAgentMaxTurns(definitions, "goals", 0); got != 0 {
		t.Fatalf("default limit = %d, want unlimited", got)
	}
	definitions["goals"] = ChatAgentDefinition{MaxTurns: 3, RuntimeConfigured: true}
	if got := taskAgentMaxTurns(definitions, "goals", 0); got != 3 {
		t.Fatalf("configured limit = %d, want 3", got)
	}
	definitions["goals"] = ChatAgentDefinition{MaxTurns: 0, RuntimeConfigured: true}
	if got := taskAgentMaxTurns(definitions, "goals", 8); got != 0 {
		t.Fatalf("configured unlimited limit = %d, want 0", got)
	}
}

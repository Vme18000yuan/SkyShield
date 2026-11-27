package llm

import (
	"strings"
	"testing"
)

func TestRequestEstimateIncludesNestedSchemasAndSignedReasoning(t *testing.T) {
	req := CompletionRequest{Messages: []Message{{Role: RoleAssistant, Content: []ContentBlock{{Type: BlockThinking, Thinking: "reason", Signature: strings.Repeat("s", 7000)}}}},
		Tools: []ToolSchema{{Name: "fixture", InputSchema: map[string]any{"properties": map[string]any{"payload": map[string]any{"description": strings.Repeat("d", 7000)}}}}}}
	if got := EstimateRequestTokens(req); got < 4000 {
		t.Fatalf("omitted signature or nested schema: %d", got)
	}
	whole := EstimateRequestTokens(req)
	body := EstimateMessageTokens(req.Messages)
	static := EstimateRequestTokens(CompletionRequest{Tools: req.Tools})
	if whole > body+static {
		t.Fatal("split budgeting underestimated the assembled request")
	}
}

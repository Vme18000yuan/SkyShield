package llm

import "encoding/json"

// EstimateRequestTokens is a conservative local preflight estimate, not usage
// accounting. Routing and context management must use the same measurement.
func EstimateRequestTokens(req CompletionRequest) int {
	n := 0
	for _, s := range req.System {
		n += len(s)
	}
	for _, m := range req.Messages {
		n += contextBlockBytes(m.Content) + 16
	}
	for _, schema := range req.Tools {
		raw, _ := json.Marshal(schema.InputSchema)
		n += len(schema.Name) + len(schema.Description) + len(raw) + 24
	}
	return (n*2 + 6) / 7
}

func EstimateMessageTokens(messages []Message) int {
	return EstimateRequestTokens(CompletionRequest{Messages: messages})
}

func contextBlockBytes(blocks []ContentBlock) int {
	n := 0
	for _, b := range blocks {
		n += len(b.Text) + len(b.Thinking) + len(b.Input) + len(b.Name) + len(b.Signature) + len(b.ID) + len(b.ToolUseID) + 16
		n += contextBlockBytes(b.Content)
	}
	return n
}

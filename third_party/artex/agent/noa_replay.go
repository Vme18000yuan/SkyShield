package agent

import (
	"context"
	"encoding/json"

	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
)

// Preserve every retained model-generated tool input, including Compress's
// summaries. Only the outgoing view changes: tools are never invoked here and
// compressed-away exchanges remain represented by their archived summaries.
type replaySafeNoaCompactor struct {
	harness.Compactor
	window func() int
}

func (c *replaySafeNoaCompactor) SetRequestBudget(overhead, output int) {
	if c.window != nil {
		if setter, ok := c.Compactor.(interface{ SetContextWindow(int) }); ok {
			setter.SetContextWindow(c.window())
		}
	}
	if budget, ok := c.Compactor.(harness.RequestBudgeter); ok {
		budget.SetRequestBudget(overhead, output)
	}
}

func (c *replaySafeNoaCompactor) View(ctx context.Context, history []llm.Message) []llm.Message {
	view := c.Compactor.(harness.ContextView).View(ctx, history)
	type originalCall struct {
		block     llm.ContentBlock
		ambiguous bool
	}
	calls := map[string]originalCall{}
	for _, message := range history {
		if message.Role != llm.RoleAssistant {
			continue
		}
		for _, block := range message.Content {
			if block.Type != llm.BlockToolUse || block.ID == "" {
				continue
			}
			if previous, exists := calls[block.ID]; exists {
				previous.ambiguous = true
				calls[block.ID] = previous
			} else {
				calls[block.ID] = originalCall{block: block}
			}
		}
	}
	result := append([]llm.Message(nil), view...)
	for i, message := range result {
		if message.Role != llm.RoleAssistant {
			continue
		}
		content := append([]llm.ContentBlock(nil), message.Content...)
		for j, block := range content {
			if block.Type != llm.BlockToolUse {
				continue
			}
			original, exists := calls[block.ID]
			if exists && !original.ambiguous && original.block.Name == block.Name && json.Valid(original.block.Input) {
				content[j].Input = append([]byte(nil), original.block.Input...)
			}
		}
		result[i].Content = content
	}
	return result
}

var _ harness.ContextView = (*replaySafeNoaCompactor)(nil)

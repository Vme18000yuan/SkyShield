package agent

import (
	"context"
	"github.com/Autumn-27/norma/compaction"
	"github.com/Autumn-27/norma/llm"
)

type contextWindowProvider interface{ ContextWindowTokens() int }
type adaptiveCompactor struct {
	current   *compaction.Compactor
	config    compaction.Config
	window    func() int
	summarize compaction.Summarizer
}

func (c *adaptiveCompactor) refresh() {
	window := c.window()
	if window <= 0 {
		window = c.config.ContextWindow
	}
	if c.current == nil || window != c.config.ContextWindow {
		c.config.ContextWindow = window
		c.current = compaction.New(c.config, c.summarize)
	}
}
func (c *adaptiveCompactor) Pre(ctx context.Context, messages []llm.Message, tokens int) []llm.Message {
	c.refresh()
	return c.current.Pre(ctx, messages, tokens)
}
func (c *adaptiveCompactor) Reactive(ctx context.Context, messages []llm.Message) ([]llm.Message, bool) {
	c.refresh()
	return c.current.Reactive(ctx, messages)
}
func (c *adaptiveCompactor) IsOverflow(err error) bool { return compaction.IsOverflow(err) }

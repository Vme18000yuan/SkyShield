package noaadapter

import (
	"context"

	"github.com/Autumn-27/norma/compaction"
	"github.com/Autumn-27/norma/llm"
)

// Compactor adapts a Session to the harness's context-manager interfaces.
//
// It satisfies harness.Compactor and harness.ContextView. Implementing the
// latter is what takes over request construction from llm.MessagesForAPI.
type Compactor struct {
	sess *Session
	// overflow tracks what a prompt-too-long error taught us.
	overflow overflowState
}

func (c *Compactor) ContextWindowTokens() int { return c.sess.Config().ModelContextLimit }

// Pre is a no-op on the history.
//
// The built-in compaction rewrites messages here; noa must not. Message
// identity is a content hash over the stored text, and a rewrite would change
// every id derived from it, invalidating every ref the model holds.
func (c *Compactor) Pre(_ context.Context, msgs []llm.Message, lastInputTokens int) []llm.Message {
	if lastInputTokens > 0 {
		c.sess.noteProviderTokens(lastInputTokens)
	}
	return msgs
}

// View builds the request's message array.
func (c *Compactor) View(_ context.Context, msgs []llm.Message) []llm.Message {
	return c.sess.View(msgs)
}

// IsOverflow reports whether err is a context-overflow rejection. The built-in
// compaction already recognises every provider's phrasing, so the detection is
// borrowed rather than duplicated.
func (c *Compactor) IsOverflow(err error) bool {
	if !compaction.IsOverflow(err) {
		return false
	}
	c.overflow.learn(ParseOverflowWindow(err.Error()))
	return true
}

func (c *Compactor) SetRequestBudget(overhead, output int) {
	c.sess.mu.Lock()
	defer c.sess.mu.Unlock()
	c.sess.overhead = max(0, overhead)
	if output <= 0 {
		output = 8192
	}
	c.sess.outputReservation = output
}

// A task can switch model profiles between turns. The archive stays intact.
func (c *Compactor) SetContextWindow(window int) {
	if window <= 0 {
		return
	}
	c.sess.mu.Lock()
	defer c.sess.mu.Unlock()
	if c.sess.cfg.ModelContextLimit != window {
		c.sess.emergencyFloor = 0
		c.overflow.reset()
	}
	c.sess.cfg.ModelContextLimit = window
}

// Reactive recovers from a prompt-too-long error.
//
// noa cannot ask the model to compress here — the model is not in the loop at
// this point. What it can do is arm the mechanical valve and VERIFY the result:
// returning true without actually shrinking the view would produce a
// 413 → retry → 413 loop, which is worse than a clean failure. So the view is
// rebuilt and measured, and false is returned if it did not shrink.
func (c *Compactor) Reactive(ctx context.Context, msgs []llm.Message) ([]llm.Message, bool) {
	// Compare against the array the provider actually rejected, not the raw
	// projection. The projection is always larger than the sent view, so
	// measuring against it would show progress on every attempt and never admit
	// that arming changed nothing.
	before := c.sess.sentTokens()
	if before > 0 {
		before += c.sess.overhead + c.sess.outputReservation
	}

	c.overflow.arm(c.sess.Config().ModelContextLimit)
	floor := c.overflow.armedFloor()
	c.sess.setEmergencyFloor(floor)

	after := estimateMessages(c.View(ctx, msgs)) + c.sess.overhead + c.sess.outputReservation

	shrank := before > 0 && after < before
	fits := floor == 0 || after <= floor
	if !shrank || !fits {
		// Nothing more to give. Returning true here would promise a smaller
		// request that is not smaller, and the loop would retry into the same
		// rejection indefinitely; a clean prompt-too-long is the better outcome.
		c.sess.setEmergencyFloor(0)
		c.overflow.disarm()
		return msgs, false
	}
	return msgs, true
}

// estimateMessages sizes a rebuilt request.
func estimateMessages(msgs []llm.Message) int {
	return llm.EstimateMessageTokens(msgs)
}

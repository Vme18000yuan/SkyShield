package harness

import (
	"context"
	"crypto/rand"
	"sync"
	"time"
)

// ToolTiming is host-side observation data. It is never included in a
// completion request or transcript. Each occurrence is independent of the
// provider's tool ID, which some providers reuse.
type ToolTiming struct {
	Occurrence            string
	Round, Batch          int
	StartedAt, FinishedAt time.Time
	Interrupted           bool
}

type occurrenceKey struct{}

func ToolOccurrence(ctx context.Context) string {
	id, _ := ctx.Value(occurrenceKey{}).(string)
	return id
}

type toolClock struct {
	mu     sync.Mutex
	timing ToolTiming
	notify chan<- struct{}
}

func (e *streamExec) newClock() *toolClock {
	if !e.l.in.ObserveToolTiming {
		return nil
	}
	if e.batch == 0 {
		e.l.observedBatches++
		e.batch = e.l.observedBatches
	}
	return &toolClock{timing: ToolTiming{Occurrence: rand.Text(), Round: e.l.observedRounds, Batch: e.batch}, notify: e.notify}
}

func (c *toolClock) snapshot() *ToolTiming {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	v := c.timing
	return &v
}

func (c *toolClock) start(ctx context.Context) context.Context {
	c.mu.Lock()
	c.timing.StartedAt = time.Now()
	id := c.timing.Occurrence
	c.mu.Unlock()
	select {
	case c.notify <- struct{}{}:
	default:
	}
	return context.WithValue(ctx, occurrenceKey{}, id)
}

func (c *toolClock) finish() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timing.FinishedAt = c.timing.StartedAt.Add(time.Since(c.timing.StartedAt))
}

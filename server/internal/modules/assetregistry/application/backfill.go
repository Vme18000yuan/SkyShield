package application

import (
	"context"
	"sync"
	"time"

	"github.com/yyhuni/lunafox/server/internal/pkg"
	"go.uber.org/zap"
)

// BackfillController reconciles pre-existing records in bounded transactions.
type BackfillController struct {
	store Store
	once  sync.Once
	done  chan struct{}
}

func NewBackfillController(store Store) *BackfillController {
	return &BackfillController{store: store, done: make(chan struct{})}
}

func (c *BackfillController) Start(ctx context.Context) {
	c.once.Do(func() { go c.run(ctx) })
}

func (c *BackfillController) Done() <-chan struct{} { return c.done }

func (c *BackfillController) run(ctx context.Context) {
	defer close(c.done)
	for ctx.Err() == nil {
		done, err := c.store.BackfillBatch(ctx, 500)
		if err == nil && done {
			return
		}
		if err != nil {
			pkg.Error("Asset registry backfill failed", zap.Error(err))
		}
		if err != nil || !done {
			delay := 100 * time.Millisecond
			if err != nil {
				delay = 5 * time.Second
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}
}

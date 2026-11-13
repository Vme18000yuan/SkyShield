package application

import (
	"context"
	"sync"
	"time"

	"github.com/yyhuni/lunafox/server/internal/pkg"
	"go.uber.org/zap"
)

type ApplicationProjectionStore interface {
	ProcessApplicationBatch(context.Context, int) (bool, error)
}

// ApplicationProjectionController replays history and consumes durable Web
// changes without holding the scan's write transaction open.
type ApplicationProjectionController struct {
	store ApplicationProjectionStore
	once  sync.Once
	done  chan struct{}
}

func NewApplicationProjectionController(store ApplicationProjectionStore) *ApplicationProjectionController {
	return &ApplicationProjectionController{store: store, done: make(chan struct{})}
}
func (c *ApplicationProjectionController) Start(ctx context.Context) {
	c.once.Do(func() { go c.run(ctx) })
}
func (c *ApplicationProjectionController) Done() <-chan struct{} { return c.done }
func (c *ApplicationProjectionController) run(ctx context.Context) {
	defer close(c.done)
	for ctx.Err() == nil {
		worked, err := c.store.ProcessApplicationBatch(ctx, 200)
		delay := time.Second
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			pkg.Error("Application projection failed", zap.Error(err))
			delay = 5 * time.Second
		} else if worked {
			delay = 100 * time.Millisecond
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

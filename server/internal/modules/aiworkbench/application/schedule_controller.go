package application

import (
	"context"
	"sync"
	"time"

	"github.com/yyhuni/lunafox/server/internal/pkg"
)

// ScheduleController reconciles calendar windows with the same task queue and
// task leases used for manual task execution. PostgreSQL serializes all replicas.
type ScheduleController struct {
	store     ScheduleStore
	tasks     *TaskController
	startedAt time.Time
	once      sync.Once
	wake      chan struct{}
	done      chan struct{}
}

func NewScheduleController(store ScheduleStore, tasks *TaskController) *ScheduleController {
	return &ScheduleController{store: store, tasks: tasks, startedAt: time.Now(), wake: make(chan struct{}, 1), done: make(chan struct{})}
}
func (c *ScheduleController) Start(ctx context.Context) { c.once.Do(func() { go c.run(ctx) }) }
func (c *ScheduleController) Done() <-chan struct{}     { return c.done }
func (c *ScheduleController) Notify() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}
func (c *ScheduleController) NotifyTasks() {
	if c.tasks != nil {
		c.tasks.Notify()
	}
	c.Notify()
}
func (c *ScheduleController) run(ctx context.Context) {
	defer close(c.done)
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for ctx.Err() == nil {
		result, err := c.store.TickSchedules(ctx, time.Now(), c.startedAt)
		if err != nil && ctx.Err() == nil {
			pkg.Warn("AI schedule reconciliation failed")
		}
		if c.tasks != nil {
			for _, id := range result.PausedTaskIDs {
				c.tasks.CancelBySchedule(id)
			}
			if result.Changed {
				c.tasks.Notify()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-c.wake:
		}
	}
}

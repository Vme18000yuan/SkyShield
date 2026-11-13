package ainotification

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Autumn-27/artex/notify"
	pkg "github.com/yyhuni/lunafox/server/internal/pkg"
	"go.uber.org/zap"
)

type Worker struct {
	store *Store
	done  chan struct{}
	once  sync.Once
}

func NewWorker(store *Store) *Worker {
	if store == nil {
		panic("AI notification store is required")
	}
	return &Worker{store: store, done: make(chan struct{})}
}
func (w *Worker) Done() <-chan struct{}     { return w.done }
func (w *Worker) Start(ctx context.Context) { w.once.Do(func() { go w.run(ctx) }) }

func (w *Worker) run(ctx context.Context) {
	defer close(w.done)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		if err := w.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			pkg.Warn("AI notification worker cycle failed", zap.Error(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RunOnce bounds each fanout/send cycle; no network I/O occurs in a DB transaction.
func (w *Worker) RunOnce(ctx context.Context) error {
	for i := 0; i < 100; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		processed, err := w.store.FanOutOne(ctx)
		if err != nil {
			return err
		}
		if !processed {
			break
		}
	}
	for i := 0; i < 5; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		claim, err := w.store.ClaimNext(ctx)
		if err != nil {
			return err
		}
		if claim == nil {
			break
		}
		prepareCtx, prepareCancel := context.WithTimeout(ctx, 30*time.Second)
		items, err := w.store.EligibleClaim(prepareCtx, claim)
		if err != nil {
			prepareCancel()
			return err
		}
		if len(items) == 0 {
			prepareCancel()
			continue
		}
		config, sendErr := w.store.decrypt(claim.User, claim.Channel)
		channel, ok := notify.Get(claim.Channel.Kind)
		if sendErr != nil || !ok {
			sendErr = notify.Permanent(errors.New("notification channel configuration unavailable"))
		}
		if sendErr == nil {
			if err = channel.Validate(config); err != nil {
				sendErr = notify.Permanent(errors.New("notification channel configuration invalid"))
			}
		}
		var message notify.Message
		if sendErr == nil {
			message, sendErr = w.store.message(prepareCtx, claim, items)
		}
		if sendErr == nil {
			var current []claimedDelivery
			current, sendErr = w.store.EligibleClaim(prepareCtx, claim)
			if sendErr == nil && len(current) != len(items) {
				items = current
				if len(items) > 0 {
					message, sendErr = w.store.message(prepareCtx, claim, items)
				}
			}
		}
		prepareCancel()
		if len(items) == 0 {
			continue
		}
		kept := 0
		if sendErr == nil {
			sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			kept, sendErr = channel.Send(sendCtx, config, message)
			cancel()
			if sendErr == nil && kept == 0 {
				sendErr = errors.New("channel sent no items")
			}
		}
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		err = w.store.FinishClaim(finishCtx, claim, items, kept, sendErr)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

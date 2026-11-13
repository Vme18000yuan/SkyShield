package application

import (
	"context"
	"fmt"
	"github.com/Autumn-27/artex/sidequestion"
	"github.com/yyhuni/lunafox/server/internal/pkg"
	"sync"
	"time"
)

type sideSnapshotWrite struct {
	user     int
	parent   sidequestion.Parent
	run      string
	snapshot sidequestion.Snapshot
}
type sideSnapshotWriter struct {
	mu      sync.Mutex
	pending map[string]sideSnapshotWrite
	active  map[string]sideSnapshotWrite
	changed chan struct{}
	wake    chan struct{}
	done    chan struct{}
	store   SideQuestionStore
}

func newSideSnapshotWriter(ctx context.Context, store SideQuestionStore) *sideSnapshotWriter {
	w := &sideSnapshotWriter{pending: map[string]sideSnapshotWrite{}, active: map[string]sideSnapshotWrite{}, changed: make(chan struct{}), wake: make(chan struct{}, 1), done: make(chan struct{}), store: store}
	go w.run(ctx)
	return w
}
func (w *sideSnapshotWriter) notify() { close(w.changed); w.changed = make(chan struct{}) }
func (w *sideSnapshotWriter) put(item sideSnapshotWrite) {
	key := fmt.Sprintf("%d/%s/%s", item.user, item.parent.Key(), item.run)
	w.mu.Lock()
	if old, ok := w.pending[key]; ok && (old.snapshot.RunID > item.snapshot.RunID || old.snapshot.RunID == item.snapshot.RunID && old.snapshot.Version >= item.snapshot.Version) {
		w.mu.Unlock()
		return
	}
	// One latest snapshot per parent; bounded backlog under a slow database.
	if _, ok := w.pending[key]; !ok && len(w.pending) >= 32 {
		w.mu.Unlock()
		pkg.Warn("AI side snapshot backlog full; retaining last durable context")
		return
	}
	w.pending[key] = item
	w.notify()
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}
func (w *sideSnapshotWriter) run(ctx context.Context) {
	defer close(w.done)
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
		}
		for ctx.Err() == nil {
			w.mu.Lock()
			var key string
			var item sideSnapshotWrite
			for k, v := range w.pending {
				key, item = k, v
				break
			}
			if key == "" {
				w.mu.Unlock()
				break
			}
			delete(w.pending, key)
			w.active[key] = item
			w.mu.Unlock()
			write, stop := context.WithTimeout(ctx, 2*time.Second)
			err := w.store.SaveSideSnapshot(write, item.user, item.parent, item.run, item.snapshot)
			stop()
			if err != nil && ctx.Err() == nil {
				pkg.Warn("AI side snapshot persistence failed; main execution continues")
			}
			w.mu.Lock()
			delete(w.active, key)
			w.notify()
			w.mu.Unlock()
		}
	}
}
func (w *sideSnapshotWriter) flush(ctx context.Context, user int, run string) {
	if w == nil {
		return
	}
	for {
		w.mu.Lock()
		waiting := false
		for _, items := range []map[string]sideSnapshotWrite{w.pending, w.active} {
			for _, item := range items {
				if item.user == user && item.run == run {
					waiting = true
					break
				}
			}
		}
		changed := w.changed
		w.mu.Unlock()
		if !waiting {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-w.done:
			return
		case <-changed:
		}
	}
}
func (f *SideQuestionFacade) FlushSnapshots(user int, run string) {
	ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	f.snapshots.flush(ctx, user, run)
}

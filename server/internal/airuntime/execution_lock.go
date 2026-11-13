package airuntime

import (
	"context"
	"sync"
)

type executionLockEntry struct {
	gate chan struct{}
	refs int
}
type executionLocks struct {
	mu      sync.Mutex
	entries map[string]*executionLockEntry
}

// A replacement run waits for the previous local executor's cleanup, while its
// own cancellation/deadline remains responsive. Idle keys do not accumulate.
func (l *executionLocks) acquire(ctx context.Context, key string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	if l.entries == nil {
		l.entries = map[string]*executionLockEntry{}
	}
	entry := l.entries[key]
	if entry == nil {
		entry = &executionLockEntry{gate: make(chan struct{}, 1)}
		l.entries[key] = entry
	}
	entry.refs++
	l.mu.Unlock()
	releaseRef := func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		entry.refs--
		if entry.refs == 0 {
			delete(l.entries, key)
		}
	}
	select {
	case entry.gate <- struct{}{}:
	case <-ctx.Done():
		releaseRef()
		return nil, ctx.Err()
	}
	return func() { <-entry.gate; releaseRef() }, nil
}

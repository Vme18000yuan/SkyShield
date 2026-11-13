package application

import (
	"context"
	"github.com/Autumn-27/artex/sidequestion"
	"sync"
	"testing"
	"time"
)

type delayedSnapshotStore struct {
	SideQuestionStore
	started, release chan struct{}
	once             sync.Once
	mu               sync.Mutex
	versions         []int64
}

func (s *delayedSnapshotStore) SaveSideSnapshot(ctx context.Context, _ int, _ sidequestion.Parent, _ string, snapshot sidequestion.Snapshot) error {
	s.once.Do(func() { close(s.started) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.release:
	}
	s.mu.Lock()
	s.versions = append(s.versions, snapshot.Version)
	s.mu.Unlock()
	return nil
}
func TestSlowSideSnapshotDoesNotBlockMainAndCoalescesUpdates(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	store := &delayedSnapshotStore{started: make(chan struct{}), release: make(chan struct{})}
	w := newSideSnapshotWriter(ctx, store)
	parent := sidequestion.Parent{ConversationKey: "fixture"}
	w.put(sideSnapshotWrite{user: 7, parent: parent, run: "one", snapshot: sidequestion.Snapshot{RunID: 1, Version: 1}})
	<-store.started
	for i := int64(2); i <= 100; i++ {
		w.put(sideSnapshotWrite{user: 7, parent: parent, run: "one", snapshot: sidequestion.Snapshot{RunID: 1, Version: i}})
	}
	close(store.release)
	wait, stop := context.WithTimeout(ctx, time.Second)
	defer stop()
	w.flush(wait, 7, "one")
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.versions) != 2 || store.versions[1] != 100 {
		t.Fatalf("snapshots were not coalesced: %v", store.versions)
	}
}

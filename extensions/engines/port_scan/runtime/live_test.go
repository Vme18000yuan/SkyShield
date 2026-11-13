package portscanruntime

import (
	"context"
	"errors"
	contract "github.com/yyhuni/lunafox/engines/port_scan/contract"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type livePortCapture struct {
	saved chan struct{}
	items []contract.HostPort
}

func (c *livePortCapture) Submit(_ context.Context, items <-chan contract.HostPort) error {
	for item := range items {
		c.items = append(c.items, item)
		if c.saved != nil {
			close(c.saved)
			c.saved = nil
		}
	}
	return nil
}
func TestLivePortIsSavedBeforeCommandFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ports")
	saved := make(chan struct{})
	capture := &livePortCapture{saved: saved}
	failure := errors.New("later port failure")
	err := NewWithResults(capture).executeLive(context.Background(), path, func(ctx context.Context) error {
		if err := os.WriteFile(path, []byte("{\"host\":\"api.example.com\",\"ip\":\"192.0.2.1\",\"port\":443}\n"), 0o600); err != nil {
			return err
		}
		select {
		case <-saved:
			return failure
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
			return errors.New("port result waited for command exit")
		}
	})
	if !errors.Is(err, failure) || len(capture.items) != 1 {
		t.Fatalf("%v %+v", err, capture.items)
	}
}

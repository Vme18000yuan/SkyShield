package directoryscanruntime

import (
	"context"
	"errors"
	contract "github.com/yyhuni/lunafox/engines/directory_scan/contract"
	"os"
	"testing"
	"time"
)

type liveTestFFUFRunner struct {
	saved   <-chan struct{}
	failure error
}

func (r liveTestFFUFRunner) Run(ctx context.Context, invocation ffufInvocation) error {
	path, err := rawFFUFArtifactPath(invocation.Workspace, invocation.Candidate.Ordinal)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte("{\"url\":\"https://example.com/admin\",\"status\":200,\"length\":8,\"content-type\":\"text/plain\",\"duration\":10}\n"), 0o600); err != nil {
		return err
	}
	select {
	case <-r.saved:
		return r.failure
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(3 * time.Second):
		return errors.New("directory result waited for tool exit")
	}
}

type liveDirectoryCapture struct {
	saved chan struct{}
	items []contract.Directory
}

func (c *liveDirectoryCapture) Submit(_ context.Context, items <-chan contract.Directory) error {
	for item := range items {
		c.items = append(c.items, item)
		if c.saved != nil {
			close(c.saved)
			c.saved = nil
		}
	}
	return nil
}

func TestDirectoryObservationIsDeliveredBeforeLaterToolFailure(t *testing.T) {
	saved := make(chan struct{})
	capture := &liveDirectoryCapture{saved: saved}
	failure := errors.New("later ffuf failure")
	runner := liveFFUFRunner{inner: liveTestFFUFRunner{saved: saved, failure: failure}, submitter: capture}
	err := runner.Run(context.Background(), ffufInvocation{Workspace: t.TempDir()})
	if !errors.Is(err, failure) || len(capture.items) != 1 || capture.items[0].URL != "https://example.com/admin" {
		t.Fatalf("%v %+v", err, capture.items)
	}
}

package websitediscoveryruntime

import (
	"context"
	"errors"
	contract "github.com/yyhuni/lunafox/engines/website_discovery/contract"
	"os"
	"testing"
	"time"
)

type liveWebsiteCapture struct {
	saved chan struct{}
	items []contract.WebsiteV2
}

func (c *liveWebsiteCapture) Submit(_ context.Context, items <-chan contract.WebsiteV2) error {
	for item := range items {
		c.items = append(c.items, item)
		if c.saved != nil {
			close(c.saved)
			c.saved = nil
		}
	}
	return nil
}

type liveHTTPXExecutor struct {
	saved   <-chan struct{}
	failure error
}

func (e liveHTTPXExecutor) run(ctx context.Context, cmd httpxCommand) error {
	var output string
	for i, arg := range cmd.args {
		if arg == "-o" {
			output = cmd.args[i+1]
		}
	}
	if err := os.WriteFile(output, []byte("{\"input\":\"https://example.com/Case%2FPath\",\"host\":\"example.com\",\"status_code\":200}\n"), 0o600); err != nil {
		return err
	}
	select {
	case <-e.saved:
		return e.failure
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(3 * time.Second):
		return errors.New("website result waited for process exit")
	}
}

func TestWebsiteIsSavedDuringProbeAndRetainedOnLaterFailure(t *testing.T) {
	saved := make(chan struct{})
	capture := &liveWebsiteCapture{saved: saved}
	failure := errors.New("later httpx failure")
	run := &websiteDiscoveryRun{config: contract.Config{HTTPX: contract.HTTPXConfig{Enabled: true, Timeout: 60}}, urlListPath: "input", urlCount: 1, workDir: t.TempDir(), progress: discardingProgress{}, executor: liveHTTPXExecutor{saved: saved, failure: failure}}
	_, err := NewWithResults(capture).runHTTPXStage(context.Background(), run)
	if !errors.Is(err, failure) || len(capture.items) != 1 || capture.items[0].URL != "https://example.com/Case%2FPath" {
		t.Fatalf("%v %+v", err, capture.items)
	}
}

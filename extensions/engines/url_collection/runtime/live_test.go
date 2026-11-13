package urlcollectionruntime

import (
	"context"
	"errors"
	"fmt"
	contract "github.com/yyhuni/lunafox/engines/url_collection/contract"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type liveCapture struct {
	items []contract.EndpointV2
	saved chan struct{}
}

func (c *liveCapture) Submit(ctx context.Context, items <-chan contract.EndpointV2) error {
	for item := range items {
		c.items = append(c.items, item)
		if c.saved != nil {
			close(c.saved)
			c.saved = nil
		}
	}
	return ctx.Err()
}

func TestLiveURLResultsPersistBeforeCollectorFinishesAndSurviveItsFailure(t *testing.T) {
	workspace := t.TempDir()
	input := filepath.Join(t.TempDir(), "input")
	_ = os.WriteFile(input, []byte("https://example.com\n"), 0o600)
	saved := make(chan struct{})
	capture := &liveCapture{saved: saved}
	execution := runtimeProgressExecution(workspace, input, noOpProgress{}, nil)
	execution.Results.EndpointV2 = capture
	failure := errors.New("collector failed after producing valid URLs")
	runtime := Runtime{live: true, executor: collectorTestExecutor{runFn: func(ctx context.Context, cmd toolCommand) error {
		output, _ := toolArgument(cmd.args, "-o")
		switch cmd.name {
		case "katana":
			if err := os.WriteFile(output, []byte("https://example.com/Case%2FPath?b=2&a=1\n"), 0o600); err != nil {
				return err
			}
			if cmd.liveOutput != "" {
				if err := os.WriteFile(cmd.liveOutput, []byte("https://example.com/Case%2FPath?b=2&a=1\n"), 0o600); err != nil {
					return err
				}
			}
			select {
			case <-saved:
				return failure
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
				return errors.New("results waited for collector completion")
			}
		case "uro":
			input, _ := toolArgument(cmd.args, "-i")
			raw, err := os.ReadFile(input)
			if err != nil {
				return err
			}
			return os.WriteFile(output, raw, 0o600)
		case "httpx":
			input, _ := toolArgument(cmd.args, "-list")
			raw, err := os.ReadFile(input)
			if err != nil {
				return err
			}
			return os.WriteFile(output, []byte(fmt.Sprintf("{\"input\":%q,\"host\":\"example.com\",\"status_code\":200}\n", strings.TrimSpace(string(raw)))), 0o600)
		}
		return errors.New("unexpected tool")
	}}}
	err := runtime.Execute(context.Background(), execution)
	if !errors.Is(err, failure) || len(capture.items) != 1 || capture.items[0].URL != "https://example.com/Case%2FPath?b=2&a=1" {
		t.Fatalf("err=%v results=%+v", err, capture.items)
	}
}

func TestKatanaUsesIndependentInputBudgetAndParallelism(t *testing.T) {
	cmd, err := buildKatanaCommand("seeds", "out", contract.KatanaConfig{Timeout: 1800, Depth: 2, Concurrency: 10, RateLimit: 30, RequestTimeout: 5, CrawlDuration: 120, Parallelism: 6})
	if err != nil {
		t.Fatal(err)
	}
	for flag, want := range map[string]string{"-ct": "120s", "-p": "6", "-timeout": "5"} {
		if got, _ := toolArgument(cmd.args, flag); got != want {
			t.Fatalf("%s=%q want %q", flag, got, want)
		}
	}
}

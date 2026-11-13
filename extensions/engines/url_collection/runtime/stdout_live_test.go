package urlcollectionruntime

import (
	"context"
	"fmt"
	contract "github.com/yyhuni/lunafox/engines/url_collection/contract"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fileSignalEndpoints struct {
	marker string
	count  int
}

func (c *fileSignalEndpoints) Submit(_ context.Context, items <-chan contract.EndpointV2) error {
	for range items {
		c.count++
		if err := os.WriteFile(c.marker, []byte("saved"), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// Reproduce the pinned Katana writer: stdout is immediate, but -o stays
// buffered until the tool exits. Delivery must unblock it before that exit.
func TestLiveKatanaStdoutDoesNotWaitForBufferedOutputFile(t *testing.T) {
	workspace := t.TempDir()
	bin := t.TempDir()
	marker := filepath.Join(workspace, "saved")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
out=""
while [ "$#" -gt 0 ]; do case "$1" in -o) out="$2"; shift 2;; *) shift;; esac; done
printf 'https://example.com/early\n'
while [ ! -f '%s' ]; do sleep 0.05; done
printf 'https://example.com/early\n' > "$out"
`, marker)
	if err := os.WriteFile(filepath.Join(bin, "katana"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	input := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(input, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	execution := runtimeProgressExecution(workspace, input, noOpProgress{}, nil)
	execution.Config.Uro.Enabled = false
	execution.Config.HTTPX.Enabled = false
	capture := &fileSignalEndpoints{marker: marker}
	execution.Results.EndpointV2 = capture
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := New().Execute(ctx, execution); err != nil {
		t.Fatal(err)
	}
	if capture.count != 1 {
		t.Fatalf("expected one early result, got %d", capture.count)
	}
}

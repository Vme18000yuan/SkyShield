package ailocal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/norma/tool"
)

func TestBrokenToolProtocolDoesNotLeaveCallWaiting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX helper process")
	}
	if _, err := os.Stat("/usr/bin/python3"); err != nil {
		t.Skip("Python fixture unavailable")
	}
	for _, failure := range []string{"malformed", "closed stream", "unresponsive"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			executable := filepath.Join(root, "worker-fixture")
			code := "print('invalid JSON', flush=True)"
			if failure == "closed stream" {
				code = "os.close(1)"
			}
			if failure == "unresponsive" {
				code = "print('{', end='', flush=True)"
			}
			program := "#!/usr/bin/python3\nimport os,sys,time\nprint('{\"ready\":true}', flush=True)\nsys.stdin.readline()\n" + code + "\ntime.sleep(60)\n"
			if err := os.WriteFile(executable, []byte(program), 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client, err := NewClient(ctx, root, executable, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if failure == "unresponsive" {
				client.responseTimeout = 20 * time.Millisecond
			}
			started := time.Now()
			_, err = client.Call(ctx, "probe", []byte(`{}`), nil)
			want := ErrTransportLost
			if failure == "unresponsive" {
				want = ErrResponseTimeout
			}
			if !errors.Is(err, want) || time.Since(started) > 2*time.Second {
				t.Fatalf("call waited instead of surfacing transport loss: %v", err)
			}
			select {
			case <-client.done:
			case <-time.After(3 * time.Second):
				t.Fatal("broken worker was left running")
			}
			if _, err := client.Call(ctx, "probe", []byte(`{}`), nil); !errors.Is(err, ErrTransportLost) {
				t.Fatal("dead worker lost its explicit transport failure")
			}
		})
	}
}

func TestBlockedRequestWriteHonorsWatchdog(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture")
	}
	root := t.TempDir()
	executable := filepath.Join(root, "worker-fixture")
	if err := os.WriteFile(executable, []byte("#!/usr/bin/python3\nimport time\nprint('{\"ready\":true}',flush=True)\ntime.sleep(60)\n"), 0700); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(t.Context(), root, executable, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.responseTimeout = 30 * time.Millisecond
	start := time.Now()
	_, err = client.Call(t.Context(), "Write", []byte(`{"content":"`+strings.Repeat("x", 1<<20)+`"}`), nil)
	if !errors.Is(err, ErrResponseTimeout) || time.Since(start) > time.Second {
		t.Fatalf("blocked send escaped watchdog: %v %s", err, time.Since(start))
	}
}

func TestCompleteToolResultSurvivesLaterTransportClose(t *testing.T) {
	responses := make(chan Response, 1)
	responses <- Response{Result: tool.Text("completed before closure")}
	result, err := interruptedLocalTool(context.Background(), responses)
	if err != nil || result.IsError || result.Flatten() != "completed before closure" {
		t.Fatal("lost a complete result")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	responses <- Response{Result: tool.Text("completed")}
	if _, err := interruptedLocalTool(ctx, responses); !errors.Is(err, context.Canceled) {
		t.Fatal("transport recovery ignored cancellation")
	}
}

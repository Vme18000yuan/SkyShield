package ailocal

import (
	"context"
	"encoding/json"
	"github.com/Autumn-27/norma/tool"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestConfinedNativeFileBashAndPTY(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux sandbox")
	}
	executable := filepath.Join(t.TempDir(), "ai-tool-worker")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", executable, "./cmd/ai-tool-worker")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build worker: %v %s", err, output)
	}
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(outside, []byte("host-private-sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TIANQIONG_LLM_API_KEY", "host-secret-sentinel")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := NewClient(ctx, root, executable, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	tc := &tool.ToolContext{WorkingDir: root, OutputDir: filepath.Join(root, "cmd-output")}
	call := func(name string, input any) tool.Result {
		t.Helper()
		raw, _ := json.Marshal(input)
		result, err := client.Call(ctx, name, raw, tc)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	result := call("Write", map[string]any{"file_path": "sample.txt", "content": "local file contents"})
	if result.IsError {
		t.Fatal(result.Flatten())
	}
	result = call("Read", map[string]any{"file_path": "sample.txt"})
	if result.IsError || !strings.Contains(result.Flatten(), "local file contents") {
		t.Fatal(result.Flatten())
	}
	result = call("Bash", map[string]any{"command": "printf 'fixture failed'; exit 7"})
	if !result.IsError || !strings.Contains(result.Flatten(), "exit code 7") {
		t.Fatal("non-zero exit was not returned as a tool error")
	}
	result = call("Bash", map[string]any{"command": "sleep 0.2", "timeout_ms": 1})
	if !result.IsError || !strings.Contains(result.Flatten(), "timed out") {
		t.Fatal("per-tool timeout did not return an error result")
	}
	result = call("Read", map[string]any{"file_path": "missing-fixture.txt"})
	if !result.IsError {
		t.Fatal("missing file was presented as success")
	}
	result = call("Bash", map[string]any{"command": "printf 'before\\000after'"})
	if result.IsError || !strings.ContainsRune(result.Flatten(), 0) {
		t.Fatal("binary output was lost in the tool transport")
	}
	result = call("Bash", map[string]any{"command": "printf 'worker still healthy'"})
	if result.IsError || !strings.Contains(result.Flatten(), "worker still healthy") {
		t.Fatal("ordinary failures stopped the local worker")
	}
	result = call("Bash", map[string]any{"command": "python3 -c 'import os; print(os.getenv(\"TIANQIONG_LLM_API_KEY\", \"clean\"))'"})
	if result.IsError || !strings.Contains(result.Flatten(), "clean") || strings.Contains(result.Flatten(), "host-secret") {
		t.Fatalf("inherited secrets: %s", result.Flatten())
	}
	result = call("Read", map[string]any{"file_path": outside})
	if !result.IsError || strings.Contains(result.Flatten(), "host-private-sentinel") {
		t.Fatalf("host file exposed: %s", result.Flatten())
	}
	result = call("Bash", map[string]any{"command": "cat '" + outside + "'"})
	if !result.IsError || strings.Contains(result.Flatten(), "host-private-sentinel") {
		t.Fatalf("shell escaped: %s", result.Flatten())
	}
	result = call("shell_open", map[string]any{"command": "python3 -q", "mode": "hands-free"})
	if result.IsError {
		t.Fatal(result.Flatten())
	}
	var session struct {
		ID string `json:"session_id"`
	}
	if err = json.Unmarshal([]byte(result.Flatten()), &session); err != nil || session.ID == "" {
		t.Fatalf("PTY open: %s %v", result.Flatten(), err)
	}
	result = call("shell_send", map[string]any{"session_id": session.ID, "text": "print(6 * 7)", "submit": true, "wait": false})
	if result.IsError {
		t.Fatal(result.Flatten())
	}
	deadline := time.Now().Add(3 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		result = call("shell_read", map[string]any{"session_id": session.ID})
		if strings.Contains(result.Flatten(), "42") {
			found = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !found {
		t.Fatalf("PTY output: %s", result.Flatten())
	}
	if result = call("shell_close", map[string]any{"session_id": session.ID}); result.IsError {
		t.Fatal(result.Flatten())
	}
}

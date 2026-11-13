package aitraffic_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/aitraffic"
	"github.com/yyhuni/lunafox/server/internal/httpoutbound"
)

func TestRecordingRouteCapturesRelayAndCustomHTTP(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); _, _ = io.WriteString(w, "route-fixture") }))
	defer target.Close()
	manager, err := aitraffic.NewManager(t.TempDir(), func(*http.Request) (*url.URL, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	lease, err := manager.Acquire(t.Context(), aitraffic.Scope{TenantID: uuid.NewString(), UserID: 1, TaskID: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	var enabled atomic.Bool
	enabled.Store(true)
	var rejected atomic.Bool
	ctx, err := httpoutbound.WithRecordingRoute(t.Context(), lease.ProxyURL(), lease.CABundle(), func(context.Context) (bool, error) {
		if rejected.Load() {
			return false, errors.New("lease lost")
		}
		return enabled.Load(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	relay, err := httpoutbound.NewLocalRelay(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	proxyURL, err := url.Parse(relay.URL())
	if err != nil {
		t.Fatal(err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	if err = httpoutbound.ApplyRecordingTrust(ctx, transport); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, err := client.Get(target.URL + "/relay")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	client.CloseIdleConnections()
	exec, _ := json.Marshal(map[string]any{"url": target.URL + "/custom", "method": "GET"})
	custom := airuntime.BuildCustomTool(artexdb.Tool{Key: "fixture_http", Kind: "http", Exec: exec, Schema: json.RawMessage(`{"type":"object","properties":{}}`)}, nil)
	result, err := custom.Call(ctx, json.RawMessage(`{}`), &tool.ToolContext{ToolUseID: "fixture"})
	if err != nil || result.IsError {
		t.Fatalf("custom HTTP capture: %+v %v", result, err)
	}
	rows, err := lease.Recorder().Search("127.0.0.1", 0, 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("recorded paths: %+v %v", rows, err)
	}
	enabled.Store(false)
	// A tool started while capture is off follows the original outbound route.
	// Use plain HTTP here because the local test server's TLS cert is untrusted.
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); _, _ = io.WriteString(w, "direct") }))
	defer plain.Close()
	offExec, _ := json.Marshal(map[string]any{"url": plain.URL + "/disabled", "method": "GET"})
	off := airuntime.BuildCustomTool(artexdb.Tool{Key: "off_http", Kind: "http", Exec: offExec, Schema: json.RawMessage(`{}`)}, nil)
	result, err = off.Call(ctx, json.RawMessage(`{}`), &tool.ToolContext{ToolUseID: "off"})
	if err != nil || result.IsError {
		t.Fatalf("disabled route: %+v %v", result, err)
	}
	rows, err = lease.Recorder().Search("127.0.0.1", 0, 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("disabled capture wrote traffic: %+v %v", rows, err)
	}
	rejected.Store(true)
	result, err = off.Call(ctx, json.RawMessage(`{}`), &tool.ToolContext{ToolUseID: "stale"})
	if err != nil || !result.IsError || hits.Load() != 3 {
		t.Fatalf("stale route bypassed lease: %+v hits=%d %v", result, hits.Load(), err)
	}
	if strings.Contains(relay.URL(), lease.ProxyURL()) {
		t.Fatal("recorder credentials exposed through local relay")
	}
}

func TestRecordingRouteCapturesIsolatedShellHTTPS(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux sandbox")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl unavailable")
	}
	executable := filepath.Join(t.TempDir(), "ai-tool-worker")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", executable, "./cmd/ai-tool-worker")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build worker: %v %s", err, out)
	}
	t.Setenv("TIANQIONG_AI_TOOL_RUNNER", executable)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "shell-capture-fixture") }))
	defer server.Close()
	root := t.TempDir()
	tenant := uuid.NewString()
	scope := aitraffic.Scope{TenantID: tenant, UserID: 1, TaskID: 33}
	manager, err := aitraffic.NewManager(root, func(*http.Request) (*url.URL, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	lease, err := manager.Acquire(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	ctx, err = httpoutbound.WithRecordingRoute(ctx, lease.ProxyURL(), lease.CABundle(), func(context.Context) (bool, error) { return true, nil })
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, tenant, "user-1", "work")
	local, err := airuntime.NewNativeLocalTools(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	var bash tool.CoreTool
	for _, item := range local.Tools(false) {
		if item.Name() == "Bash" {
			bash = item
			break
		}
	}
	if bash == nil {
		t.Fatal("missing Bash")
	}
	input, _ := json.Marshal(map[string]any{"command": "curl -fsS " + server.URL + "/from-shell"})
	result, err := bash.Call(ctx, input, &tool.ToolContext{WorkingDir: workspace, ToolUseID: "shell"})
	if err != nil || result.IsError || !strings.Contains(result.Flatten(), "shell-capture-fixture") {
		t.Fatalf("captured shell request: %+v %v", result, err)
	}
	var fetch tool.CoreTool
	for _, item := range local.Tools(false) {
		if item.Name() == "WebFetch" {
			fetch = item
			break
		}
	}
	if fetch == nil {
		t.Fatal("missing WebFetch")
	}
	fetchInput, _ := json.Marshal(map[string]any{"url": server.URL + "/from-webfetch"})
	fetched, err := fetch.Call(ctx, fetchInput, &tool.ToolContext{WorkingDir: workspace, ToolUseID: "fetch"})
	if err != nil || fetched.IsError || !strings.Contains(fetched.Flatten(), "shell-capture-fixture") {
		t.Fatalf("captured WebFetch: %+v %v", fetched, err)
	}
	rows, err := lease.Recorder().Search("127.0.0.1", 0, 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("shell capture missing: %+v %v", rows, err)
	}
}

package aitraffic_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/norma/tool"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/aitraffic"
	"github.com/yyhuni/lunafox/server/internal/httpoutbound"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

// Optional real-browser test. The package directory must contain a locally
// installed @playwright/mcp and its dependencies; it is copied into an
// isolated workspace so the test never alters an existing user installation.
func TestBrowserMCPRecordsHTTPS(t *testing.T) {
	modules, browser := os.Getenv("TIANQIONG_TEST_MCP_NODE_MODULES"), os.Getenv("TIANQIONG_TEST_MCP_BROWSER")
	if runtime.GOOS != "linux" || modules == "" || browser == "" {
		t.Skip("real MCP package and browser not configured")
	}
	worker := filepath.Join(t.TempDir(), "ai-tool-worker")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", worker, "./cmd/ai-tool-worker")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build worker: %v %s", err, out)
	}
	t.Setenv("TIANQIONG_AI_TOOL_RUNNER", worker)
	root := t.TempDir()
	workspace := filepath.Join(root, assets.DefaultTenantID, "user-1", "work")
	if err := os.MkdirAll(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cp", "-a", modules, filepath.Join(workspace, "node_modules")).CombinedOutput(); err != nil {
		t.Fatalf("copy MCP fixture: %v %s", err, out)
	}
	var upstreamHits atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "<title>Browser MCP fixture</title><p>browser-capture-fixture</p>")
	}))
	defer target.Close()
	targetURL, _ := url.Parse(target.URL)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || !strings.HasPrefix(r.Host, "browser-fixture") {
			http.Error(w, "unexpected request", http.StatusBadGateway)
			return
		}
		upstreamHits.Add(1)
		server, err := net.DialTimeout("tcp", targetURL.Host, 3*time.Second)
		if err != nil {
			http.Error(w, "target unavailable", http.StatusBadGateway)
			return
		}
		client, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			server.Close()
			return
		}
		defer client.Close()
		defer server.Close()
		_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffer.Flush()
		done := make(chan struct{})
		go func() { _, _ = io.Copy(server, buffer); close(done) }()
		_, _ = io.Copy(client, server)
		_ = server.Close()
		<-done
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)
	if err := httpoutbound.DefaultProxyController.Set(true, upstream.URL); err != nil {
		t.Fatal(err)
	}
	defer httpoutbound.DefaultProxyController.UseEnvironment()
	manager, err := aitraffic.NewManager(root, func(*http.Request) (*url.URL, error) { return upstreamURL, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	lease, err := manager.Acquire(t.Context(), aitraffic.Scope{TenantID: assets.DefaultTenantID, UserID: 1, TaskID: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	var enabled, rejected atomic.Bool
	enabled.Store(true)
	ctx, err = httpoutbound.WithRecordingRoute(ctx, lease.ProxyURL(), lease.CABundle(), func(context.Context) (bool, error) {
		if rejected.Load() {
			return false, errors.New("lease lost")
		}
		return enabled.Load(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	browserCache := filepath.Dir(filepath.Dir(filepath.Dir(browser)))
	client, err := airuntime.NewMCPConnector(root)(ctx, 1, airuntime.MCPConfig{Name: "browser", Transport: "stdio", Command: "npx", Args: []string{"@playwright/mcp", "--headless"}, Env: map[string]string{"PLAYWRIGHT_BROWSERS_PATH": browserCache}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	tools, err := client.Tools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var navigate tool.CoreTool
	for _, candidate := range tools {
		if candidate.Name() == "mcp__browser__browser_navigate" {
			navigate = candidate
			break
		}
	}
	if navigate == nil {
		t.Fatal("browser_navigate unavailable")
	}
	input, _ := json.Marshal(map[string]string{"url": fmt.Sprintf("https://browser-fixture.invalid:%s/capture", targetURL.Port())})
	result, err := navigate.Call(ctx, input, nil)
	if err != nil || result.IsError || !strings.Contains(result.Flatten(), "Browser MCP fixture") {
		t.Fatalf("browser navigate: %v %s", err, result.Flatten())
	}
	rows, err := lease.Recorder().Search("browser-fixture.invalid", 0, 10)
	if err != nil || len(rows) == 0 || upstreamHits.Load() == 0 {
		t.Fatalf("browser route was not captured through the upstream proxy: %+v hits=%d %v", rows, upstreamHits.Load(), err)
	}
	previousHits := upstreamHits.Load()
	enabled.Store(false)
	input, _ = json.Marshal(map[string]string{"url": fmt.Sprintf("https://browser-fixture2.invalid:%s/disabled", targetURL.Port())})
	result, err = navigate.Call(ctx, input, nil)
	if err != nil || result.IsError || !strings.Contains(result.Flatten(), "Browser MCP fixture") || upstreamHits.Load() <= previousHits {
		t.Fatalf("disabled capture did not use the system proxy: %v %s", err, result.Flatten())
	}
	rows, err = lease.Recorder().Search("browser-fixture2.invalid", 0, 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("disabled capture recorded traffic: %+v %v", rows, err)
	}
	previousHits = upstreamHits.Load()
	rejected.Store(true)
	input, _ = json.Marshal(map[string]string{"url": fmt.Sprintf("https://browser-fixture3.invalid:%s/stale", targetURL.Port())})
	result, err = navigate.Call(ctx, input, nil)
	if err == nil && !result.IsError || upstreamHits.Load() != previousHits {
		t.Fatalf("expired run bypassed its traffic fence: %v %s", err, result.Flatten())
	}
}

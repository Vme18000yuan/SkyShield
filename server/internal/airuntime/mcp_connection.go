package airuntime

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Autumn-27/artex/mcphttp"
	"github.com/Autumn-27/norma/mcp"
	"github.com/Autumn-27/norma/tool"
	"github.com/yyhuni/lunafox/server/internal/ailocal"
	"github.com/yyhuni/lunafox/server/internal/httpoutbound"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type MCPClient interface {
	Tools(context.Context) ([]tool.CoreTool, error)
	Close() error
}
type MCPConnector func(context.Context, int, MCPConfig) (MCPClient, error)
type ownedMCPClient struct {
	MCPClient
	once    sync.Once
	stop    func()
	wait    func()
	cleanup func()
}

func (c *ownedMCPClient) Close() error {
	var err error
	c.once.Do(func() {
		if c.stop != nil {
			c.stop()
		}
		err = c.MCPClient.Close()
		if c.wait != nil {
			c.wait()
		}
		if c.cleanup != nil {
			c.cleanup()
		}
	})
	return err
}
func NewMCPConnector(root string) MCPConnector {
	connect := func(ctx context.Context, user int, config MCPConfig) (MCPClient, error) {
		if err := workspaceScopeError(ctx); err != nil {
			return nil, err
		}
		switch config.Transport {
		case "sse":
			return mcphttp.NewSSE(ctx, config.Name, config.Endpoint, config.Headers, config.Insecure)
		case "stdio":
			if !filepath.IsAbs(root) || user <= 0 {
				return nil, fmt.Errorf("本地 MCP 工作目录未配置")
			}
			workspace := filepath.Join(root, assets.DefaultTenantID, fmt.Sprintf("user-%d", user), "work")
			workspace, sharedPaths := scopedWorkspace(ctx, workspace)
			if err := os.MkdirAll(filepath.Join(workspace, ".tmp"), 0700); err != nil {
				return nil, err
			}
			relay, err := httpoutbound.NewLocalRelay(ctx)
			if err != nil {
				return nil, err
			}
			environment := map[string]string{}
			for key, value := range config.Env {
				environment[key] = value
			}
			// Reuse installed Playwright browsers without exposing the host's
			// general home directory to this isolated MCP process.
			browserCache := environment["PLAYWRIGHT_BROWSERS_PATH"]
			if config.Name == "browser" && browserCache == "" {
				if cache, cacheErr := os.UserCacheDir(); cacheErr == nil {
					candidate := filepath.Join(cache, "ms-playwright")
					if len(playwrightBrowserPaths(candidate)) > 0 {
						browserCache = candidate
						environment["PLAYWRIGHT_BROWSERS_PATH"] = candidate
					}
				}
			}
			for _, entry := range relay.Environment() {
				for i := range entry {
					if entry[i] == '=' {
						environment[entry[:i]] = entry[i+1:]
						break
					}
				}
			}
			browserArgs := append([]string(nil), config.Args...)
			var browserCleanup func()
			var resourceCleanup func()
			var tracker *mcpProcessTracker
			var cleanupOnce sync.Once
			closed := make(chan struct{})
			cleanup := func() {
				cleanupOnce.Do(func() {
					close(closed)
					if tracker != nil {
						tracker.stop()
					}
					relay.Close()
					if browserCleanup != nil {
						browserCleanup()
					}
					if resourceCleanup != nil {
						resourceCleanup()
					}
				})
			}
			if config.Name == "browser" && isPlaywrightMCP(config.Command, config.Args) {
				var socketDir string
				browserArgs, socketDir, browserCleanup, err = prepareBrowserMCP(workspace, browserCache, relay.URL(), relay.CABundle() != "", browserArgs)
				if err != nil {
					cleanup()
					return nil, err
				}
				environment["PWTEST_SOCKETS_DIR"] = socketDir
				// Chromium also creates its SingletonSocket under TMPDIR; use the
				// short symlink into the same workspace to fit Unix socket limits.
				environment["TMPDIR"] = socketDir
			}
			mcpCommand := config.Command
			// The published Server image contains the default browser MCP. Avoid
			// npx resolving it over the network on every new isolated workspace.
			// Explicit package versions and other MCP commands stay user-owned.
			if config.Name == "browser" && filepath.Base(mcpCommand) == "npx" && len(browserArgs) > 0 && browserArgs[0] == "@playwright/mcp" {
				const bundled = "/usr/local/bin/playwright-mcp"
				if info, statErr := os.Stat(bundled); statErr == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
					mcpCommand, browserArgs = bundled, browserArgs[1:]
				}
			}
			args := append([]string{mcpCommand}, browserArgs...)
			raw, _ := json.Marshal(args)
			// A one-shot anonymous pipe keeps configured MCP credentials and
			// proxy environment variables out of argv.
			secretRead, secretWrite, err := os.Pipe()
			if err != nil {
				cleanup()
				return nil, err
			}
			defer secretRead.Close()
			defer secretWrite.Close()
			secretPath := fmt.Sprintf("/proc/%d/fd/%d", os.Getpid(), secretRead.Fd())
			go func() { _ = json.NewEncoder(secretWrite).Encode(environment); _ = secretWrite.Close() }()
			paths, err := mcpReadPaths(mcpCommand, browserArgs)
			if err != nil {
				cleanup()
				return nil, err
			}
			paths = append(paths, sharedPaths...)
			if relay.CABundle() != "" {
				paths = append(paths, relay.CABundle())
			}
			if browserCache != "" && browserCache != "0" {
				paths = append(paths, playwrightBrowserPaths(browserCache)...)
			}
			if config.Name == "browser" {
				// Chromium and fontconfig read these public host settings. Keep
				// the rest of /proc, /sys and /etc outside the MCP sandbox.
				for _, path := range []string{"/etc/fonts", "/proc/cpuinfo", "/proc/sys/fs/inotify", "/sys/bus/pci/devices", "/opt/google/chrome", "/opt/microsoft/msedge"} {
					if _, statErr := os.Stat(path); statErr == nil {
						paths = append(paths, path)
					}
				}
				// PCI entries in /sys/bus are symlinks into these public
				// hardware-information directories.
				pciRoots, _ := filepath.Glob("/sys/devices/pci*")
				paths = append(paths, pciRoots...)
			}
			if len(paths) > 128 {
				cleanup()
				return nil, fmt.Errorf("本地 MCP 文件参数过多")
			}

			pathJSON, _ := json.Marshal(paths)
			executable := os.Getenv("TIANQIONG_AI_TOOL_RUNNER")
			if executable == "" {
				executable, err = os.Executable()
			}
			if err != nil {
				cleanup()
				return nil, err
			}
			tracker, err = newMCPProcessTracker()
			if err != nil {
				cleanup()
				return nil, err
			}
			defer tracker.closePipes()
			command := exec.Command(executable, "ai-tool-worker", "--root", workspace, "--exec-json", string(raw), "--env-file", secretPath, "--read-paths", string(pathJSON), "--group-report", tracker.reportPath())
			resourceCleanup, err = ailocal.ConstrainCommand(command)
			if err != nil {
				tracker.failedLaunch()
				cleanup()
				return nil, err
			}
			client, err := mcp.NewStdioClientCommand(ctx, config.Name, command)
			if err != nil {
				// No child can open the report pipe after the failed launch has
				// returned. Closing our copy lets the tracker finish on EOF.
				tracker.failedLaunch()
			}
			identityCtx, cancelIdentity := context.WithTimeout(context.Background(), time.Second)
			identityErr := tracker.ready(identityCtx)
			cancelIdentity()
			if err != nil {
				cleanup()
				return nil, err
			}
			if identityErr != nil {
				_ = client.Close()
				cleanup()
				return nil, identityErr
			}
			go func() {
				select {
				case <-ctx.Done():
					cleanup()
				case <-closed:
				}
			}()
			return &ownedMCPClient{MCPClient: client, stop: tracker.stop, wait: tracker.wait, cleanup: cleanup}, nil
		default:
			return mcphttp.New(ctx, config.Name, config.Endpoint, config.Headers, config.Insecure)
		}
	}
	return func(ctx context.Context, user int, config MCPConfig) (MCPClient, error) {
		run, release, err := AcquireToolProcess(ctx, false)
		if err != nil {
			return nil, err
		}
		client, err := connect(run, user, config)
		if err != nil {
			release()
			return nil, err
		}
		return &ownedMCPClient{MCPClient: client, cleanup: release}, nil
	}
}

// mcpReadPaths grants only explicitly configured executable and file arguments.
// General local Agent tools do not receive these MCP-specific permissions.
func mcpReadPaths(command string, args []string) ([]string, error) {
	paths := []string{}
	if filepath.IsAbs(command) {
		var err error
		paths, err = interpreterReadPaths(command)
		if err != nil {
			return nil, err
		}
	}
	for _, arg := range args {
		value := arg
		if strings.HasPrefix(value, "-") {
			if i := strings.IndexByte(value, '='); i >= 0 {
				value = value[i+1:]
			} else {
				continue
			}
		}
		if !filepath.IsAbs(value) {
			continue
		}
		info, err := os.Stat(value)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if info.Mode().IsRegular() {
			paths = append(paths, filepath.Clean(value))
		}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if !seen[path] {
			seen[path] = true
			out = append(out, path)
		}
	}
	if len(out) > 128 {
		return nil, fmt.Errorf("本地 MCP 文件参数过多")
	}
	return out, nil
}

// Grant only browser installation subdirectories, not arbitrary sibling files.
func playwrightBrowserPaths(root string) []string {
	if !filepath.IsAbs(root) || filepath.Clean(root) == "/" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	paths := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if !(strings.HasPrefix(name, "chromium-") || strings.HasPrefix(name, "chromium_headless_shell-") || strings.HasPrefix(name, "firefox-") || strings.HasPrefix(name, "webkit-") || strings.HasPrefix(name, "ffmpeg-")) {
			continue
		}
		path := filepath.Join(root, name)
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			paths = append(paths, path)
		}
	}
	return paths
}

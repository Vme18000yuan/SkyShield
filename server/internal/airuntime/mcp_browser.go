package airuntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Playwright's --proxy-server option discards URL credentials. Put the
// authenticated loopback relay in its configuration instead of argv.
func prepareBrowserMCP(workspace, cache, proxy string, recording bool, original []string) (args []string, socketDir string, cleanup func(), err error) {
	parsed, err := url.Parse(proxy)
	if err != nil || parsed.User == nil || parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" {
		return nil, "", nil, errors.New("invalid browser proxy")
	}
	password, _ := parsed.User.Password()
	proxyConfig := map[string]any{"server": parsed.Scheme + "://" + parsed.Host, "username": parsed.User.Username(), "password": password, "bypass": "<-loopback>"}
	args, configPath := []string{}, ""
	var selectedBrowser string
	executableSpecified := false
	for i := 0; i < len(original); i++ {
		value := original[i]
		name, inline, hasInline := strings.Cut(value, "=")
		if name == "--config" || name == "--proxy-server" || name == "--proxy-bypass" {
			if !hasInline {
				if i+1 >= len(original) {
					return nil, "", nil, fmt.Errorf("%s requires a value", name)
				}
				i++
				inline = original[i]
			}
			if name == "--config" {
				configPath = inline
			}
			continue
		}
		if name == "--executable-path" {
			executableSpecified = true
		}
		if name == "--browser" {
			if hasInline {
				selectedBrowser = inline
			} else if i+1 < len(original) {
				selectedBrowser = original[i+1]
			}
		}
		args = append(args, value)
	}
	config := map[string]any{}
	if configPath != "" {
		var file *os.File
		if filepath.IsAbs(configPath) {
			file, err = os.Open(configPath)
		} else {
			var root *os.Root
			root, err = os.OpenRoot(workspace)
			if err == nil {
				defer root.Close()
				file, err = root.Open(configPath)
			}
		}
		if err != nil {
			return nil, "", nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(file, (1<<20)+1))
		_ = file.Close()
		if readErr != nil || len(data) > 1<<20 || json.Unmarshal(data, &config) != nil || config == nil {
			return nil, "", nil, errors.New("invalid browser MCP configuration")
		}
	}
	object := func(parent map[string]any, key string) (map[string]any, error) {
		if parent[key] == nil {
			parent[key] = map[string]any{}
		}
		value, ok := parent[key].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid browser MCP %s configuration", key)
		}
		return value, nil
	}
	browser, err := object(config, "browser")
	if err != nil {
		return nil, "", nil, err
	}
	launch, err := object(browser, "launchOptions")
	if err != nil {
		return nil, "", nil, err
	}
	contextOptions, err := object(browser, "contextOptions")
	if err != nil {
		return nil, "", nil, err
	}
	launch["proxy"], contextOptions["proxy"] = proxyConfig, proxyConfig
	if recording {
		// The recorder terminates TLS with its per-scope CA. Playwright's
		// Chromium does not use SSL_CERT_FILE for browser trust.
		contextOptions["ignoreHTTPSErrors"] = true
	}
	if selectedBrowser == "" {
		selectedBrowser, _ = browser["browserName"].(string)
	}
	if selectedBrowser == "" || selectedBrowser == "chromium" || strings.HasPrefix(selectedBrowser, "chrome") {
		if launch["chromiumSandbox"] == nil {
			// Landlock supplies the local filesystem boundary. Newer
			// Playwright releases otherwise enable Chrome's sandbox, which
			// is incompatible with the required no-zygote mode.
			launch["chromiumSandbox"] = false
		}
		flags, ok := launch["args"].([]any)
		if launch["args"] != nil && !ok {
			return nil, "", nil, errors.New("invalid browser launch arguments")
		}
		// Renderer subprocesses cannot access other PIDs under Landlock.
		// Keep Chromium in the same sandboxed process, without opening /proc.
		for _, flag := range []string{"--single-process", "--no-zygote", "--disable-gpu"} {
			found := false
			for _, existing := range flags {
				if existing == flag {
					found = true
				}
			}
			if !found {
				flags = append(flags, flag)
			}
		}
		launch["args"] = flags
		if !executableSpecified && launch["executablePath"] == nil && selectedBrowser == "" && cache != "" && cache != "0" {
			paths, _ := filepath.Glob(filepath.Join(cache, "chromium_headless_shell-*", "chrome-headless-shell-*", "chrome-headless-shell"))
			if len(paths) > 0 {
				args = append(args, "--executable-path", paths[len(paths)-1])
				executableSpecified = true
			}
		}
		if !executableSpecified && launch["executablePath"] == nil && selectedBrowser == "" && cache == "" {
			// Only use the image's Chromium when its bundled MCP is present;
			// locally configured browsers and explicit selections take priority.
			if _, bundledErr := os.Stat("/usr/local/bin/playwright-mcp"); bundledErr == nil {
				const chromium = "/usr/lib/chromium/chromium"
				if info, browserErr := os.Stat(chromium); browserErr == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
					args = append(args, "--executable-path", chromium)
				}
			}
		}
	}
	short, err := os.MkdirTemp("/tmp", "ss-mcp-")
	if err != nil {
		return nil, "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(short) }
	defer func() {
		if err != nil {
			cleanup()
		}
	}()
	if err = os.Symlink(filepath.Join(workspace, ".tmp"), filepath.Join(short, "s")); err != nil {
		return nil, "", cleanup, err
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, "", cleanup, err
	}
	path := filepath.Join(short, "config.json")
	if err = os.WriteFile(path, encoded, 0600); err != nil {
		return nil, "", cleanup, err
	}
	return append(args, "--config", path), filepath.Join(short, "s"), cleanup, nil
}

func isPlaywrightMCP(command string, args []string) bool {
	for _, part := range append([]string{command}, args...) {
		if strings.Contains(part, "@playwright/mcp") || strings.Contains(filepath.Base(part), "playwright-mcp") {
			return true
		}
	}
	return false
}

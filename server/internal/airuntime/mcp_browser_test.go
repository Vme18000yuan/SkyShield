package airuntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareBrowserMCPPreservesConfigurationAndProtectsProxy(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, ".tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	userConfig := `{"browser":{"launchOptions":{"args":["--custom-flag"]},"contextOptions":{"locale":"zh-CN"}},"outputDir":"captures"}`
	if err := os.WriteFile(filepath.Join(workspace, "custom.json"), []byte(userConfig), 0600); err != nil {
		t.Fatal(err)
	}
	args, sockets, cleanup, err := prepareBrowserMCP(workspace, "", "http://relay-secret:x@127.0.0.1:12345", true, []string{"@playwright/mcp", "--headless", "--proxy-server", "http://ignored:80", "--config", "custom.json"})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for _, arg := range args {
		if strings.Contains(arg, "relay-secret") || strings.Contains(arg, "ignored") {
			t.Fatalf("proxy credential or override exposed in argv: %q", arg)
		}
	}
	path := args[len(args)-1]
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private browser config: %v %v", info, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		OutputDir string `json:"outputDir"`
		Browser   struct {
			Launch struct {
				Args  []string          `json:"args"`
				Proxy map[string]string `json:"proxy"`
			} `json:"launchOptions"`
			Context struct {
				Locale string            `json:"locale"`
				Ignore bool              `json:"ignoreHTTPSErrors"`
				Proxy  map[string]string `json:"proxy"`
			} `json:"contextOptions"`
		} `json:"browser"`
	}
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.OutputDir != "captures" || config.Browser.Context.Locale != "zh-CN" || !config.Browser.Context.Ignore || config.Browser.Launch.Proxy["username"] != "relay-secret" || config.Browser.Context.Proxy["server"] != "http://127.0.0.1:12345" {
		t.Fatalf("browser config was not merged: %+v", config)
	}
	flags := strings.Join(config.Browser.Launch.Args, " ")
	for _, flag := range []string{"--custom-flag", "--single-process", "--no-zygote", "--disable-gpu"} {
		if !strings.Contains(flags, flag) {
			t.Fatalf("missing browser flag %s: %s", flag, flags)
		}
	}
	target, err := os.Readlink(sockets)
	if err != nil || target != filepath.Join(workspace, ".tmp") {
		t.Fatalf("short socket path: %q %v", target, err)
	}
	cleanup()
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("browser config survived cleanup: %v", err)
	}
	if current, err := os.ReadFile(filepath.Join(workspace, "custom.json")); err != nil || string(current) != userConfig {
		t.Fatalf("user config was modified: %v", err)
	}
}

func TestPrepareBrowserMCPRejectsMalformedConfig(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "bad.json"), []byte(`{"browser":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := prepareBrowserMCP(workspace, "", "http://token:x@127.0.0.1:12345", false, []string{"--config", "bad.json"})
	if err == nil {
		t.Fatal("malformed browser config accepted")
	}
}

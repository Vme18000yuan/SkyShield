package localagent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDependenciesMustBePresentBeforeAgentStarts(t *testing.T) {
	root := t.TempDir()
	bin, tools := filepath.Join(root, "engines"), filepath.Join(root, "tools")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tools, 0o700); err != nil {
		t.Fatal(err)
	}
	config := Config{EngineBinDir: bin, ToolsDir: tools}
	if CheckDependencies(config) == nil {
		t.Fatal("missing tools were accepted")
	}
	populateFixtureDependencies(t, bin, tools)
	t.Setenv("PATH", tools)
	if err := CheckDependencies(config); err != nil {
		t.Fatalf("complete local dependencies rejected: %v", err)
	}
}

func populateFixtureDependencies(t *testing.T, bin, tools string) {
	t.Helper()
	write := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range supportedEngines {
		write(filepath.Join(bin, name))
	}
	for _, name := range []string{"subfinder", "puredns", "massdns", "naabu", "httpx", "katana", "waymore", "uro", "observer-ward", "ffuf", "nuclei", "cwebp", "website_discovery/httpx", "website_discovery/httpx.real", "subdomain_discovery/subfinder", "url_collection/katana", "nuclei_vulnerability/nuclei", "fingerprint_detection/observer-ward", "directory_scan/ffuf", "google-chrome"} {
		write(filepath.Join(tools, filepath.FromSlash(name)))
	}
}

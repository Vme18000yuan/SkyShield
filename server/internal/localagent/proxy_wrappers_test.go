package localagent

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNativeProxyWrappersRespectSystemOverride(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	project := filepath.Clean(filepath.Join(filepath.Dir(source), "../../.."))
	root := t.TempDir()
	for _, test := range []struct {
		name, engine, tool, template, real, flag string
	}{
		{"katana", "url_collection", "katana", "native-proxy-wrapper.sh", "katana", "-proxy"},
		{"nuclei", "nuclei_vulnerability", "nuclei", "native-proxy-wrapper.sh", "nuclei", "-proxy-internal\n-proxy"},
		{"website httpx", "website_discovery", "httpx", "native-httpx-proxy-wrapper.sh", "website_discovery/httpx.real", "-http-proxy"},
	} {
		t.Run(test.name, func(t *testing.T) {
			engineDir := filepath.Join(root, test.engine)
			if err := os.MkdirAll(engineDir, 0o700); err != nil {
				t.Fatal(err)
			}
			wrapper, err := os.ReadFile(filepath.Join(project, "scripts", test.template))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(engineDir, test.tool)
			if err := os.WriteFile(path, wrapper, 0o700); err != nil {
				t.Fatal(err)
			}
			real := filepath.Join(root, filepath.FromSlash(test.real))
			if err := os.WriteFile(real, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			for _, scenario := range []struct {
				marker    string
				wantProxy bool
			}{{"1", true}, {"0", false}} {
				cmd := exec.Command(path, "target")
				cmd.Env = []string{"PATH=/usr/bin:/bin", "HTTP_PROXY=http://127.0.0.1:1234", "NO_PROXY=example.invalid", "SKYSHIELD_FORCE_SYSTEM_PROXY=" + scenario.marker}
				output, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("wrapper: %v: %s", err, output)
				}
				if strings.Contains(string(output), test.flag+"\nhttp://127.0.0.1:1234") != scenario.wantProxy {
					t.Fatalf("marker %s: args %q", scenario.marker, output)
				}
			}
		})
	}
}

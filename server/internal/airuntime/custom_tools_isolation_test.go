package airuntime

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/tool"
)

func TestCustomPythonVirtualenvIsScopedToScript(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux sandbox")
	}
	executable := filepath.Join(t.TempDir(), "ai-tool-worker")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", executable, "./cmd/ai-tool-worker")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build worker: %v %s", err, out)
	}
	t.Setenv("TIANQIONG_AI_TOOL_RUNNER", executable)
	venvRoot := filepath.Join(t.TempDir(), "venv")
	if out, err := exec.Command("python3", "-m", "venv", "--without-pip", venvRoot).CombinedOutput(); err != nil {
		t.Fatalf("create virtualenv: %v %s", err, out)
	}
	python := filepath.Join(venvRoot, "bin", "python")
	out, err := exec.Command(python, "-c", "import sysconfig; print(sysconfig.get_path('purelib'))").Output()
	if err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(strings.TrimSpace(string(out)), "scope_fixture.py")
	if err = os.WriteFile(module, []byte("VALUE = 'virtualenv-loaded'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(filepath.Dir(venvRoot), "private")
	if err = os.WriteFile(secret, []byte("outside-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root := t.TempDir()
	tester := CustomToolTesterWithPython(root, func(context.Context, int) (string, error) { return python, nil })
	spec, _ := json.Marshal(map[string]any{"code": "import scope_fixture\nprint(scope_fixture.VALUE)\ntry:\n print(open(" + strconvQuote(secret) + ").read())\nexcept PermissionError:\n print('outside-blocked')"})
	result, err := tester(ctx, 1, artexdb.Tool{Key: "fixture_script", Kind: "script", Exec: spec, Schema: json.RawMessage(`{"type":"object","properties":{}}`)}, map[string]any{})
	if err != nil || result.IsError || !strings.Contains(result.Flatten(), "virtualenv-loaded") || !strings.Contains(result.Flatten(), "outside-blocked") || strings.Contains(result.Flatten(), "outside-secret") {
		t.Fatalf("virtualenv script: %+v %v", result, err)
	}
	workspace := filepath.Join(root, "00000000-0000-0000-0000-000000000001", "user-1", "work")
	local, err := NewNativeLocalTools(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	for _, item := range local.Tools(false) {
		if item.Name() == "Bash" {
			raw, _ := json.Marshal(map[string]any{"command": "cat " + shellQuoteValue(module)})
			res, e := item.Call(ctx, raw, &tool.ToolContext{WorkingDir: workspace})
			if e != nil || !res.IsError || strings.Contains(res.Flatten(), "virtualenv-loaded") {
				t.Fatalf("ordinary shell read virtualenv: %+v %v", res, e)
			}
			return
		}
	}
	t.Fatal("missing Bash tool")
}

func strconvQuote(value string) string { b, _ := json.Marshal(value); return string(b) }

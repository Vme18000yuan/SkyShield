package infogather

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/norma/skill"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/ailocal"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

func TestReadRunFilesSelectsCurrentRunArtifacts(t *testing.T) {
	const id = "current-run"
	const body = `{"run":{"id":"current-run"},"assets":[]}`
	for _, test := range []struct {
		name    string
		dirs    []string
		reports []string
		wantErr string
	}{
		{
			name:    "tool configuration and caches beside results",
			dirs:    []string{".config/subfinder", ".cache", ".dit", ".tmp", ".task-output/tasks", "tool-data", "old.example/runs/previous-run"},
			reports: []string{"example.test"},
		},
		{
			name:    "multiple results for the current run",
			reports: []string{"example.test", "other.example"},
			wantErr: "采集结果目录不唯一",
		},
		{
			name:    "configuration without results",
			dirs:    []string{".config/subfinder", ".cache", "example.test/runs/previous-run"},
			wantErr: "采集未生成结果目录",
		},
		{
			name:    "current run directory without import report",
			dirs:    []string{".config/subfinder", "example.test/runs/" + id},
			wantErr: "采集未生成可导入结果",
		},
		{
			name:    "incomplete second result remains ambiguous",
			dirs:    []string{"other.example/runs/" + id},
			reports: []string{"example.test"},
			wantErr: "采集结果目录不唯一",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			workspace := t.TempDir()
			dir := filepath.Join("info-gather", id)
			for _, name := range test.dirs {
				if err := os.MkdirAll(filepath.Join(workspace, dir, name), 0700); err != nil {
					t.Fatal(err)
				}
			}
			for _, target := range test.reports {
				base := filepath.Join(workspace, dir, target, "runs", id)
				if err := os.MkdirAll(base, 0700); err != nil {
					t.Fatal(err)
				}
				for name, data := range map[string]string{
					"skyshield-import.json": body,
					"manifest.json":         `{"steps":{"passive":{"status":"partial"}}}`,
				} {
					if err := os.WriteFile(filepath.Join(base, name), []byte(data), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			root, err := os.OpenRoot(workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			raw, coverage, err := readRunFiles(root, dir, id)
			if test.wantErr != "" {
				if err == nil || err.Error() != test.wantErr {
					t.Fatalf("want %q, got %v", test.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != body || !strings.Contains(string(coverage), `"passive":"partial"`) {
				t.Fatalf("lost current report or coverage: %s %s", raw, coverage)
			}
		})
	}
}

// Fast mode for an IP runs the real skill without DNS, HTTP or external scans.
func TestSandboxExecutesBundledSkillOffline(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux sandbox required")
	}
	executable := filepath.Join(t.TempDir(), "ai-tool-worker")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", executable, "./cmd/ai-tool-worker")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build worker: %v %s", err, out)
	}
	t.Setenv("TIANQIONG_AI_TOOL_RUNNER", executable)
	dataRoot := t.TempDir()
	workspace := filepath.Join(dataRoot, assets.DefaultTenantID, "user-1", "work")
	skillDir := filepath.Join(workspace, ".skills", "test-package")
	source, err := filepath.Abs("../../../resources/skills/info-gath")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.CopyFS(skillDir, os.DirFS(source)); err != nil {
		t.Fatal(err)
	}
	executor := SandboxExecutor{Root: dataRoot, Resolve: func(context.Context, int, string) ([]skill.Skill, error) {
		return []skill.Skill{{Name: "info-gath", Dir: skillDir}}, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	id := uuid.NewString()
	// A full collection shares HOME with subfinder and other tool state.
	for _, name := range []string{".config/subfinder", ".dit"} {
		if err := os.MkdirAll(filepath.Join(executor.ArtifactDirectory(1, id), name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	scope, err := parseScope("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	raw, coverage, err := executor.Execute(ctx, 1, id, scope, "fast", nil)
	if err != nil {
		t.Fatalf("sandbox run failed: %v", err)
	}
	report, err := decodeEnvelope(raw, id, scope, "fast")
	if err != nil {
		t.Fatalf("invalid skill output: %v: %s", err, raw)
	}
	if report.Run.Status != "completed" || len(report.Assets) != 1 || report.Assets[0].Value != "127.0.0.1" {
		t.Fatalf("unexpected report: %+v", report)
	}
	if len(coverage) == 0 {
		t.Fatal("missing stage coverage")
	}
}

// The model can run one actual phase, inspect its checkpoint, and finish
// later. The httpx fixture writes deterministic data and makes no requests.
func TestSandboxExecutesSeparatePhasesOffline(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux sandbox required")
	}
	executable := filepath.Join(t.TempDir(), "ai-tool-worker")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", executable, "./cmd/ai-tool-worker")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build worker: %v %s", err, out)
	}
	t.Setenv("TIANQIONG_AI_TOOL_RUNNER", executable)
	tools := t.TempDir()
	mock := `#!/usr/bin/python3
import json,sys
from pathlib import Path
# Network tools can initialize HOME even when only probing their version.
(Path.home()/'.config/httpx').mkdir(parents=True,exist_ok=True)
(Path.home()/'.cache/httpx').mkdir(parents=True,exist_ok=True)
a=sys.argv[1:]
if '-h' in a:
 print('-tech-detect -status-code -json'); sys.exit(0)
if '-version' in a:
 print('fixture'); sys.exit(0)
items=[json.dumps({'url':x,'status_code':200}) for x in sys.stdin.read().splitlines() if x]
Path(a[a.index('-o')+1]).write_text('\n'.join(items)+'\n')
`
	if err := os.WriteFile(filepath.Join(tools, "httpx"), []byte(mock), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TIANQIONG_INFO_GATHER_TOOLS_DIR", tools)
	dataRoot := t.TempDir()
	workspace := filepath.Join(dataRoot, assets.DefaultTenantID, "user-1", "work")
	skillDir := filepath.Join(workspace, ".skills", "test-package")
	source, err := filepath.Abs("../../../resources/skills/info-gath")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.CopyFS(skillDir, os.DirFS(source)); err != nil {
		t.Fatal(err)
	}
	executor := SandboxExecutor{Root: dataRoot, Resolve: func(context.Context, int, string) ([]skill.Skill, error) {
		return []skill.Skill{{Name: "info-gath", Dir: skillDir}}, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	id := uuid.NewString()
	scope, err := parseScope("http://127.0.0.1:18080/example")
	if err != nil {
		t.Fatal(err)
	}
	var progress []string
	raw, coverage, err := executor.ExecutePhase(ctx, 1, id, scope, "normal", "http", 5, func(s string) { progress = append(progress, s) })
	if err != nil {
		t.Fatalf("phase failed: %v", err)
	}
	report, err := decodeEnvelope(raw, id, scope, "normal")
	if err != nil || report.Run.Status != "partial" || len(report.Assets) < 2 || !strings.Contains(string(coverage), `"http":"success"`) || len(progress) == 0 {
		t.Fatalf("invalid phase checkpoint: report=%+v coverage=%s progress=%v err=%v", report, coverage, progress, err)
	}
	// LS runs with a conversation-specific working directory, not workspace
	// root. The returned artifact path must resolve there without escaping the sandbox.
	sessionDir := filepath.Join(workspace, "sessions", "fixture-conversation")
	if err := os.MkdirAll(sessionDir, 0700); err != nil {
		t.Fatal(err)
	}
	client, err := ailocal.NewClient(ctx, workspace, executable, nil)
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{"path": executor.ArtifactDirectory(1, id)})
	listed, listErr := client.Call(ctx, "LS", input, &tool.ToolContext{WorkingDir: sessionDir})
	client.Close()
	if listErr != nil || listed.IsError {
		t.Fatalf("chat could not inspect artifact directory: %s %v", listed.Flatten(), listErr)
	}
	raw, coverage, err = executor.ExecutePhase(ctx, 1, id, scope, "normal", "finish", 5, nil)
	if err != nil {
		t.Fatalf("finish failed: %v", err)
	}
	report, err = decodeEnvelope(raw, id, scope, "normal")
	if err != nil || report.Run.Status != "partial" || !strings.Contains(coverageSummary(coverage), "未执行阶段") {
		t.Fatalf("missing phases not reported: report=%+v coverage=%s err=%v", report, coverage, err)
	}
}

// The managed worker cannot enumerate /proc. A timed-out stage must still
// stop nested command groups and report a timeout rather than a missing tool.
func TestSandboxTimeoutStopsNestedCommandsWithoutProc(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux sandbox required")
	}
	executable := filepath.Join(t.TempDir(), "ai-tool-worker")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", executable, "./cmd/ai-tool-worker")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build worker: %v %s", err, out)
	}
	workspace := t.TempDir()
	scriptDir := filepath.Join(workspace, "scripts")
	if err := os.Mkdir(scriptDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"recon_common.py", "recon_supervisor.py"} {
		data, err := os.ReadFile(filepath.Join("../../../resources/skills/info-gath/scripts", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(scriptDir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	fixture := `import json,sys
from pathlib import Path
sys.path.insert(0, 'scripts')
from recon_common import run_command
try:
 list(Path('/proc').iterdir()); denied=False
except PermissionError:
 denied=True
payload = "import os,time; from pathlib import Path; Path('child.pid').write_text(str(os.getpid())); time.sleep(30)"
nested = "import sys; sys.path.insert(0,'scripts'); from recon_common import run_command; run_command([sys.executable,'-c'," + repr(payload) + "],'output','inner',timeout=25)"
Path('nested.py').write_text(nested)
result=run_command([sys.executable,'nested.py'],'output','outer',timeout=2)
print(json.dumps({'denied':denied,'status':result['status'],'returncode':result['returncode']}))
`
	if err := os.WriteFile(filepath.Join(workspace, "timeout-fixture.py"), []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := ailocal.NewClient(ctx, workspace, executable, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	input, _ := json.Marshal(map[string]any{"command": "python3 timeout-fixture.py"})
	result, err := client.Call(ctx, "Bash", input, &tool.ToolContext{WorkingDir: workspace})
	if err != nil || result.IsError {
		t.Fatalf("sandbox fixture failed: %s %v", result.Flatten(), err)
	}
	var report struct {
		Denied     bool   `json:"denied"`
		Status     string `json:"status"`
		ReturnCode int    `json:"returncode"`
	}
	if err := json.Unmarshal([]byte(result.Flatten()), &report); err != nil || !report.Denied || report.Status != "timeout" || report.ReturnCode != 124 {
		t.Fatalf("incorrect timeout result: %s %v", result.Flatten(), err)
	}
	payload, err := os.ReadFile(filepath.Join(workspace, "child.pid"))
	if err != nil {
		t.Fatalf("nested command did not start: %v", err)
	}
	pid, err := strconv.Atoi(string(payload))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		stat, readErr := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if os.IsNotExist(readErr) {
			return
		}
		parts := strings.SplitN(string(stat), ")", 2)
		if readErr == nil && len(parts) == 2 && strings.HasPrefix(strings.TrimSpace(parts[1]), "Z ") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("nested command remained alive after timeout")
}

func TestConfiguredToolsAreUsableWithoutExposingTheirDirectory(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux sandbox required")
	}
	executable := filepath.Join(t.TempDir(), "ai-tool-worker")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", executable, "./cmd/ai-tool-worker")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build worker: %v %s", err, out)
	}
	toolDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(toolDir, "subfinder"), []byte("#!/bin/sh\necho allowed-tool\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(toolDir, "private"), []byte("private-tool-directory-value"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(toolDir, "private"), filepath.Join(toolDir, "httpx")); err != nil {
		t.Fatal(err)
	}
	paths, foundDir := localToolResources(toolDir)
	if len(paths) != 1 || foundDir != toolDir {
		t.Fatalf("unsafe resource list: %v %q", paths, foundDir)
	}
	workspace := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := ailocal.NewClientWithReadPaths(ctx, workspace, executable, []string{"PATH=" + toolDir + ":/usr/local/bin:/usr/bin:/bin"}, paths)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	command := `subfinder; if cat ` + shellArg(filepath.Join(toolDir, "private")) + ` 2>/dev/null; then echo exposed; else echo blocked; fi`
	raw, _ := json.Marshal(map[string]any{"command": command})
	result, err := client.Call(ctx, "Bash", raw, &tool.ToolContext{WorkingDir: workspace})
	if err != nil || result.IsError || !strings.Contains(result.Flatten(), "allowed-tool") || !strings.Contains(result.Flatten(), "blocked") || strings.Contains(result.Flatten(), "private-tool-directory-value") {
		t.Fatalf("unexpected sandbox access: %s %v", result.Flatten(), err)
	}
}

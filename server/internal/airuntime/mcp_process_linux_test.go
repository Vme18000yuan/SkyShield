//go:build linux

package airuntime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

func TestMCPConnectorStopsChildProcessGroup(t *testing.T) {
	worker := filepath.Join(t.TempDir(), "ai-tool-worker")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", worker, "./cmd/ai-tool-worker")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build worker: %v %s", err, output)
	}
	t.Setenv("TIANQIONG_AI_TOOL_RUNNER", worker)
	root := t.TempDir()
	workspace := filepath.Join(root, assets.DefaultTenantID, "user-1", "work")
	if err := os.MkdirAll(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	fixture := `import json,subprocess,sys
p=subprocess.Popen([sys.executable,'-c','import time; time.sleep(300)'])
open('child.pid','w').write(str(p.pid))
for line in sys.stdin:
 r=json.loads(line)
 if 'id' not in r: continue
 result={'protocolVersion':'2024-11-05','capabilities':{'tools':{}},'serverInfo':{'name':'fixture','version':'1'}} if r['method']=='initialize' else {'tools':[]}
 print(json.dumps({'jsonrpc':'2.0','id':r['id'],'result':result}),flush=True)
`
	if err := os.WriteFile(filepath.Join(workspace, "fixture.py"), []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	for _, cancelFirst := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		client, err := NewMCPConnector(root)(ctx, 1, MCPConfig{Name: "fixture", Transport: "stdio", Command: "python3", Args: []string{"fixture.py"}})
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(workspace, "child.pid"))
		if err != nil {
			t.Fatal(err)
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			t.Fatal(err)
		}
		if cancelFirst {
			cancel()
		} else {
			_ = client.Close()
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			fields, err := processStat(pid)
			if err != nil || fields[0] == "Z" {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if fields, err := processStat(pid); err == nil && fields[0] != "Z" {
			t.Fatalf("MCP descendant %d survived close/cancel", pid)
		}
		_ = client.Close()
		cancel()
	}
}

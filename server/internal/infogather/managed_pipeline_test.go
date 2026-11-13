package infogather

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

	"github.com/Autumn-27/norma/tool"
	"github.com/yyhuni/lunafox/server/internal/ailocal"
)

// All network tools are fixtures: no documentation address or Web origin is contacted.
func TestManagedBatchPipelineInSandbox(t *testing.T) {
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
	scripts, err := filepath.Abs("../../../resources/skills/info-gath/scripts")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.CopyFS(filepath.Join(workspace, "scripts"), os.DirFS(scripts)); err != nil {
		t.Fatal(err)
	}
	mockDir := t.TempDir()
	mocks := map[string]string{
		"nmap": `#!/usr/bin/python3
import sys
from pathlib import Path
a=sys.argv[1:]; hosts=Path(a[a.index('-iL')+1]).read_text().splitlines()
dest=Path(a[a.index('-oX')+1]); out='<nmaprun>'
for host in hosts:
 kind='ipv6' if ':' in host else 'ipv4'
 out+=f'<host><address addr="{host}" addrtype="{kind}"/><ports><port protocol="tcp" portid="443"><state state="open"/><service name="https"/></port></ports></host>'
dest.write_text(out+'</nmaprun>')
`,
		"curl": `#!/usr/bin/python3
import sys
from pathlib import Path
a=sys.argv[1:]
if '-o' in a:
 p=Path(a[a.index('-o')+1]);p.parent.mkdir(parents=True,exist_ok=True);p.write_text('<html><title>fixture</title></html>')
if '-w' in a:
 print(a[a.index('-w')+1].replace('%{http_code}','200').replace('%{content_type}','text/html'),end='')
`,
	}
	readPaths := []string{}
	for name, code := range mocks {
		file := filepath.Join(mockDir, name)
		if err = os.WriteFile(file, []byte(code), 0700); err != nil {
			t.Fatal(err)
		}
		readPaths = append(readPaths, file)
	}
	fixture := `import json,subprocess,sys
from pathlib import Path
Path('addresses.txt').write_text('203.0.113.1\n2001:db8::1\n')
Path('entries.json').write_text(json.dumps(['http://a.example.test/','http://b.example.test/','http://c.example.test/']))
subprocess.run([sys.executable,'scripts/scan_ports.py','addresses.txt','out'],check=True,stdout=subprocess.DEVNULL)
subprocess.run([sys.executable,'scripts/web_batch.py','entries.json','out'],check=True,stdout=subprocess.DEVNULL)
ports=json.loads(Path('out/raw/port-scan-status.json').read_text())
web=json.loads(Path('out/raw/web-batch-status.json').read_text())
print(json.dumps({'ports':ports['completed_targets'],'web':web['completed'],'workers':web['workers'],'rate':web['per_worker_rate']}))
`
	if err = os.WriteFile(filepath.Join(workspace, "pipeline-fixture.py"), []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	env := []string{"PATH=" + mockDir + ":/usr/bin:/bin", "RECON_MANAGED_PROXY=1", "RECON_SCOPE_HOST=example.test", "RECON_MODE=normal", "RECON_HTTP_RATE=1000", "RECON_STAGE_TIMEOUT=45", "MASSCAN_RATE=2000", "PYTHONDONTWRITEBYTECODE=1"}
	client, err := ailocal.NewClientWithReadPaths(ctx, workspace, executable, env, readPaths)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	input, _ := json.Marshal(map[string]any{"command": "python3 pipeline-fixture.py"})
	result, err := client.Call(ctx, "Bash", input, &tool.ToolContext{WorkingDir: workspace})
	if err != nil || result.IsError {
		t.Fatalf("managed pipeline failed: %s %v", result.Flatten(), err)
	}
	var value struct {
		Ports   int `json:"ports"`
		Web     int `json:"web"`
		Workers int `json:"workers"`
		Rate    int `json:"rate"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Flatten())), &value); err != nil || value.Ports != 2 || value.Web != 3 || value.Workers != 3 || value.Rate != 333 {
		t.Fatalf("unexpected managed results: %s %v", result.Flatten(), err)
	}
}

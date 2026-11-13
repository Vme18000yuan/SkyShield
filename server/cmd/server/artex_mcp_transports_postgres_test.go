package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	repository "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func verifyARTEXMCPTransportsPostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	t.Setenv("TIANQIONG_AI_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("m", 32))))
	previousKey, keyPresent := os.LookupEnv("TIANQIONG_LLM_API_KEY")
	t.Setenv("TIANQIONG_LLM_API_KEY", "host-mcp-secret-sentinel")
	defer func() {
		if keyPresent {
			_ = os.Setenv("TIANQIONG_LLM_API_KEY", previousKey)
		} else {
			_ = os.Unsetenv("TIANQIONG_LLM_API_KEY")
		}
	}()
	var admin, member int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&admin); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&member); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := repository.NewMCPStore(orm)
	facade := settings.NewMCPFacade(store, airuntime.SecretBox{})
	root := t.TempDir()
	connector := airuntime.NewMCPConnector(root)
	facade.SetConnector(connector)
	executable := filepath.Join(t.TempDir(), "ai-tool-worker")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", executable, "./cmd/ai-tool-worker")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build local MCP runner: %v %s", err, output)
	}
	t.Setenv("TIANQIONG_AI_TOOL_RUNNER", executable)
	workspace := filepath.Join(root, "00000000-0000-0000-0000-000000000001", fmt.Sprintf("user-%d", admin), "work")
	if err = os.MkdirAll(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	code := `import json,sys,os
for line in sys.stdin:
 r=json.loads(line)
 if 'id' not in r: continue
 result={}
 if r['method']=='initialize': result={'protocolVersion':'2024-11-05','capabilities':{'tools':{}},'serverInfo':{'name':'fixture','version':'1'}}
 elif r['method']=='tools/list': result={'tools':[{'name':'probe','description':'fixture','inputSchema':{'type':'object','properties':{}}}]}
 elif r['method']=='tools/call': result={'content':[{'type':'text','text':os.getenv('MCP_TEST_TOKEN','')+':'+str('TIANQIONG_LLM_API_KEY' in os.environ)}]}
 print(json.dumps({'jsonrpc':'2.0','id':r['id'],'result':result}),flush=True)
`
	if err = os.WriteFile(filepath.Join(workspace, "mcp_fixture.py"), []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	record, err := facade.Save(ctx, admin, "", settings.MCPInput{Name: "native_stdio_fixture", Transport: "stdio", Command: "python3", Args: []string{"mcp_fixture.py"}, Env: map[string]string{"MCP_TEST_TOKEN": "fixture-mcp-token"}, ReplaceEnv: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer facade.Delete(context.Background(), admin, record.ID)
	var cipher []byte
	if err = db.QueryRowContext(ctx, `SELECT encrypted_env FROM ai_mcp_server WHERE id=$1`, record.ID).Scan(&cipher); err != nil || strings.Contains(string(cipher), "fixture-mcp-token") {
		t.Fatal("MCP environment is not encrypted")
	}
	rows, err := facade.List(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if len(row.EncryptedEnv) > 0 || len(row.EncryptedHeaders) > 0 {
			t.Fatal("credential cipher exposed in list")
		}
	}
	tools, err := facade.Test(ctx, admin, record.ID)
	if err != nil || len(tools) != 1 || tools[0].Name != "mcp__native_stdio_fixture__probe" {
		t.Fatalf("native stdio handshake: %+v %v", tools, err)
	}
	cached, err := facade.CachedTools(ctx, admin, record.ID)
	if err != nil || len(cached) != 1 {
		t.Fatalf("MCP tool cache: %+v %v", cached, err)
	}
	if err = facade.SetEnabled(ctx, admin, record.ID, false); err != nil {
		t.Fatal(err)
	}
	cached, err = facade.CachedTools(ctx, admin, record.ID)
	if err != nil || len(cached) != 1 {
		t.Fatal("enable toggle erased tool cache")
	}
	if err = facade.SetEnabled(ctx, admin, record.ID, true); err != nil {
		t.Fatal(err)
	}
	skillStore := repository.NewSkillStore(orm)
	packages := settings.NewSkillPackageFacade(skillStore)
	name := "native-mcp-skill-fixture"
	if err = packages.Create(ctx, admin, settings.SkillPackage{Name: name, Description: "MCP gate fixture", Instructions: "Use the MCP probe", MCPs: []string{record.Name}}); err != nil {
		t.Fatal(err)
	}
	defer packages.Delete(context.Background(), admin, name)
	if err = packages.Toggle(ctx, admin, name, "auto", true); err != nil {
		t.Fatal(err)
	}
	skills := settings.NewSkillFacade(skillStore)
	augment := airuntime.NewMCPAugmenterWithConnector(facade.Resolve, connector, skills.Resolve)
	assembled, deferred, cleanup := augment(ctx, admin, "auto")
	defer cleanup()
	if len(deferred.Deferred) != 1 || len(deferred.GlobalNames) != 0 || deferred.Unlock.Has(tools[0].Name) {
		t.Fatalf("skill-gated MCP visible too early: %+v", deferred)
	}
	found := false
	for _, item := range assembled {
		if item.Name() == "Skill" {
			result, err := item.Call(ctx, json.RawMessage(`{"name":"`+name+`"}`), nil)
			if err != nil || result.IsError {
				t.Fatalf("load gated skill: %v %s", err, result.Flatten())
			}
			found = true
		}
	}
	if !found || !deferred.Unlock.Has(tools[0].Name) {
		t.Fatal("Skill did not unlock its MCP")
	}
	for _, item := range assembled {
		if item.Name() == tools[0].Name {
			result, err := item.Call(ctx, json.RawMessage(`{}`), nil)
			if err != nil || result.IsError || !strings.Contains(result.Flatten(), "fixture-mcp-token:False") {
				t.Fatalf("confined MCP call: %s %v", result.Flatten(), err)
			}
		}
	}
	if err = facade.SetVisible(ctx, admin, record.ID, "worker", true); err != nil {
		t.Fatal(err)
	}
	configs, err := facade.Resolve(ctx, member, "worker")
	if err != nil || len(configs) != 1 || configs[0].SkillGated {
		t.Fatalf("direct MCP visibility: %+v %v", configs, err)
	}
	if _, err = facade.Details(ctx, member, record.ID); !errors.Is(err, settings.ErrForbidden) {
		t.Fatalf("member read MCP environment: %v", err)
	}
	verifyMCPExternalVirtualenv(t, ctx, admin, executable, connector)
	events := make(chan []byte, 20)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: endpoint\ndata: /message\n\n")
			w.(http.Flusher).Flush()
			for {
				select {
				case <-r.Context().Done():
					return
				case event := <-events:
					fmt.Fprintf(w, "event: message\ndata: %s\n\n", event)
					w.(http.Flusher).Flush()
				}
			}
		}
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		w.WriteHeader(202)
		if request.ID == 0 {
			return
		}
		result := map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "fixture", "version": "1"}}
		if request.Method == "tools/list" {
			result = map[string]any{"tools": []any{map[string]any{"name": "sse_probe", "description": "fixture", "inputSchema": map[string]any{"type": "object"}}}}
		}
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		events <- raw
	}))
	defer remote.Close()
	sse, err := facade.Save(ctx, admin, "", settings.MCPInput{Name: "native_sse_fixture", Transport: "sse", Endpoint: remote.URL + "/sse", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	defer facade.Delete(context.Background(), admin, sse.ID)
	if discovered, err := facade.Test(ctx, admin, sse.ID); err != nil || len(discovered) != 1 || discovered[0].Name != "mcp__native_sse_fixture__sse_probe" {
		t.Fatalf("legacy SSE handshake: %+v %v", discovered, err)
	}
	if err = store.SeedMCPDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	defaults, err := facade.List(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]settings.MCPServer{}
	for _, item := range defaults {
		seen[item.Name] = item
	}
	if seen["browser"].Transport != "stdio" || seen["browser"].Command != "npx" || seen["browser"].Enabled || seen["ScopeSentry"].Enabled {
		t.Fatalf("native default MCP settings: %+v", seen)
	}
}

// Exercise an administrator-configured executable and script outside the
// workspace, including real venv imports and secret handoff to the MCP process.
func verifyMCPExternalVirtualenv(t *testing.T, ctx context.Context, user int, worker string, connector airuntime.MCPConnector) {
	t.Helper()
	venv := filepath.Join(t.TempDir(), "venv")
	if out, err := exec.Command("python3", "-m", "venv", "--without-pip", venv).CombinedOutput(); err != nil {
		t.Fatalf("MCP venv: %v %s", err, out)
	}
	python := filepath.Join(venv, "bin", "python")
	out, err := exec.Command(python, "-c", "import sysconfig; print(sysconfig.get_path('purelib'))").Output()
	if err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(strings.TrimSpace(string(out)), "mcp_scope_fixture.py")
	if err = os.WriteFile(module, []byte("VALUE='venv-mcp-loaded'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	browserCache := t.TempDir()
	browserDir := filepath.Join(browserCache, "chromium-fixture")
	if err = os.MkdirAll(browserDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(browserDir, "marker"), []byte("browser-cache-readable"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(browserCache, "private"), []byte("cache-private-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "server.py")
	code := `import json,sys,os,mcp_scope_fixture
cache=os.environ['PLAYWRIGHT_BROWSERS_PATH']
marker=open(os.path.join(cache,'chromium-fixture','marker')).read()
try: private=open(os.path.join(cache,'private')).read()
except PermissionError: private='blocked'
for line in sys.stdin:
 r=json.loads(line)
 if 'id' not in r: continue
 result={}
 if r['method']=='initialize': result={'protocolVersion':'2024-11-05','capabilities':{'tools':{}},'serverInfo':{'name':'fixture','version':'1'}}
 elif r['method']=='tools/list': result={'tools':[{'name':'probe','description':'fixture','inputSchema':{'type':'object','properties':{}}}]}
 elif r['method']=='tools/call': result={'content':[{'type':'text','text':mcp_scope_fixture.VALUE+':'+os.getenv('MCP_PRIVATE','')+':'+str('TIANQIONG_LLM_API_KEY' in os.environ)+':'+marker+':'+private}]}
 print(json.dumps({'jsonrpc':'2.0','id':r['id'],'result':result}),flush=True)
`
	if err = os.WriteFile(script, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	argsLog := filepath.Join(t.TempDir(), "worker-args")
	wrapper := filepath.Join(t.TempDir(), "worker-wrapper")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	wrapperScript := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %s\nexec %s \"$@\"\n", quote(argsLog), quote(worker))
	if err = os.WriteFile(wrapper, []byte(wrapperScript), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TIANQIONG_AI_TOOL_RUNNER", wrapper)
	runCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	client, err := connector(runCtx, user, airuntime.MCPConfig{Name: "browser", Transport: "stdio", Command: python, Args: []string{script}, Env: map[string]string{"MCP_PRIVATE": "mcp-secret-never-in-argv", "PLAYWRIGHT_BROWSERS_PATH": browserCache}})
	if err != nil {
		t.Fatalf("external MCP: %v", err)
	}
	defer client.Close()
	tools, err := client.Tools(runCtx)
	if err != nil || len(tools) != 1 {
		t.Fatalf("external MCP tools: %+v %v", tools, err)
	}
	result, err := tools[0].Call(runCtx, json.RawMessage(`{}`), nil)
	if err != nil || result.IsError || !strings.Contains(result.Flatten(), "venv-mcp-loaded:mcp-secret-never-in-argv:False:browser-cache-readable:blocked") {
		t.Fatalf("external MCP call: %s %v", result.Flatten(), err)
	}
	args, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(args), "mcp-secret-never-in-argv") || strings.Contains(string(args), "--env-json") || !strings.Contains(string(args), "--env-file") {
		t.Fatalf("MCP worker arguments exposed credentials: %s", args)
	}
}

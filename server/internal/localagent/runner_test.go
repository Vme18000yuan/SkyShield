package localagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	control "github.com/yyhuni/lunafox/contracts/gen/lunafox/agent/control/v1"
	data "github.com/yyhuni/lunafox/contracts/gen/lunafox/agent/data/v1"
	execution "github.com/yyhuni/lunafox/contracts/gen/lunafox/agent/execution/v1"
	engine "github.com/yyhuni/lunafox/engine-go/protocol"
	"github.com/yyhuni/lunafox/server/internal/httpoutbound"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/durationpb"
)

type localFixtureServer struct {
	data.UnimplementedDataPlaneServiceServer
	data.UnimplementedExecutionArtifactServiceServer
	mu                sync.Mutex
	progress, batches int
}

func (server *localFixtureServer) BatchWriteTaskProgressLogs(_ context.Context, request *data.BatchWriteTaskProgressLogsRequest) (*data.BatchWriteTaskProgressLogsResponse, error) {
	if request.GetTask() != "scans/1/tasks/1" || len(request.GetEntries()) != 1 || request.GetEntries()[0].GetContent() != "fixture progress" {
		return nil, fmt.Errorf("unexpected progress")
	}
	server.mu.Lock()
	server.progress++
	server.mu.Unlock()
	return &data.BatchWriteTaskProgressLogsResponse{}, nil
}

func (server *localFixtureServer) BatchIngestTaskResults(_ context.Context, request *data.BatchIngestTaskResultsRequest) (*data.BatchIngestTaskResultsResponse, error) {
	if request.GetTask() != "scans/1/tasks/1" || request.GetTarget() != "targets/1" || request.GetResultType() != "asset.subdomain.v1" || len(request.GetItemsJson()) != 1 {
		return nil, fmt.Errorf("unexpected result")
	}
	server.mu.Lock()
	server.batches++
	server.mu.Unlock()
	return &data.BatchIngestTaskResultsResponse{}, nil
}

func (server *localFixtureServer) StreamExecutionInput(request *data.StreamExecutionInputRequest, stream grpc.ServerStreamingServer[data.ExecutionArtifactFrame]) error {
	if request.GetRole() != "subdomains" || request.GetTask() != "scans/1/tasks/1" || request.GetExecution() != "executions/1" {
		return fmt.Errorf("unexpected input request")
	}
	content := []byte("a.example.com\n")
	digest := sha256.Sum256(content)
	count := uint64(1)
	frames := []*data.ExecutionArtifactFrame{
		{Payload: &data.ExecutionArtifactFrame_Header{Header: &data.ExecutionArtifactHeader{Task: request.GetTask(), Execution: request.GetExecution(), ContentType: "application/vnd.lunafox.subdomains.v1", Binding: &data.ExecutionArtifactHeader_ExecutionInput{ExecutionInput: &data.ExecutionInputBindingKey{Role: request.GetRole()}}}}},
		{Payload: &data.ExecutionArtifactFrame_Chunk{Chunk: &data.ExecutionArtifactChunk{Data: content}}},
		{Payload: &data.ExecutionArtifactFrame_Trailer{Trailer: &data.ExecutionArtifactTrailer{Integrity: &data.ExecutionArtifactIntegrity{SizeBytes: uint64(len(content)), Sha256Digest: "sha256:" + hex.EncodeToString(digest[:]), RecordCount: &count}}}},
	}
	for _, frame := range frames {
		if err := stream.Send(frame); err != nil {
			return err
		}
	}
	return nil
}

func TestNativeEngineReportsAndMaterializesInput(t *testing.T) {
	t.Setenv("DB_PASSWORD", "must-not-reach-engine")
	root, err := os.MkdirTemp("/tmp", "ss-agent-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	engineDir, sourceDir, cacheDir := filepath.Join(root, "bin"), filepath.Join(root, "source"), filepath.Join(root, "cache")
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, dir := range []string{engineDir, filepath.Join(sourceDir, "port_scan"), filepath.Join(cacheDir, "expanded", "sha256-"+digest[7:])} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{filepath.Join(sourceDir, "port_scan", "engine.json"), filepath.Join(cacheDir, "expanded", "sha256-"+digest[7:], "engine.json")} {
		if err := os.WriteFile(file, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", filepath.Join(engineDir, "port_scan"), "./testdata/engine")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture Engine: %v: %s", err, output)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	fixture := &localFixtureServer{}
	data.RegisterDataPlaneServiceServer(server, fixture)
	data.RegisterExecutionArtifactServiceServer(server, fixture)
	go server.Serve(listener)
	defer server.Stop()
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	plan := &execution.ResolvedEngineExecutionPlan{
		Execution: "executions/1", Task: "scans/1/tasks/1", Target: &execution.CanonicalTarget{Resource: "targets/1", Type: execution.TargetType_TARGET_TYPE_DOMAIN, Value: "example.com"},
		WorkflowStep:  &execution.WorkflowStepScope{Scan: "scans/1", Workflow: "scanWorkflows/default", StageId: "ports", StepId: "port_scan"},
		EngineRelease: &execution.EngineRelease{Engine: "engine.lunafox.port_scan", PackageDigest: digest, EngineApiMajor: 2, CompatibilityRevision: engine.EngineExecutionDiagnosticsCompatibilityRevision},
		RuntimeImage:  &execution.RuntimeImage{Refs: []string{"docker.io/yyhuni/lunafox-engine-runtime-port-scan@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},
		Config:        &execution.FinalEngineConfig{Sections: []*execution.EngineConfigSection{{SectionId: "naabu_active", Enabled: true}}},
		Limits:        &execution.ExecutionLimits{MaxExecutionDuration: durationpb.New(time.Minute), ProgressMessageMaxBytes: 1024, ResultBatchMaxItems: 10, ResultBatchMaxBytes: 1024},
	}
	config := Config{ExecutionRoot: root, EngineBinDir: engineDir, EngineSourceDir: sourceDir, PackageCacheRoot: cacheDir, ToolsDir: root, ProxySocket: fixtureProxySocket(t, root)}
	service, err := runEngine(context.Background(), config, conn, credentials{AgentID: 1, InstanceID: "fixture", AuthenticationToken: "deadbeef"}, "session", 1, plan)
	if err != nil {
		t.Fatalf("native Engine execution: %v", err)
	}
	if got := service.terminalDiagnostics(control.TerminalTaskResultState_TERMINAL_TASK_RESULT_STATE_SUCCEEDED, nil); got.GetAvailability() != engine.EngineExecutionDiagnosticAvailability_ENGINE_EXECUTION_DIAGNOSTIC_AVAILABILITY_AVAILABLE {
		t.Fatalf("diagnostics = %v", got)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.progress != 1 || fixture.batches != 1 {
		t.Fatalf("progress=%d batches=%d", fixture.progress, fixture.batches)
	}
}

func fixtureProxySocket(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "proxy")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "proxy.sock")
	controller := httpoutbound.NewProxyController()
	if err := controller.Set(true, "http://127.0.0.1:1234"); err != nil {
		t.Fatal(err)
	}
	socket, err := httpoutbound.StartLocalProxySocket(path, controller)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = socket.Close() })
	return path
}

type frameFixture struct {
	frames []*data.ExecutionArtifactFrame
	index  int
}

func (fixture *frameFixture) Recv() (*data.ExecutionArtifactFrame, error) {
	if fixture.index == len(fixture.frames) {
		return nil, io.EOF
	}
	frame := fixture.frames[fixture.index]
	fixture.index++
	return frame, nil
}

func TestReceiveArtifactRejectsCorruptTransfer(t *testing.T) {
	content := []byte("fixture\n")
	digest := sha256.Sum256(content)
	frames := []*data.ExecutionArtifactFrame{
		{Payload: &data.ExecutionArtifactFrame_Header{Header: &data.ExecutionArtifactHeader{Task: "scans/1/tasks/1", Execution: "executions/1", ContentType: "text/plain"}}},
		{Payload: &data.ExecutionArtifactFrame_Chunk{Chunk: &data.ExecutionArtifactChunk{Data: []byte("changed")}}},
		{Payload: &data.ExecutionArtifactFrame_Trailer{Trailer: &data.ExecutionArtifactTrailer{Integrity: &data.ExecutionArtifactIntegrity{SizeBytes: uint64(len(content)), Sha256Digest: "sha256:" + hex.EncodeToString(digest[:])}}}},
	}
	destination := filepath.Join(t.TempDir(), "input")
	if err := receiveArtifact(&frameFixture{frames: frames}, destination, "scans/1/tasks/1", "executions/1", "text/plain", func(*data.ExecutionArtifactHeader) bool { return true }); err == nil {
		t.Fatal("corrupt transfer accepted")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("corrupt input was published: %v", err)
	}
}

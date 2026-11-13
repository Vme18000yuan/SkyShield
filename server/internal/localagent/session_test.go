package localagent

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	control "github.com/yyhuni/lunafox/contracts/gen/lunafox/agent/control/v1"
	data "github.com/yyhuni/lunafox/contracts/gen/lunafox/agent/data/v1"
	execution "github.com/yyhuni/lunafox/contracts/gen/lunafox/agent/execution/v1"
	engine "github.com/yyhuni/lunafox/engine-go/protocol"
	"github.com/yyhuni/lunafox/server/internal/grpc/agentcontrol"
	planeauth "github.com/yyhuni/lunafox/server/internal/grpc/planeauth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/durationpb"
)

type localControlFixture struct {
	control.UnimplementedControlPlaneServiceServer
	plan *execution.ResolvedEngineExecutionPlan
	done chan error
}

func (fixture *localControlFixture) Connect(stream grpc.BidiStreamingServer[control.ConnectRequest, control.ConnectResponse]) error {
	finish := func(err error) error { fixture.done <- err; return err }
	md, _ := metadata.FromIncomingContext(stream.Context())
	if len(md.Get(planeauth.AgentAuthenticationTokenMetadataKey)) != 1 || len(md.Get(agentcontrol.NativeExecutionMetadataKey)) != 1 {
		return finish(fmt.Errorf("missing local session metadata"))
	}
	first, err := stream.Recv()
	if err != nil {
		return finish(err)
	}
	registration := first.GetRegisterSession()
	if registration == nil || registration.GetAgent() != "agents/fixture" || registration.GetContainerRuntimeReady() || len(registration.GetSupportedEngineApiMajors()) != 1 {
		return finish(fmt.Errorf("unexpected local registration"))
	}
	if err := stream.Send(&control.ConnectResponse{Payload: &control.ConnectResponse_SessionReady{SessionReady: &control.SessionReady{SessionEpoch: 1, CompatibilityRevision: engine.EngineExecutionDiagnosticsCompatibilityRevision}}}); err != nil {
		return finish(err)
	}
	for {
		message, err := stream.Recv()
		if err != nil {
			return finish(err)
		}
		if request := message.GetRequestTask(); request != nil {
			if err := stream.Send(&control.ConnectResponse{Payload: &control.ConnectResponse_TaskAssign{TaskAssign: &control.TaskAssign{RequestId: request.GetRequestId(), Outcome: &control.TaskAssign_Plan{Plan: fixture.plan}}}}); err != nil {
				return finish(err)
			}
		}
		if terminal := message.GetTerminalTaskResult(); terminal != nil {
			if terminal.GetTask() != fixture.plan.GetTask() || terminal.GetResult() != control.TerminalTaskResultState_TERMINAL_TASK_RESULT_STATE_SUCCEEDED || terminal.GetDiagnostics().GetAvailability() != engine.EngineExecutionDiagnosticAvailability_ENGINE_EXECUTION_DIAGNOSTIC_AVAILABILITY_AVAILABLE {
				return finish(fmt.Errorf("unexpected terminal result"))
			}
			if err := stream.Send(&control.ConnectResponse{Payload: &control.ConnectResponse_TerminalTaskResultAck{TerminalTaskResultAck: &control.TerminalTaskResultAck{Task: terminal.GetTask(), SessionEpoch: 1, CompatibilityRevision: engine.EngineExecutionDiagnosticsCompatibilityRevision}}}); err != nil {
				return finish(err)
			}
			fixture.done <- nil
			<-stream.Context().Done()
			return nil
		}
	}
}

func TestLocalAgentSessionClaimsAndReportsTask(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "ss-session-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	engineDir, sourceDir, cacheDir, toolsDir := filepath.Join(root, "bin"), filepath.Join(root, "source"), filepath.Join(root, "cache"), filepath.Join(root, "tools")
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
	populateFixtureDependencies(t, engineDir, toolsDir)
	t.Setenv("PATH", toolsDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.Remove(filepath.Join(engineDir, "port_scan")); err != nil {
		t.Fatal(err)
	}
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", filepath.Join(engineDir, "port_scan"), "./testdata/engine")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture Engine: %v: %s", err, output)
	}
	plan := &execution.ResolvedEngineExecutionPlan{
		Execution: "executions/1", Task: "scans/1/tasks/1", Target: &execution.CanonicalTarget{Resource: "targets/1", Type: execution.TargetType_TARGET_TYPE_DOMAIN, Value: "example.com"},
		WorkflowStep:  &execution.WorkflowStepScope{Scan: "scans/1", Workflow: "scanWorkflows/default", StageId: "ports", StepId: "port_scan"},
		EngineRelease: &execution.EngineRelease{Engine: "engine.lunafox.port_scan", PackageDigest: digest, EngineApiMajor: 2, CompatibilityRevision: engine.EngineExecutionDiagnosticsCompatibilityRevision},
		RuntimeImage:  &execution.RuntimeImage{Refs: []string{"docker.io/yyhuni/lunafox-engine-runtime-port-scan@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},
		Config:        &execution.FinalEngineConfig{Sections: []*execution.EngineConfigSection{{SectionId: "naabu_active", Enabled: true}}},
		Limits:        &execution.ExecutionLimits{MaxExecutionDuration: durationpb.New(time.Minute), ProgressMessageMaxBytes: 1024, ResultBatchMaxItems: 10, ResultBatchMaxBytes: 1024},
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	controlFixture := &localControlFixture{plan: plan, done: make(chan error, 1)}
	dataFixture := &localFixtureServer{}
	control.RegisterControlPlaneServiceServer(server, controlFixture)
	data.RegisterDataPlaneServiceServer(server, dataFixture)
	data.RegisterExecutionArtifactServiceServer(server, dataFixture)
	go server.Serve(listener)
	defer server.Stop()
	config := Config{ControlAddress: listener.Addr().String(), ExecutionRoot: root, EngineBinDir: engineDir, EngineSourceDir: sourceDir, PackageCacheRoot: cacheDir, ToolsDir: toolsDir, ProxySocket: fixtureProxySocket(t, root), Version: "0.0.1-alpha.229"}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runSession(ctx, config, credentials{AgentID: 1, InstanceID: "fixture", AuthenticationToken: "deadbeef"})
	}()
	select {
	case err := <-controlFixture.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("local Agent did not complete its task")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("local Agent did not stop")
	}
	dataFixture.mu.Lock()
	defer dataFixture.mu.Unlock()
	if dataFixture.progress != 1 || dataFixture.batches != 1 {
		t.Fatalf("progress=%d batches=%d", dataFixture.progress, dataFixture.batches)
	}
}

package localagent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yyhuni/lunafox/contracts/agentexecution"
	control "github.com/yyhuni/lunafox/contracts/gen/lunafox/agent/control/v1"
	data "github.com/yyhuni/lunafox/contracts/gen/lunafox/agent/data/v1"
	execution "github.com/yyhuni/lunafox/contracts/gen/lunafox/agent/execution/v1"
	engine "github.com/yyhuni/lunafox/engine-go/protocol"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

var supportedEngines = map[string]string{
	"engine.lunafox.subdomain_discovery":   "subdomain_discovery",
	"engine.lunafox.port_scan":             "port_scan",
	"engine.lunafox.website_discovery":     "website_discovery",
	"engine.lunafox.url_collection":        "url_collection",
	"engine.lunafox.fingerprint_detection": "fingerprint_detection",
	"engine.lunafox.directory_scan":        "directory_scan",
	"engine.lunafox.screenshot":            "screenshot",
	"engine.lunafox.nuclei_vulnerability":  "nuclei_vulnerability",
}

func executeTask(ctx context.Context, config Config, conn *grpc.ClientConn, identity credentials, sessionID string, epoch int64, plan *execution.ResolvedEngineExecutionPlan) taskResult {
	result := taskResult{task: plan.GetTask(), state: control.TerminalTaskResultState_TERMINAL_TASK_RESULT_STATE_FAILED,
		diagnostics: &engine.EngineExecutionDiagnostics{CompatibilityRevision: engine.EngineExecutionDiagnosticsCompatibilityRevision,
			Availability: engine.EngineExecutionDiagnosticAvailability_ENGINE_EXECUTION_DIAGNOSTIC_AVAILABILITY_UNAVAILABLE,
			ResultState:  engine.EngineExecutionResultState_ENGINE_EXECUTION_RESULT_STATE_UNKNOWN}}
	service, err := runEngine(ctx, config, conn, identity, sessionID, epoch, plan)
	if err == nil {
		result.state = control.TerminalTaskResultState_TERMINAL_TASK_RESULT_STATE_SUCCEEDED
	} else if errors.Is(err, context.Canceled) {
		result.state = control.TerminalTaskResultState_TERMINAL_TASK_RESULT_STATE_CANCELLED
	} else {
		result.failure = &control.FailureDetail{Kind: "runtime_error", Message: "Local Engine execution failed", DisplayMessage: "本地扫描引擎执行失败，请查看任务日志"}
		fmt.Fprintf(os.Stderr, "local task %s failed: %v\n", plan.GetTask(), err)
	}
	if service != nil {
		result.diagnostics = service.terminalDiagnostics(result.state, err)
	}
	return result
}

func runEngine(ctx context.Context, config Config, conn *grpc.ClientConn, identity credentials, sessionID string, epoch int64, plan *execution.ResolvedEngineExecutionPlan) (*engineService, error) {
	if err := agentexecution.ValidateResolvedEngineExecutionPlan(plan); err != nil {
		return nil, fmt.Errorf("validate execution plan: %w", err)
	}
	name, ok := supportedEngines[plan.GetEngineRelease().GetEngine()]
	if !ok {
		return nil, errors.New("Engine is not available in local mode")
	}
	if err := verifyLocalEngineManifest(config, name, plan.GetEngineRelease().GetPackageDigest()); err != nil {
		return nil, err
	}
	proxyEnvironment, forceProxy, err := localProxyEnvironment(config.ProxySocket)
	if err != nil {
		return nil, err
	}
	enginePath := filepath.Join(config.EngineBinDir, name)
	info, err := os.Stat(enginePath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return nil, fmt.Errorf("local Engine %s is not installed", name)
	}
	root, err := os.MkdirTemp(config.ExecutionRoot, "run-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	for _, relative := range []string{"runtime/context", "runtime/credential", "runtime/socket", "runtime/inputs", "workspace", "workspace/tmp"} {
		if err := os.MkdirAll(filepath.Join(root, relative), 0o700); err != nil {
			return nil, err
		}
	}
	deadline := plan.GetLimits().GetMaxExecutionDuration().AsDuration()
	runCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	serverCtx := scopedContext(runCtx, identity, sessionID, epoch)
	artifactClient := data.NewExecutionArtifactServiceClient(conn)
	configResources, err := materializeConfigResources(serverCtx, artifactClient, plan, root)
	if err != nil {
		return nil, err
	}
	platformResources, err := materializePlatformResources(serverCtx, artifactClient, plan, root)
	if err != nil {
		return nil, err
	}
	if err := materializeRuntimeArtifacts(serverCtx, artifactClient, plan, root); err != nil {
		return nil, err
	}
	snapshot, err := projectEngineContext(plan, configResources, platformResources)
	if err != nil {
		return nil, err
	}
	payload, err := proto.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(root, "runtime", "context", "execution.pb"), payload, 0o400); err != nil {
		return nil, err
	}
	var rawToken [32]byte
	if _, err := rand.Read(rawToken[:]); err != nil {
		return nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(rawToken[:])
	if err := os.WriteFile(filepath.Join(root, "runtime", "credential", "token"), []byte(token), 0o400); err != nil {
		return nil, err
	}
	service := &engineService{conn: conn, identity: identity, sessionID: sessionID, epoch: epoch, plan: plan, root: root, token: token, inputs: make(map[string]string)}
	socketPath := filepath.Join(root, "runtime", "socket", "engine.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return service, fmt.Errorf("create local Engine socket: %w", err)
	}
	defer listener.Close()
	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(service.authenticate), grpc.MaxRecvMsgSize(8<<20), grpc.MaxSendMsgSize(8<<20))
	engine.RegisterEngineExecutionReportingServiceServer(grpcServer, service)
	engine.RegisterEngineExecutionInputServiceServer(grpcServer, service)
	engine.RegisterEngineExecutionDiagnosticsServiceServer(grpcServer, service)
	go func() { _ = grpcServer.Serve(listener) }()
	defer grpcServer.Stop()
	command := exec.CommandContext(runCtx, enginePath)
	command.Dir = filepath.Join(root, "workspace")
	// Engines and their child tools must not inherit Server/Agent credentials.
	toolPath := filepath.Join(config.ToolsDir, name) + string(os.PathListSeparator) + config.ToolsDir + string(os.PathListSeparator) + os.Getenv("PATH")
	forceProxyValue := "0"
	if forceProxy {
		forceProxyValue = "1"
	}
	command.Env = []string{engine.NativeExecutionRootEnv + "=" + root, "PATH=" + toolPath,
		"SKYSHIELD_FORCE_SYSTEM_PROXY=" + forceProxyValue,
		"HOME=" + filepath.Join(root, "workspace"), "TMPDIR=" + filepath.Join(root, "workspace", "tmp"), "LANG=C.UTF-8"}
	command.Env = append(command.Env, proxyEnvironment...)
	for _, name := range []string{"SSL_CERT_FILE", "SSL_CERT_DIR", "CHROME_PATH"} {
		if value, ok := os.LookupEnv(name); ok {
			command.Env = append(command.Env, name+"="+value)
		}
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	command.WaitDelay = 5 * time.Second
	var output limitedOutput
	command.Stdout, command.Stderr = &output, &output
	err = command.Run()
	if err != nil {
		if runCtx.Err() != nil {
			return service, runCtx.Err()
		}
		service.reportLocalFailure(ctx, fmt.Errorf("%s: %s", name, strings.TrimSpace(output.String())))
		return service, fmt.Errorf("Engine %s exited: %w", name, err)
	}
	return service, service.validateSuccess()
}

type limitedOutput struct {
	mu sync.Mutex
	bytes.Buffer
}

func (output *limitedOutput) Write(p []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	n := len(p)
	if remaining := 4096 - output.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = output.Buffer.Write(p)
	}
	return n, nil
}

func verifyLocalEngineManifest(config Config, name, digest string) error {
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
		return errors.New("invalid Engine package digest")
	}
	local, err := os.ReadFile(filepath.Join(config.EngineSourceDir, name, "engine.json"))
	if err != nil {
		return fmt.Errorf("read local Engine definition: %w", err)
	}
	installed, err := os.ReadFile(filepath.Join(config.PackageCacheRoot, "expanded", strings.Replace(digest, ":", "-", 1), "engine.json"))
	if err != nil || !bytes.Equal(local, installed) {
		return errors.New("installed Engine definition does not match the local Engine source")
	}
	return nil
}

func projectEngineContext(plan *execution.ResolvedEngineExecutionPlan, resources []*engine.ConfigResource, platform []*engine.PlatformResource) (*engine.EngineExecutionContext, error) {
	targetType := map[execution.TargetType]string{execution.TargetType_TARGET_TYPE_DOMAIN: "domain", execution.TargetType_TARGET_TYPE_IP: "ip", execution.TargetType_TARGET_TYPE_CIDR: "cidr"}[plan.GetTarget().GetType()]
	if targetType == "" {
		return nil, errors.New("unsupported target type")
	}
	sections := make([]*engine.ConfigSection, 0, len(plan.GetConfig().GetSections()))
	for _, section := range plan.GetConfig().GetSections() {
		enabled := section.GetEnabled()
		mapped := &engine.ConfigSection{SectionId: section.GetSectionId(), Enabled: &enabled}
		for _, param := range section.GetParams() {
			value := &engine.ConfigValue{ParamKey: param.GetKey()}
			switch scalar := param.GetValue().GetValue().(type) {
			case *execution.EngineConfigScalar_BoolValue:
				value.Value = &engine.ConfigValue_BooleanValue{BooleanValue: scalar.BoolValue}
			case *execution.EngineConfigScalar_IntegerValue:
				value.Value = &engine.ConfigValue_IntegerValue{IntegerValue: scalar.IntegerValue}
			case *execution.EngineConfigScalar_StringValue:
				value.Value = &engine.ConfigValue_StringValue{StringValue: scalar.StringValue}
			case *execution.EngineConfigScalar_StringArrayValue:
				value.Value = &engine.ConfigValue_StringArrayValue{StringArrayValue: &engine.StringArrayValue{Values: append([]string(nil), scalar.StringArrayValue.GetValues()...)}}
			default:
				return nil, errors.New("unsupported Engine configuration value")
			}
			mapped.Params = append(mapped.Params, value)
		}
		sections = append(sections, mapped)
	}
	limits := plan.GetLimits()
	return &engine.EngineExecutionContext{Target: &engine.CanonicalTarget{Type: targetType, Value: plan.GetTarget().GetValue()}, CompatibilityRevision: engine.EngineExecutionDiagnosticsCompatibilityRevision,
		Config: &engine.EngineExecutionConfig{Sections: sections}, ConfigResources: resources, PlatformResources: platform,
		Limits: &engine.ExecutionLimits{ProgressMessageMaxBytes: limits.GetProgressMessageMaxBytes(), ResultBatchMaxItems: limits.GetResultBatchMaxItems(), ResultBatchMaxBytes: limits.GetResultBatchMaxBytes()}}, nil
}

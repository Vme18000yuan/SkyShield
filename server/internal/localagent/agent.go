// Package localagent runs Engine processes on the same host as the Server.
package localagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	control "github.com/yyhuni/lunafox/contracts/gen/lunafox/agent/control/v1"
	execution "github.com/yyhuni/lunafox/contracts/gen/lunafox/agent/execution/v1"
	"github.com/yyhuni/lunafox/contracts/resourcenames"
	engine "github.com/yyhuni/lunafox/engine-go/protocol"
	"github.com/yyhuni/lunafox/server/internal/grpc/agentcontrol"
	planeauth "github.com/yyhuni/lunafox/server/internal/grpc/planeauth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

type Config struct {
	CredentialsPath  string
	ControlAddress   string
	ExecutionRoot    string
	EngineBinDir     string
	EngineSourceDir  string
	PackageCacheRoot string
	ProxySocket      string
	ToolsDir         string
	Version          string
}

type credentials struct {
	AgentID             int    `json:"agentId"`
	InstanceID          string `json:"instanceId"`
	DisplayName         string `json:"displayName"`
	AuthenticationToken string `json:"agentAuthenticationToken"`
}

func readCredentials(path string) (credentials, error) {
	var result credentials
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return result, errors.New("local Agent credentials must be a mode-0600 regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return result, fmt.Errorf("read local Agent credentials: %w", err)
	}
	if err := json.Unmarshal(data, &result); err != nil || result.AgentID <= 0 || strings.TrimSpace(result.InstanceID) == "" || len(result.AuthenticationToken) != 8 {
		return credentials{}, errors.New("local Agent credentials are invalid")
	}
	return result, nil
}

func validateConfig(config Config) error {
	for _, path := range []string{config.CredentialsPath, config.ExecutionRoot, config.EngineBinDir, config.EngineSourceDir, config.PackageCacheRoot, config.ToolsDir, config.ProxySocket} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
			return errors.New("local Agent paths must be clean absolute paths")
		}
	}
	host, _, err := net.SplitHostPort(config.ControlAddress)
	if err != nil || (host != "localhost" && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback())) {
		return errors.New("local Agent control address must be loopback")
	}
	if strings.TrimSpace(config.Version) == "" {
		return errors.New("local Agent version is required")
	}
	return nil
}

// Run reconnects after a control-plane interruption. A new process session
// fences any interrupted assignment before the Agent can request more work.
func Run(ctx context.Context, config Config) error {
	if err := validateConfig(config); err != nil {
		return err
	}
	if err := CheckDependencies(config); err != nil {
		return err
	}
	identity, err := readCredentials(config.CredentialsPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(config.ExecutionRoot, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(config.ExecutionRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return errors.New("local execution root must be a private directory")
	}
	if owner, ok := info.Sys().(*syscall.Stat_t); !ok || owner.Uid != uint32(os.Getuid()) {
		return errors.New("local execution root must belong to the current user")
	}
	if len(filepath.Join(config.ExecutionRoot, "run-1234567890", "runtime", "socket", "engine.sock")) >= 104 {
		return errors.New("local execution root is too long for a Unix socket")
	}
	for ctx.Err() == nil {
		if err := runSession(ctx, config, identity); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "local Agent connection: %v\n", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(3 * time.Second):
		}
	}
	return nil
}

type sessionEvent struct {
	response *control.ConnectResponse
	err      error
}

type taskResult struct {
	task        string
	state       control.TerminalTaskResultState
	failure     *control.FailureDetail
	diagnostics *engine.EngineExecutionDiagnostics
}

func runSession(ctx context.Context, config Config, identity credentials) error {
	conn, err := grpc.NewClient(config.ControlAddress, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(8<<20), grpc.MaxCallSendMsgSize(8<<20)))
	if err != nil {
		return err
	}
	defer conn.Close()
	sessionID := uuid.NewString()
	streamContext, cancel := context.WithCancel(planeauth.WithAgentAuthenticationToken(ctx, identity.AuthenticationToken))
	defer cancel()
	streamContext = metadata.AppendToOutgoingContext(streamContext, agentcontrol.NativeExecutionMetadataKey, "1")
	stream, err := control.NewControlPlaneServiceClient(conn).Connect(streamContext)
	if err != nil {
		return err
	}
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "skyshield-local"
	}
	agentName := resourcenames.Agent(identity.InstanceID)
	sessionName := resourcenames.AgentSession(identity.InstanceID, sessionID)
	if err := stream.Send(&control.ConnectRequest{Payload: &control.ConnectRequest_RegisterSession{RegisterSession: &control.RegisterSession{
		Agent: agentName, Session: sessionName, ObservedHostname: hostname, AgentVersion: config.Version,
		OperatingSystem: runtime.GOOS, Architecture: runtime.GOARCH,
		SupportedEngineApiMajors: []uint32{engine.EngineAPIMajor}, CompatibilityRevision: engine.EngineExecutionDiagnosticsCompatibilityRevision,
	}}}); err != nil {
		return err
	}
	events := make(chan sessionEvent, 1)
	go func() {
		for {
			message, receiveErr := stream.Recv()
			select {
			case events <- sessionEvent{response: message, err: receiveErr}:
			case <-streamContext.Done():
				return
			}
			if receiveErr != nil {
				return
			}
		}
	}()
	heartbeats := time.NewTicker(15 * time.Second)
	polls := time.NewTicker(2 * time.Second)
	defer heartbeats.Stop()
	defer polls.Stop()
	started := time.Now()
	var epoch int64
	var pendingRequest string
	var runningTask string
	var cancelTask context.CancelFunc
	var resultChannel <-chan taskResult
	awaitingAck := false
	allowTasks := true
	var lastReadinessLog time.Time
	defer func() {
		if cancelTask != nil {
			cancelTask()
			if resultChannel != nil {
				<-resultChannel
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case event := <-events:
			if event.err != nil {
				return event.err
			}
			if event.response == nil {
				return errors.New("empty control response")
			}
			switch message := event.response.GetPayload().(type) {
			case *control.ConnectResponse_SessionReady:
				if epoch != 0 || message.SessionReady.GetSessionEpoch() <= 0 || message.SessionReady.GetCompatibilityRevision() != engine.EngineExecutionDiagnosticsCompatibilityRevision {
					return errors.New("invalid local Agent session response")
				}
				epoch = message.SessionReady.GetSessionEpoch()
				if setting := message.SessionReady.GetConfigSnapshot(); setting != nil && setting.MaxTasks != nil {
					allowTasks = setting.GetMaxTasks() > 0
				}
				fmt.Fprintln(os.Stderr, "local Agent connected")
			case *control.ConnectResponse_TaskAssign:
				assignment := message.TaskAssign
				if assignment == nil || pendingRequest == "" || assignment.GetRequestId() != pendingRequest || runningTask != "" {
					return errors.New("invalid local Agent task assignment")
				}
				pendingRequest = ""
				switch outcome := assignment.GetOutcome().(type) {
				case *control.TaskAssign_NoTask:
					if outcome.NoTask == nil {
						return errors.New("invalid empty task assignment")
					}
				case *control.TaskAssign_Plan:
					if outcome.Plan == nil {
						return errors.New("missing execution plan")
					}
					runningTask = outcome.Plan.GetTask()
					taskCtx, taskCancel := context.WithCancel(ctx)
					cancelTask = taskCancel
					completed := make(chan taskResult, 1)
					resultChannel = completed
					go func(plan *execution.ResolvedEngineExecutionPlan) {
						completed <- executeTask(taskCtx, config, conn, identity, sessionID, epoch, plan)
					}(outcome.Plan)
				default:
					return errors.New("invalid task assignment outcome")
				}
			case *control.ConnectResponse_TaskCancel:
				if message.TaskCancel.GetTask() == runningTask && cancelTask != nil {
					cancelTask()
				}
			case *control.ConnectResponse_TerminalTaskResultAck:
				if !awaitingAck || message.TerminalTaskResultAck.GetTask() != runningTask || message.TerminalTaskResultAck.GetSessionEpoch() != epoch {
					return errors.New("invalid terminal result acknowledgement")
				}
				awaitingAck, runningTask = false, ""
			case *control.ConnectResponse_ConfigUpdate:
				if message.ConfigUpdate != nil && message.ConfigUpdate.MaxTasks != nil {
					allowTasks = message.ConfigUpdate.GetMaxTasks() > 0
				}
			case *control.ConnectResponse_UpdateRequired:
				// Local mode is updated with the Server, and runs at most one task.
			}
		case result := <-resultChannel:
			resultChannel = nil
			if cancelTask != nil {
				cancelTask()
			}
			cancelTask = nil
			if result.task != runningTask || awaitingAck {
				return errors.New("local Agent task result does not match assignment")
			}
			if err := stream.Send(&control.ConnectRequest{Payload: &control.ConnectRequest_TerminalTaskResult{TerminalTaskResult: &control.TerminalTaskResult{
				Task: result.task, Result: result.state, Failure: result.failure, Diagnostics: result.diagnostics,
				CompatibilityRevision: engine.EngineExecutionDiagnosticsCompatibilityRevision,
			}}}); err != nil {
				return err
			}
			awaitingAck = true
		case <-heartbeats.C:
			if epoch == 0 {
				continue
			}
			used := int32(0)
			if runningTask != "" && !awaitingAck {
				used = 1
			}
			if err := stream.Send(&control.ConnectRequest{Payload: &control.ConnectRequest_Heartbeat{Heartbeat: &control.Heartbeat{
				Agent: agentName, Session: sessionName, ObservedHostname: hostname, AgentVersion: config.Version,
				OperatingSystem: runtime.GOOS, Architecture: runtime.GOARCH, RunningTasks: used, TaskSlotsUsed: used,
				UptimeSeconds: int64(time.Since(started).Seconds()), SupportedEngineApiMajors: []uint32{engine.EngineAPIMajor},
				Health: &control.HealthStatus{State: control.HealthState_HEALTH_STATE_HEALTHY}, CompatibilityRevision: engine.EngineExecutionDiagnosticsCompatibilityRevision,
			}}}); err != nil {
				return err
			}
		case <-polls.C:
			if epoch == 0 || !allowTasks || runningTask != "" || pendingRequest != "" || awaitingAck {
				continue
			}
			readinessErr := CheckDependencies(config)
			if readinessErr == nil {
				_, _, readinessErr = localProxyEnvironment(config.ProxySocket)
			}
			if readinessErr != nil {
				if time.Since(lastReadinessLog) >= 30*time.Second {
					fmt.Fprintf(os.Stderr, "local Agent is waiting for execution dependencies: %v\n", readinessErr)
					lastReadinessLog = time.Now()
				}
				continue
			}
			pendingRequest = uuid.NewString()
			if err := stream.Send(&control.ConnectRequest{Payload: &control.ConnectRequest_RequestTask{RequestTask: &control.RequestTask{RequestId: pendingRequest}}}); err != nil {
				return err
			}
		}
	}
}

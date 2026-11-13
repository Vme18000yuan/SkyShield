package localagent

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	control "github.com/yyhuni/lunafox/contracts/gen/lunafox/agent/control/v1"
	data "github.com/yyhuni/lunafox/contracts/gen/lunafox/agent/data/v1"
	execution "github.com/yyhuni/lunafox/contracts/gen/lunafox/agent/execution/v1"
	progress "github.com/yyhuni/lunafox/contracts/gen/lunafox/taskprogress/v1"
	engine "github.com/yyhuni/lunafox/engine-go/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// engineService is bound to one claimed task and one private Unix socket.
// Its RPCs cannot select another task, execution, Agent or destination.
type engineService struct {
	engine.UnimplementedEngineExecutionReportingServiceServer
	engine.UnimplementedEngineExecutionInputServiceServer
	engine.UnimplementedEngineExecutionDiagnosticsServiceServer
	conn        *grpc.ClientConn
	identity    credentials
	sessionID   string
	epoch       int64
	plan        *execution.ResolvedEngineExecutionPlan
	root        string
	token       string
	sequence    atomic.Int64
	inputMu     sync.Mutex
	inputs      map[string]string
	diagMu      sync.Mutex
	established bool
	diagnostic  *engine.EngineTerminalDiagnosticSnapshot
}

func (service *engineService) authenticate(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	values := md.Get("authorization")
	want := "Bearer " + service.token
	if !ok || len(values) != 1 || subtle.ConstantTimeCompare([]byte(values[0]), []byte(want)) != 1 {
		return nil, status.Error(codes.Unauthenticated, "invalid Engine execution credential")
	}
	return handler(ctx, req)
}

func (service *engineService) serverContext(ctx context.Context) context.Context {
	return scopedContext(ctx, service.identity, service.sessionID, service.epoch)
}

func (service *engineService) ReportProgress(ctx context.Context, request *engine.ReportProgressRequest) (*engine.ReportProgressResponse, error) {
	if request == nil || request.GetMessage() == "" || len(request.GetMessage()) > int(service.plan.GetLimits().GetProgressMessageMaxBytes()) || len(request.GetMessage()) > 16*1024 {
		return nil, status.Error(codes.InvalidArgument, "invalid progress message")
	}
	_, err := data.NewDataPlaneServiceClient(service.conn).BatchWriteTaskProgressLogs(service.serverContext(ctx), &data.BatchWriteTaskProgressLogsRequest{
		Task: service.plan.GetTask(), RequestId: uuid.NewString(), Entries: []*progress.TaskProgressLogEntry{{
			Sequence: service.sequence.Add(1), Level: progress.TaskProgressLogLevel_TASK_PROGRESS_LOG_LEVEL_INFO,
			Content: request.GetMessage(), EmittedAt: timestamppb.New(time.Now().UTC()),
		}},
	})
	if err != nil {
		return nil, err
	}
	return &engine.ReportProgressResponse{}, nil
}

func (service *engineService) SubmitResultBatch(ctx context.Context, request *engine.SubmitResultBatchRequest) (*engine.SubmitResultBatchResponse, error) {
	if request == nil || len(request.GetItems()) == 0 || len(request.GetItems()) > int(service.plan.GetLimits().GetResultBatchMaxItems()) {
		return nil, status.Error(codes.InvalidArgument, "invalid Engine result batch")
	}
	items := make([]string, len(request.GetItems()))
	total := 0
	for i, item := range request.GetItems() {
		total += len(item)
		items[i] = string(item)
	}
	if total > int(service.plan.GetLimits().GetResultBatchMaxBytes()) {
		return nil, status.Error(codes.ResourceExhausted, "Engine result batch is too large")
	}
	_, err := data.NewDataPlaneServiceClient(service.conn).BatchIngestTaskResults(service.serverContext(ctx), &data.BatchIngestTaskResultsRequest{
		Task: service.plan.GetTask(), Target: service.plan.GetTarget().GetResource(), ResultType: request.GetResultType(), ItemsJson: items,
	})
	if err != nil {
		return nil, err
	}
	return &engine.SubmitResultBatchResponse{}, nil
}

func (service *engineService) MaterializeExecutionInput(ctx context.Context, request *engine.MaterializeExecutionInputRequest) (*engine.MaterializeExecutionInputResponse, error) {
	if request == nil || request.GetRole() == "" {
		return nil, status.Error(codes.InvalidArgument, "input role is required")
	}
	service.inputMu.Lock()
	defer service.inputMu.Unlock()
	if path := service.inputs[request.GetRole()]; path != "" {
		return &engine.MaterializeExecutionInputResponse{Path: path}, nil
	}
	path, err := inputStream(service.serverContext(ctx), service.conn, service.plan, request.GetRole(), service.root)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "materialize Engine input failed")
	}
	service.inputs[request.GetRole()] = path
	return &engine.MaterializeExecutionInputResponse{Path: path}, nil
}

func (service *engineService) EstablishExecutionDiagnostics(_ context.Context, request *engine.EstablishExecutionDiagnosticsRequest) (*engine.EstablishExecutionDiagnosticsResponse, error) {
	if err := engine.ValidateEngineExecutionDiagnosticsEstablishment(request); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	service.diagMu.Lock()
	service.established = true
	service.diagMu.Unlock()
	return &engine.EstablishExecutionDiagnosticsResponse{}, nil
}

func (service *engineService) ReportTerminalDiagnostics(_ context.Context, request *engine.ReportTerminalDiagnosticsRequest) (*engine.ReportTerminalDiagnosticsResponse, error) {
	if request == nil || engine.ValidateEngineTerminalDiagnosticSnapshot(request.GetSnapshot()) != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid Engine diagnostics")
	}
	service.diagMu.Lock()
	defer service.diagMu.Unlock()
	if !service.established {
		return nil, status.Error(codes.FailedPrecondition, "Engine diagnostics session was not established")
	}
	if service.diagnostic != nil && !proto.Equal(service.diagnostic, request.GetSnapshot()) {
		return nil, status.Error(codes.FailedPrecondition, "Engine diagnostics already recorded")
	}
	service.diagnostic = proto.Clone(request.GetSnapshot()).(*engine.EngineTerminalDiagnosticSnapshot)
	return &engine.ReportTerminalDiagnosticsResponse{}, nil
}

func (service *engineService) validateSuccess() error {
	service.diagMu.Lock()
	defer service.diagMu.Unlock()
	if !service.established || service.diagnostic == nil {
		return fmt.Errorf("Engine did not complete its reporting session")
	}
	if service.diagnostic.FailedStage != nil || service.diagnostic.ErrorType != nil {
		return fmt.Errorf("Engine reported a terminal failure")
	}
	for _, watermark := range service.diagnostic.GetResultTypeWatermarks() {
		if watermark.GetSubmittedItems() != watermark.GetAcknowledgedItems() || watermark.GetSubmittedBatches() != watermark.GetAcknowledgedBatches() {
			return fmt.Errorf("Engine result delivery was not fully acknowledged")
		}
	}
	return nil
}

func (service *engineService) terminalDiagnostics(state control.TerminalTaskResultState, runErr error) *engine.EngineExecutionDiagnostics {
	service.diagMu.Lock()
	defer service.diagMu.Unlock()
	result := &engine.EngineExecutionDiagnostics{CompatibilityRevision: engine.EngineExecutionDiagnosticsCompatibilityRevision,
		Availability: engine.EngineExecutionDiagnosticAvailability_ENGINE_EXECUTION_DIAGNOSTIC_AVAILABILITY_UNAVAILABLE,
		ResultState:  engine.EngineExecutionResultState_ENGINE_EXECUTION_RESULT_STATE_UNKNOWN}
	if service.diagnostic == nil {
		return result
	}
	result.Availability = engine.EngineExecutionDiagnosticAvailability_ENGINE_EXECUTION_DIAGNOSTIC_AVAILABILITY_AVAILABLE
	result.FailedStage, result.ErrorType = service.diagnostic.FailedStage, service.diagnostic.ErrorType
	result.ResultTypeWatermarks = service.diagnostic.ResultTypeWatermarks
	acknowledged, unresolved := false, false
	for _, watermark := range result.ResultTypeWatermarks {
		acknowledged = acknowledged || watermark.GetAcknowledgedItems() > 0 || watermark.GetAcknowledgedBatches() > 0
		unresolved = unresolved || watermark.GetSubmittedBatches() > watermark.GetAcknowledgedBatches()
	}
	if state == control.TerminalTaskResultState_TERMINAL_TASK_RESULT_STATE_SUCCEEDED {
		result.ResultState = engine.EngineExecutionResultState_ENGINE_EXECUTION_RESULT_STATE_COMPLETE
		if unresolved {
			result.ResultState = engine.EngineExecutionResultState_ENGINE_EXECUTION_RESULT_STATE_UNKNOWN
		}
		return result
	}
	result.ResultState = engine.EngineExecutionResultState_ENGINE_EXECUTION_RESULT_STATE_NONE
	if acknowledged {
		result.ResultState = engine.EngineExecutionResultState_ENGINE_EXECUTION_RESULT_STATE_PARTIAL
	} else if unresolved {
		result.ResultState = engine.EngineExecutionResultState_ENGINE_EXECUTION_RESULT_STATE_UNKNOWN
	}
	if result.FailedStage == nil || result.ErrorType == nil {
		stage := engine.EngineExecutionFailedStage_ENGINE_EXECUTION_FAILED_STAGE_UNKNOWN
		kind := engine.EngineExecutionErrorType_ENGINE_EXECUTION_ERROR_TYPE_EXECUTION_FAILED
		if errors.Is(runErr, context.Canceled) {
			kind = engine.EngineExecutionErrorType_ENGINE_EXECUTION_ERROR_TYPE_CANCELLED
		}
		if errors.Is(runErr, context.DeadlineExceeded) {
			kind = engine.EngineExecutionErrorType_ENGINE_EXECUTION_ERROR_TYPE_TIMEOUT
		}
		result.FailedStage, result.ErrorType = &stage, &kind
	}
	return result
}

func (service *engineService) reportLocalFailure(ctx context.Context, err error) {
	if err == nil || ctx.Err() != nil {
		return
	}
	message := fmt.Sprintf("[local Agent] %v", err)
	if len(message) > 1000 {
		message = message[:1000]
	}
	_, _ = service.ReportProgress(ctx, &engine.ReportProgressRequest{Message: message})
}

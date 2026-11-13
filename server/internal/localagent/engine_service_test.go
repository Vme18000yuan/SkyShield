package localagent

import (
	"context"
	"testing"

	control "github.com/yyhuni/lunafox/contracts/gen/lunafox/agent/control/v1"
	engine "github.com/yyhuni/lunafox/engine-go/protocol"
)

func TestTerminalDiagnosticsMatchesExecutionOutcome(t *testing.T) {
	service := &engineService{established: true, diagnostic: &engine.EngineTerminalDiagnosticSnapshot{
		CompatibilityRevision: engine.EngineExecutionDiagnosticsCompatibilityRevision,
		ResultTypeWatermarks:  []*engine.ResultTypeWatermark{{ResultType: "asset.subdomain.v1", ReceivedItems: 1, EncodedItems: 1, SubmittedItems: 1, AcknowledgedItems: 1, SubmittedBatches: 1, AcknowledgedBatches: 1}},
	}}
	success := service.terminalDiagnostics(control.TerminalTaskResultState_TERMINAL_TASK_RESULT_STATE_SUCCEEDED, nil)
	if err := engine.ValidateEngineExecutionDiagnostics(success); err != nil || success.GetResultState() != engine.EngineExecutionResultState_ENGINE_EXECUTION_RESULT_STATE_COMPLETE || success.FailedStage != nil {
		t.Fatalf("success diagnostics = %v, err=%v", success, err)
	}
	cancelled := service.terminalDiagnostics(control.TerminalTaskResultState_TERMINAL_TASK_RESULT_STATE_CANCELLED, context.Canceled)
	if err := engine.ValidateEngineExecutionDiagnostics(cancelled); err != nil || cancelled.GetResultState() != engine.EngineExecutionResultState_ENGINE_EXECUTION_RESULT_STATE_PARTIAL || cancelled.GetErrorType() != engine.EngineExecutionErrorType_ENGINE_EXECUTION_ERROR_TYPE_CANCELLED {
		t.Fatalf("cancelled diagnostics = %v, err=%v", cancelled, err)
	}
}

func TestEngineCannotSucceedWithoutDiagnostics(t *testing.T) {
	service := &engineService{}
	if service.validateSuccess() == nil {
		t.Fatal("Engine without a reporting session succeeded")
	}
	service.established = true
	if service.validateSuccess() == nil {
		t.Fatal("Engine without terminal diagnostics succeeded")
	}
}

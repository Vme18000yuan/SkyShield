package airuntime

import "context"

type processPermitKey struct{}
type auxiliaryProcessKey struct{}
type processPermitScope struct {
	governor *ExecutionGovernor
	user     int
}

func WithProcessGovernor(ctx context.Context, g *ExecutionGovernor, user int) context.Context {
	return context.WithValue(ctx, processPermitKey{}, processPermitScope{g, user})
}

// Process permits are separate from root permits to avoid nested admission deadlocks.
func AcquireToolProcess(ctx context.Context, wait bool) (context.Context, func(), error) {
	scope, _ := ctx.Value(processPermitKey{}).(processPermitScope)
	kind := "process"
	if auxiliary, _ := ctx.Value(auxiliaryProcessKey{}).(bool); auxiliary {
		kind = "process_aux"
	}
	return scope.governor.Acquire(ctx, scope.user, kind, wait)
}

func withAuxiliaryProcess(ctx context.Context) context.Context {
	return context.WithValue(ctx, auxiliaryProcessKey{}, true)
}
func AcquireAuxiliaryToolProcess(ctx context.Context) (context.Context, func(), error) {
	return AcquireToolProcess(withAuxiliaryProcess(ctx), true)
}

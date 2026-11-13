package airuntime

import "context"

type executionIdentityKey struct{}
type taskTargetKey struct{}

func withExecutionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, executionIdentityKey{}, id)
}

func executionID(ctx context.Context) string {
	value, _ := ctx.Value(executionIdentityKey{}).(string)
	return value
}

// ExecutionID returns the trusted run identity inserted by the host runtime.
func ExecutionID(ctx context.Context) string { return executionID(ctx) }

func withTaskTarget(ctx context.Context, id int) context.Context {
	return context.WithValue(ctx, taskTargetKey{}, id)
}
func taskTarget(ctx context.Context) int { value, _ := ctx.Value(taskTargetKey{}).(int); return value }

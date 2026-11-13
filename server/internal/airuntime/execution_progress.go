package airuntime

import "context"

type executionProgressKey struct{}

func withExecutionProgress(ctx context.Context, emit func(string)) context.Context {
	return context.WithValue(ctx, executionProgressKey{}, emit)
}
func executionProgress(ctx context.Context, message string) {
	if emit, ok := ctx.Value(executionProgressKey{}).(func(string)); ok {
		emit(message)
	}
}

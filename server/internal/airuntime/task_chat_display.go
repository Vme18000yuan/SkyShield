package airuntime

import "context"

type taskChatDisplayKey struct{}
type taskChatSegmentKey struct{}
type taskChatDetailKey struct{}

// WithTaskChatSegment selects a validated task-owned conversation segment.
func WithTaskChatSegment(ctx context.Context, segment int) context.Context {
	return context.WithValue(ctx, taskChatSegmentKey{}, segment)
}
func taskChatSegment(ctx context.Context) (int, bool) {
	segment, ok := ctx.Value(taskChatSegmentKey{}).(int)
	return segment, ok
}

// WithTaskChatDisplay preserves the user's visible message when the runtime
// receives a server-generated context snapshot in addition to that message.
func WithTaskChatDisplay(ctx context.Context, message string) context.Context {
	return context.WithValue(ctx, taskChatDisplayKey{}, message)
}
func taskChatDisplay(ctx context.Context) string {
	v, _ := ctx.Value(taskChatDisplayKey{}).(string)
	return v
}

// WithTaskChatDetail preserves attachment metadata for the visible transcript.
func WithTaskChatDetail(ctx context.Context, detail string) context.Context {
	return context.WithValue(ctx, taskChatDetailKey{}, detail)
}
func taskChatDetail(ctx context.Context) string {
	v, _ := ctx.Value(taskChatDetailKey{}).(string)
	return v
}

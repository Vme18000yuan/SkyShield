package airuntime

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/norma/llm"
)

var ErrTaskModelChainExhausted = errors.New("任务模型链额度已耗尽")

type taskExecutionContextKey struct{}
type pinnedWorkerContextKey struct{}

func WithPinnedWorkerProfile(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, pinnedWorkerContextKey{}, id)
}
func PinnedWorkerProfileFromContext(ctx context.Context) string {
	value, _ := ctx.Value(pinnedWorkerContextKey{}).(string)
	return value
}

func WithTaskExecution(ctx context.Context, task TaskExecution) context.Context {
	return context.WithValue(ctx, taskExecutionContextKey{}, task)
}
func TaskExecutionFromContext(ctx context.Context) (TaskExecution, bool) {
	task, ok := ctx.Value(taskExecutionContextKey{}).(TaskExecution)
	return task, ok
}

type TaskModelSelection struct {
	Provider  llm.Provider
	Model     string
	ProfileID string
	Revision  int64
}
type TaskModelTransition struct{ Stale, Advanced, Exhausted bool }
type TaskModelSelector func(context.Context) (TaskModelSelection, error)
type TaskModelExhauster func(context.Context, TaskModelSelection, error) (TaskModelTransition, error)

// TaskModelRouter mirrors ARTEX's task-scoped chain. It never replays a
// response after content or a tool call has reached the Agent.
type TaskModelRouter struct {
	Context context.Context
	Select  TaskModelSelector
	Exhaust TaskModelExhauster
}

func (r *TaskModelRouter) ContextWindowTokens() int { return r.RuntimeModelOptions().ContextWindow }

func (r *TaskModelRouter) RuntimeModelOptions() RuntimeModelOptions {
	selection, err := r.Select(r.Context)
	if err == nil && selection.Provider != nil {
		return ModelOptions(selection.Provider)
	}
	return defaultModelOptions()
}
func (r *TaskModelRouter) Complete(ctx context.Context, request llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	for {
		selection, err := r.Select(ctx)
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		if selection.Provider == nil {
			return llm.Message{}, "", llm.Usage{}, errors.New("任务模型不可用")
		}
		options := ModelOptions(selection.Provider)
		retries := 2
		if options.StreamAttempts != 0 {
			retries = max(options.StreamAttempts, 0)
		}
		var message llm.Message
		var stop string
		var usage llm.Usage
		callCtx := llm.WithRequestBudget(ctx, max(4, options.MaxRequestAttempts, retries+1))
		if retries > 0 {
			callCtx = llm.WithConnectRetryLimit(callCtx, 0)
		}
		for attempt := 0; ; attempt++ {
			message, stop, usage, err = selection.Provider.Complete(callCtx, request)
			if err == nil || ctx.Err() != nil || attempt >= retries || !retryableStreamError(err) {
				break
			}
			delay := min(500*time.Millisecond*time.Duration(1<<min(attempt, 4)), 4*time.Second)
			if options.StreamInterval > 0 {
				delay = options.StreamInterval
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return llm.Message{}, "", llm.Usage{}, ctx.Err()
			case <-timer.C:
			}
		}
		if err == nil {
			return message, stop, usage, nil
		}
		if ctx.Err() == nil && retryableStreamError(err) {
			err = errors.Join(ErrModelRecoveryExhausted, err)
		}
		if selection.ProfileID == "" || ctx.Err() != nil || !agent.IsQuotaExhaustedMessage(err.Error()) {
			return llm.Message{}, "", llm.Usage{}, err
		}
		transition, markErr := r.Exhaust(ctx, selection, err)
		if markErr != nil {
			return llm.Message{}, "", llm.Usage{}, fmt.Errorf("保存任务模型切换失败: %w", markErr)
		}
		if transition.Exhausted && !transition.Stale {
			return llm.Message{}, "", llm.Usage{}, errors.Join(ErrTaskModelChainExhausted, err)
		}
	}
}
func (r *TaskModelRouter) Stream(ctx context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		for {
			selection, err := r.Select(ctx)
			if err != nil {
				yield(llm.StreamEvent{}, err)
				return
			}
			if selection.Provider == nil {
				yield(llm.StreamEvent{}, errors.New("任务模型不可用"))
				return
			}
			committed := false
			pending := []llm.StreamEvent{}
			var failure error
			for event, streamErr := range selection.Provider.Stream(ctx, request) {
				if streamErr != nil {
					failure = streamErr
					break
				}
				if !committed && !streamCommitsOutput(event) && len(pending) < 256 {
					pending = append(pending, event)
					continue
				}
				if !committed {
					for _, buffered := range pending {
						if !yield(buffered, nil) {
							return
						}
					}
					pending = nil
					committed = true
				}
				if !yield(event, nil) {
					return
				}
			}
			if failure == nil {
				for _, buffered := range pending {
					if !yield(buffered, nil) {
						return
					}
				}
				return
			}
			if selection.ProfileID == "" || ctx.Err() != nil || !agent.IsQuotaExhaustedMessage(failure.Error()) {
				for _, buffered := range pending {
					if !yield(buffered, nil) {
						return
					}
				}
				yield(llm.StreamEvent{}, failure)
				return
			}
			transition, markErr := r.Exhaust(ctx, selection, failure)
			if markErr != nil {
				yield(llm.StreamEvent{}, fmt.Errorf("保存任务模型切换失败: %w", markErr))
				return
			}
			if committed {
				if transition.Exhausted && !transition.Stale {
					failure = errors.Join(ErrTaskModelChainExhausted, failure)
				}
				yield(llm.StreamEvent{}, failure)
				return
			}
			if transition.Exhausted && !transition.Stale {
				yield(llm.StreamEvent{}, errors.Join(ErrTaskModelChainExhausted, failure))
				return
			}
		}
	}
}

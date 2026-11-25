package application

import (
	"context"
	"github.com/Autumn-27/norma/llm"
	"sync"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/pkg"
	"go.uber.org/zap"
)

type mainChatHandle struct {
	run    string
	user   int
	cancel context.CancelCauseFunc
}

type taskRunHandle struct {
	run    string
	cancel context.CancelCauseFunc
}

type TaskController struct {
	store        TaskStore
	runtime      TaskRuntime
	provider     ProviderResolver
	models       TaskModelResolver
	workerSlots  func(context.Context, int, int) ([]string, error)
	instance     string
	wake         chan struct{}
	once         sync.Once
	done         chan struct{}
	mu           sync.Mutex
	active       map[int64]taskRunHandle
	activeChats  map[int64]mainChatHandle
	chatWG       sync.WaitGroup
	serverCtx    context.Context
	shuttingDown bool
}

func NewTaskController(store TaskStore, runtime TaskRuntime, provider ProviderResolver) *TaskController {
	return &TaskController{store: store, runtime: runtime, provider: provider, instance: uuid.NewString(), wake: make(chan struct{}, 1), done: make(chan struct{}), active: map[int64]taskRunHandle{}, activeChats: map[int64]mainChatHandle{}}
}
func (c *TaskController) SetTaskModelResolver(resolve TaskModelResolver) { c.models = resolve }
func (c *TaskController) SetWorkerSlotsResolver(resolve func(context.Context, int, int) ([]string, error)) {
	c.workerSlots = resolve
}

func (c *TaskController) Start(ctx context.Context) {
	c.once.Do(func() { c.mu.Lock(); c.serverCtx = ctx; c.mu.Unlock(); go c.run(ctx) })
}
func (c *TaskController) Done() <-chan struct{} { return c.done }
func (c *TaskController) Notify() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}
func (c *TaskController) Cancel(id int64) {
	c.cancel(id, agent.AbortPausedByUser)
}

func (c *TaskController) HasActiveExecution(id int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, task := c.active[id]
	_, chat := c.activeChats[id]
	return task || chat
}
func (c *TaskController) CancelBySchedule(id int64) {
	c.cancel(id, agent.AbortPausedBySchedule)
}
func (c *TaskController) CancelByAuto(id int64) { c.cancel(id, agent.AbortPausedByOrchestrator) }

func (c *TaskController) Chat(ctx context.Context, user int, id int64, message string) (string, error) {
	leases, ok := c.store.(TaskMainChatLeaseStore)
	runtime, independent := c.runtime.(TaskIndependentChatRuntime)
	if !ok || !independent {
		return c.chatActiveTask(ctx, user, id, message)
	}
	run := uuid.NewString()
	task, err := leases.ClaimMainChat(ctx, user, id, run)
	if err != nil {
		return "", err
	}
	chatCtx, finish, err := c.registerMainChat(ctx, task, run, leases)
	if err != nil {
		return "", err
	}
	defer finish()
	return c.executeMainChat(chatCtx, task, run, message, leases, runtime)
}

// StartChat admits the turn before returning; request disconnection does not
// cancel it. The server shutdown and the task/chat stop actions still do.
func (c *TaskController) StartChat(ctx context.Context, user int, id int64, message string) error {
	leases, ok := c.store.(TaskMainChatLeaseStore)
	runtime, independent := c.runtime.(TaskIndependentChatRuntime)
	if !ok || !independent {
		return ErrTaskChatUnavailable
	}
	run := uuid.NewString()
	task, err := leases.ClaimMainChat(ctx, user, id, run)
	if err != nil {
		return err
	}
	chatCtx, finish, err := c.registerMainChat(context.WithoutCancel(ctx), task, run, leases)
	if err != nil {
		return err
	}
	go func() {
		defer finish()
		if _, err := c.executeMainChat(chatCtx, task, run, message, leases, runtime); err != nil && chatCtx.Err() == nil {
			pkg.Warn("AI MainAgent turn failed", zap.Int64("ai.task_id", task.ID))
		}
	}()
	return nil
}

func (c *TaskController) registerMainChat(ctx context.Context, task *Task, run string, leases TaskMainChatLeaseStore) (context.Context, func(), error) {
	chatCtx, cancel := context.WithCancelCause(ctx)
	c.mu.Lock()
	if c.shuttingDown {
		c.mu.Unlock()
		cancel(agent.AbortShutdown)
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = leases.ReleaseMainChat(cleanup, task.OwnerUserID, task.ID, run)
		return nil, nil, ErrTaskChatUnavailable
	}
	if previous, exists := c.activeChats[task.ID]; exists {
		previous.cancel(ErrRunLeaseLost)
	}
	c.activeChats[task.ID] = mainChatHandle{run: run, user: task.OwnerUserID, cancel: cancel}
	c.chatWG.Add(1)
	serverCtx := c.serverCtx
	c.mu.Unlock()
	stopServer := func() bool { return false }
	if serverCtx != nil {
		stopServer = context.AfterFunc(serverCtx, func() { cancel(agent.AbortShutdown) })
	}
	finish := func() {
		stopServer()
		cancel(context.Canceled)
		c.mu.Lock()
		if current, ok := c.activeChats[task.ID]; ok && current.run == run {
			delete(c.activeChats, task.ID)
		}
		c.mu.Unlock()
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		if err := leases.ReleaseMainChat(cleanup, task.OwnerUserID, task.ID, run); err != nil {
			pkg.Warn("AI MainAgent lease release failed")
		}
		stop()
		c.chatWG.Done()
	}
	return chatCtx, finish, nil
}

func (c *TaskController) executeMainChat(chatCtx context.Context, task *Task, run, message string, leases TaskMainChatLeaseStore, runtime TaskIndependentChatRuntime) (string, error) {
	// Catch a stop committed in the narrow interval between admission and registration.
	valid, err := leases.HeartbeatMainChat(chatCtx, task.OwnerUserID, task.ID, run)
	if err != nil {
		return "", err
	}
	if !valid {
		return "", ErrRunLeaseLost
	}
	runCtx, cancel := context.WithCancelCause(chatCtx)
	defer cancel(context.Canceled)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				valid, err := leases.HeartbeatMainChat(runCtx, task.OwnerUserID, task.ID, run)
				if err != nil || !valid {
					cancel(ErrRunLeaseLost)
					return
				}
			}
		}
	}()
	defer func() { cancel(context.Canceled); <-heartbeatDone }()
	var provider llm.Provider
	var model string
	// Role-specific bindings may exist even without a global default.
	if c.models != nil {
		provider, model, _, _ = c.models(runCtx, task.OwnerUserID, task.LLMProfileIDs, task.ActiveLLMProfileID)
	} else if c.provider != nil {
		provider, model, _, _ = c.provider(runCtx, task.OwnerUserID)
	}
	target := 0
	if task.TargetID != nil {
		target = *task.TargetID
	}
	return runtime.ChatIndependent(runCtx, airuntime.TaskExecution{
		ID: task.ID, ExplorationID: task.ExplorationID, TenantID: task.TenantID, UserID: task.OwnerUserID,
		RunID: run, ExecutionMode: "main_chat", Goal: task.Goal, Description: task.Description, TargetID: target,
		WorkerCount: task.WorkerCount, MaxTurns: task.MaxTurns, PlanHeartbeatSeconds: task.PlanHeartbeatSeconds, CoverageEnabled: &task.CoverageEnabled,
	}, provider, model, message)
}

func (c *TaskController) StopChat(ctx context.Context, user int, id int64) (bool, error) {
	leases, ok := c.store.(TaskMainChatLeaseStore)
	if !ok {
		return false, ErrTaskChatUnavailable
	}
	run, err := leases.StopMainChat(ctx, user, id)
	if err != nil {
		return false, err
	}
	c.mu.Lock()
	handle, exists := c.activeChats[id]
	c.mu.Unlock()
	if exists && run != "" && handle.run == run {
		handle.cancel(agent.AbortChatStoppedByUser)
	}
	return run != "", nil
}
func (c *TaskController) ChatRunning(ctx context.Context, user int, id int64) (bool, error) {
	leases, ok := c.store.(TaskMainChatLeaseStore)
	if !ok {
		return false, ErrTaskChatUnavailable
	}
	return leases.MainChatRunning(ctx, user, id)
}

func (c *TaskController) chatActiveTask(ctx context.Context, user int, id int64, message string) (string, error) {
	task, err := c.store.GetTask(ctx, user, id)
	if err != nil {
		return "", err
	}
	if task.Status != "running" || task.Queued || task.ActiveRun == "" || task.LeaseUntil == nil || !task.LeaseUntil.After(time.Now()) {
		return "", ErrTaskControlConflict
	}
	runtime, ok := c.runtime.(TaskChatRuntime)
	if !ok {
		return "", ErrTaskChatUnavailable
	}
	target := 0
	if task.TargetID != nil {
		target = *task.TargetID
	}
	var deadline time.Time
	if task.DeadlineAt != nil {
		deadline = *task.DeadlineAt
	}
	return runtime.ChatTask(ctx, airuntime.TaskExecution{
		ID: task.ID, ExplorationID: task.ExplorationID, TenantID: task.TenantID, UserID: task.OwnerUserID,
		RunID: task.ActiveRun, Goal: task.Goal, Description: task.Description, TargetID: target,
		WorkerCount: task.WorkerCount, MaxTurns: task.MaxTurns, Deadline: deadline, PlanHeartbeatSeconds: task.PlanHeartbeatSeconds, CoverageEnabled: &task.CoverageEnabled,
	}, message)
}

func (c *TaskController) cancel(id int64, cause error) {
	c.mu.Lock()
	cancel := c.active[id].cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel(cause)
	}
	c.mu.Lock()
	chat, exists := c.activeChats[id]
	c.mu.Unlock()
	// A new chat may have been admitted after a pause committed. Only cancel
	// the captured lease if the pause actually marked that lease inactive.
	if exists {
		if leases, ok := c.store.(TaskMainChatLeaseStore); ok {
			check, stop := context.WithTimeout(context.Background(), 3*time.Second)
			valid, err := leases.HeartbeatMainChat(check, chat.user, id, chat.run)
			stop()
			if err == nil && !valid {
				chat.cancel(cause)
			}
		}
	}
}

func (c *TaskController) run(ctx context.Context) {
	defer close(c.done)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	completed := make(chan int64, 128)
	var workers sync.WaitGroup
	defer func() {
		c.mu.Lock()
		c.shuttingDown = true
		for _, handle := range c.active {
			handle.cancel(agent.AbortShutdown)
		}
		for _, chat := range c.activeChats {
			chat.cancel(agent.AbortShutdown)
		}
		c.mu.Unlock()
		workers.Wait()
		c.chatWG.Wait()
	}()
	for ctx.Err() == nil {
		if _, err := c.store.RecoverExpiredTasks(ctx); err != nil {
			pkg.Warn("AI task recovery pass failed")
		}
		if chats, ok := c.store.(TaskMainChatLeaseStore); ok {
			if err := chats.ProcessMainChatWakes(ctx); err != nil {
				pkg.Warn("AI MainAgent task wake recovery failed")
			}
		}
		if deletes, ok := c.store.(interface{ ProcessIntentDeletions(context.Context) error }); ok {
			if err := deletes.ProcessIntentDeletions(ctx); err != nil {
				pkg.Warn("AI intent deletion recovery failed")
			}
		}
		for ctx.Err() == nil {
			task, err := c.store.ClaimTask(ctx, c.instance)
			if err != nil {
				pkg.Warn("AI task admission failed")
				break
			}
			if task == nil {
				break
			}
			runCtx, cancel := context.WithCancelCause(ctx)
			c.mu.Lock()
			c.active[task.ID] = taskRunHandle{run: task.ActiveRun, cancel: cancel}
			c.mu.Unlock()
			workers.Add(1)
			go func(task *Task) {
				defer workers.Done()
				defer cancel(nil)
				c.execute(runCtx, cancel, task)
				c.mu.Lock()
				if current, ok := c.active[task.ID]; ok && current.run == task.ActiveRun {
					delete(c.active, task.ID)
				}
				c.mu.Unlock()
				select {
				case completed <- task.ID:
				case <-ctx.Done():
				}
			}(task)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-c.wake:
		case <-completed:
		}
	}
}

func (c *TaskController) execute(ctx context.Context, cancel context.CancelCauseFunc, task *Task) {
	heartbeatDone := make(chan struct{})
	leaseTask := task
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				valid, err := c.store.HeartbeatTask(ctx, leaseTask)
				if err != nil || !valid {
					cancel(ErrRunLeaseLost)
					return
				}
			}
		}
	}()
	status, reason := "failed", "AI 任务启动失败"
	defer func() {
		cancel(nil)
		<-heartbeatDone
		finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer finishCancel()
		if err := c.store.FinishTask(finishCtx, task, status, reason); err != nil {
			pkg.Warn("AI task completion persistence failed", zap.Int64("ai.task_id", task.ID))
		}
	}()
	var provider llm.Provider
	var model string
	var configured bool
	var err error
	if c.models != nil {
		provider, model, configured, err = c.models(ctx, task.OwnerUserID, task.LLMProfileIDs, task.ActiveLLMProfileID)
	} else if c.provider != nil {
		provider, model, configured, err = c.provider(ctx, task.OwnerUserID)
	}
	if err != nil || !configured || provider == nil {
		status, reason = "paused", "请先配置可用的 AI 模型，再恢复任务。"
		return
	}
	var slots []string
	if c.workerSlots != nil {
		slots, err = c.workerSlots(ctx, task.OwnerUserID, task.WorkerCount)
		if err != nil {
			status, reason = "paused", "Worker 配置不可用，请检查全局模型池。"
			return
		}
	}
	bootstrap := !task.BootstrapComplete
	started, err := c.store.StampTaskStart(ctx, task)
	if err != nil {
		return
	}
	task = started
	target := 0
	if task.TargetID != nil {
		target = *task.TargetID
	}
	var deadline time.Time
	if task.DeadlineAt != nil {
		deadline = *task.DeadlineAt
	}
	outcome, err := c.runtime.RunTask(ctx, airuntime.TaskExecution{ID: task.ID, ExplorationID: task.ExplorationID, TenantID: task.TenantID, UserID: task.OwnerUserID,
		RunID: task.ActiveRun, Goal: task.Goal, Description: task.Description, TargetID: target, WorkerCount: task.WorkerCount, WorkerProfiles: slots, MaxTurns: task.MaxTurns, Deadline: deadline, PlanHeartbeatSeconds: task.PlanHeartbeatSeconds, Bootstrap: bootstrap, CoverageEnabled: &task.CoverageEnabled}, provider, model)
	if err != nil && ctx.Err() != nil {
		status, reason = "paused", "任务执行已中断，可恢复后继续。"
		return
	}
	if outcome.Status != "" {
		status, reason = outcome.Status, outcome.Reason
	}
	if err != nil {
		pkg.Warn("AI task runtime failed", zap.Int64("ai.task_id", task.ID))
		if reason == "" {
			reason = "AI 运行时调用失败"
		}
	}
}

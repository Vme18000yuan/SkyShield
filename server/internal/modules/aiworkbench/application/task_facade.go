package application

import (
	"context"
	"github.com/Autumn-27/norma/llm"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapproval "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/application"
	"strings"
	"unicode/utf8"
)

type TaskFacade struct {
	store       TaskStore
	provider    ProviderResolver
	controller  *TaskController
	models      TaskModelResolver
	mentions    *ChatMentionFacade
	attachments *AttachmentFacade
	evidence    *FindingEvidenceFacade
}

type TaskModelResolver func(context.Context, int, []string, *string) (llm.Provider, string, bool, error)

func (f *TaskFacade) SetTaskModelResolver(resolve TaskModelResolver) { f.models = resolve }

func NewTaskFacade(store TaskStore, provider ProviderResolver, controller *TaskController) *TaskFacade {
	return &TaskFacade{store: store, provider: provider, controller: controller}
}

func (f *TaskFacade) Create(ctx context.Context, input CreateTaskInput) (*Task, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Goal = strings.TrimSpace(input.Goal)
	input.Remark = strings.TrimSpace(input.Remark)
	if input.Name == "" {
		input.Name = "新 AI 任务"
	}
	if input.UserID <= 0 || input.Goal == "" || utf8.RuneCountInString(input.Name) > 200 || len(input.Goal) > 32*1024 || len(input.Description) > 64*1024 || utf8.RuneCountInString(input.Remark) > 2000 || !utf8.ValidString(input.Remark) || strings.ContainsRune(input.Remark, '\x00') || input.CategoryID != nil && *input.CategoryID <= 0 || input.TargetID < 0 || len(input.AssetIDs) > 1000 || len(input.InterceptRules) > 100 || len(input.SourceTaskIDs) > 8 {
		return nil, ErrInvalidInput
	}
	for i, rule := range input.InterceptRules {
		normalized, err := aiapproval.NormalizeAssetRule(1, rule)
		if err != nil {
			return nil, ErrInvalidInput
		}
		normalized.ID, normalized.Builtin = 0, false
		input.InterceptRules[i] = normalized
	}
	if input.ConversationRunID != "" || input.ToolUseID != "" || input.ToolInputHash != "" {
		if !validAutoTaskMutation(AutoTaskMutation{RunID: input.ConversationRunID, ToolUseID: input.ToolUseID, InputHash: input.ToolInputHash}) {
			return nil, ErrInvalidInput
		}
	}
	ids, active, err := normalizeTaskLLMChain(input.LLMProfileIDs, nil)
	if err != nil {
		return nil, err
	}
	input.LLMProfileIDs = ids
	if input.PlanHeartbeatSeconds < 600 {
		input.PlanHeartbeatSeconds = 600
	}
	if input.WorkerCount < 0 || input.WorkerCount > 50 || input.MaxTurns < 0 || input.MaxTurns > 1000 || input.TimeoutSeconds < 0 || int64(input.TimeoutSeconds) > 2147483647 || int64(input.PlanHeartbeatSeconds) > 2147483647 {
		return nil, ErrInvalidInput
	}
	seen := map[int64]bool{}
	for _, id := range input.AssetIDs {
		if id <= 0 || seen[id] {
			return nil, ErrInvalidInput
		}
		seen[id] = true
	}
	seen = map[int64]bool{}
	for _, id := range input.SourceTaskIDs {
		if id <= 0 || seen[id] {
			return nil, ErrInvalidInput
		}
		seen[id] = true
	}
	if input.StartImmediately {
		if err := f.requireTaskModel(ctx, input.UserID, ids, active); err != nil {
			return nil, err
		}
	}
	task, err := f.store.CreateTask(ctx, input)
	if err == nil && f.controller != nil {
		f.controller.Notify()
	}
	return task, err
}

func (f *TaskFacade) requireTaskModel(ctx context.Context, user int, ids []string, active *string) error {
	if f.models == nil {
		return f.requireModel(ctx, user)
	}
	provider, _, configured, err := f.models(ctx, user, ids, active)
	if err != nil {
		return err
	}
	if !configured || provider == nil {
		return ErrModelNotConfigured
	}
	return nil
}

func (f *TaskFacade) requireModel(ctx context.Context, user int) error {
	if f.provider == nil {
		return ErrModelNotConfigured
	}
	provider, _, ok, err := f.provider(ctx, user)
	if err != nil {
		return err
	}
	if !ok || provider == nil {
		return ErrModelNotConfigured
	}
	return nil
}

func (f *TaskFacade) List(ctx context.Context, user int, before int64) ([]Task, error) {
	if user <= 0 || before < 0 {
		return nil, ErrInvalidInput
	}
	return f.store.ListTasks(ctx, user, before)
}
func (f *TaskFacade) Get(ctx context.Context, user int, id int64) (*Task, error) {
	if user <= 0 || id <= 0 {
		return nil, ErrInvalidInput
	}
	return f.store.GetTask(ctx, user, id)
}
func (f *TaskFacade) Graph(ctx context.Context, user int, id int64) (*TaskGraph, error) {
	if user <= 0 || id <= 0 {
		return nil, ErrInvalidInput
	}
	return f.store.ReadTaskGraph(ctx, user, id)
}

func (f *TaskFacade) NodeDetail(ctx context.Context, user int, taskID, nodeID int64) (*TaskGraphNode, error) {
	if user <= 0 || taskID <= 0 || nodeID <= 0 {
		return nil, ErrInvalidInput
	}
	store, ok := f.store.(TaskNodeStore)
	if !ok {
		return nil, ErrTaskNotFound
	}
	return store.ReadTaskNode(ctx, user, taskID, nodeID)
}

func (f *TaskFacade) Trace(ctx context.Context, user int, taskID, intentID int64, stepIDs []int64) ([]TaskActivity, error) {
	if user <= 0 || taskID <= 0 || intentID <= 0 || len(stepIDs) > 5 {
		return nil, ErrInvalidInput
	}
	node, err := f.NodeDetail(ctx, user, taskID, intentID)
	if err != nil {
		return nil, err
	}
	if node == nil || node.Kind != "intent" {
		return nil, ErrTaskNotFound
	}
	store, ok := f.store.(TaskTraceStore)
	if !ok {
		return nil, ErrTaskNotFound
	}
	return store.ReadTaskTrace(ctx, user, taskID, intentID, stepIDs)
}
func (f *TaskFacade) Activities(ctx context.Context, user int, id, after int64) ([]TaskActivity, error) {
	if user <= 0 || id <= 0 || after < 0 {
		return nil, ErrInvalidInput
	}
	return f.store.ReadTaskActivities(ctx, user, id, after)
}

func (f *TaskFacade) ToolExecutions(ctx context.Context, user int, query string, before int64) ([]TaskToolExecution, error) {
	query = strings.TrimSpace(query)
	if user <= 0 || before < 0 || len(query) > 200 {
		return nil, ErrInvalidInput
	}
	return f.store.ListTaskToolExecutions(ctx, user, query, before, 51, false)
}

func (f *TaskFacade) ToolExecution(ctx context.Context, user int, id int64) (*TaskToolExecution, error) {
	if user <= 0 || id <= 0 {
		return nil, ErrInvalidInput
	}
	items, err := f.store.ListTaskToolExecutions(ctx, user, "", id, 1, true)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, ErrTaskNotFound
	}
	return &items[0], nil
}

func (f *TaskFacade) Assets(ctx context.Context, user int, id, after int64) ([]TaskAsset, error) {
	if user <= 0 || id <= 0 || after < 0 {
		return nil, ErrInvalidInput
	}
	return f.store.ListTaskAssets(ctx, user, id, after)
}

func (f *TaskFacade) prepareChat(ctx context.Context, user int, id int64, message string, allowEmpty bool) (context.Context, string, error) {
	message = strings.TrimSpace(message)
	if user <= 0 || id <= 0 || (!allowEmpty && message == "") || len(message) > 64*1024 {
		return ctx, "", ErrInvalidInput
	}
	if f.controller == nil {
		return ctx, "", ErrTaskChatUnavailable
	}
	if f.mentions != nil && message != "" {
		expanded, err := f.mentions.Compose(ctx, user, message)
		if err != nil {
			return ctx, "", err
		}
		ctx = airuntime.WithTaskChatDisplay(ctx, message)
		message = expanded
	}
	return ctx, message, nil
}
func (f *TaskFacade) Chat(ctx context.Context, user int, id int64, message string) (string, error) {
	ctx, message, err := f.prepareChat(ctx, user, id, message, false)
	if err != nil {
		return "", err
	}
	return f.controller.Chat(ctx, user, id, message)
}

func (f *TaskFacade) Control(ctx context.Context, user int, id int64, action string) (*Task, error) {
	if user <= 0 || id <= 0 {
		return nil, ErrInvalidInput
	}
	if action != "start" && action != "resume" && action != "pause" && action != "stop" {
		return nil, ErrInvalidInput
	}
	if action == "start" || action == "resume" {
		var ids []string
		var active *string
		if f.models != nil {
			item, err := f.store.GetTask(ctx, user, id)
			if err != nil {
				return nil, err
			}
			ids, active = item.LLMProfileIDs, item.ActiveLLMProfileID
		}
		if err := f.requireTaskModel(ctx, user, ids, active); err != nil {
			return nil, err
		}
	}
	task, err := f.store.ControlTask(ctx, user, id, action, false)
	if err == nil && f.controller != nil {
		f.controller.Notify()
		if action == "pause" || action == "stop" {
			f.controller.Cancel(id)
		}
	}
	return task, err
}

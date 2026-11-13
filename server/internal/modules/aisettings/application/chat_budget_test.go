package application

import (
	"context"
	"testing"
)

type chatBudgetSettings struct{ options map[string]any }

func (s *chatBudgetSettings) ReadRuntimeOptions(context.Context, int, bool) (map[string]any, map[string][]byte, error) {
	return s.options, nil, nil
}
func (s *chatBudgetSettings) PatchRuntimeOptions(_ context.Context, _ int, patch map[string]any, _ map[string][]byte) error {
	for k, v := range patch {
		s.options[k] = v
	}
	return nil
}
func TestChatBudgetDefaultsAndZeroDisableLimits(t *testing.T) {
	store := &chatBudgetSettings{options: map[string]any{}}
	facade := NewRuntimeSettingFacade(store, nil, nil)
	ctx := context.Background()
	limits, err := facade.ChatBudget(ctx, 7)
	if err != nil || limits.Tokens != 0 || limits.Requests != 0 {
		t.Fatalf("limited by default: %#v %v", limits, err)
	}
	if _, err = facade.Patch(ctx, 7, map[string]any{"chat_run_max_tokens": float64(2_000_000), "chat_run_max_requests": float64(1200)}); err != nil {
		t.Fatal(err)
	}
	limits, err = facade.ChatBudget(ctx, 7)
	if err != nil || limits.Tokens != 2_000_000 || limits.Requests != 1200 {
		t.Fatalf("configured budget ignored: %#v %v", limits, err)
	}
	if _, err = facade.Patch(ctx, 7, map[string]any{"chat_run_max_tokens": float64(0), "chat_run_max_requests": float64(0)}); err != nil {
		t.Fatal(err)
	}
	limits, err = facade.ChatBudget(ctx, 7)
	if err != nil || limits.Tokens != 0 || limits.Requests != 0 {
		t.Fatal("0 did not disable budgets")
	}
	for _, value := range []float64{-1, 0.5, 100_000_001} {
		if _, err = facade.Patch(ctx, 7, map[string]any{"chat_run_max_tokens": value}); err == nil {
			t.Fatalf("accepted invalid budget %v", value)
		}
	}
}

func TestTaskBudgetDefaultsToUnlimitedIndependentlyOfChat(t *testing.T) {
	store := &chatBudgetSettings{options: map[string]any{"chat_run_max_tokens": float64(2000000), "chat_run_max_requests": float64(512)}}
	facade := NewRuntimeSettingFacade(store, nil, nil)
	ctx := context.Background()
	limits, err := facade.TaskBudget(ctx, 7)
	if err != nil || limits.Requests != 0 || limits.Tokens != 0 {
		t.Fatalf("hidden task defaults: %#v %v", limits, err)
	}
	if _, err = facade.Patch(ctx, 7, map[string]any{"task_run_max_tokens": float64(5000000), "task_run_max_requests": float64(1024)}); err != nil {
		t.Fatal(err)
	}
	limits, err = facade.TaskBudget(ctx, 7)
	if err != nil || limits.Tokens != 5000000 || limits.Requests != 1024 {
		t.Fatalf("task configuration ignored: %#v %v", limits, err)
	}
	chat, err := facade.ChatBudget(ctx, 7)
	if err != nil || chat.Tokens != 2000000 || chat.Requests != 512 {
		t.Fatal("task setting changed chat settings")
	}
	if _, err = facade.Patch(ctx, 7, map[string]any{"task_run_max_tokens": float64(0), "task_run_max_requests": float64(0)}); err != nil {
		t.Fatal(err)
	}
	limits, err = facade.TaskBudget(ctx, 7)
	if err != nil || limits.Tokens != 0 || limits.Requests != 0 {
		t.Fatal("task zero did not mean unlimited")
	}
}

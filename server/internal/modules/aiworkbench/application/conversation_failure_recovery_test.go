package application

import (
	"context"
	"errors"
	"testing"

	"github.com/Autumn-27/artex/agent"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
)

type recoveryConversationStore struct {
	ConversationStore
	activities []airuntime.Activity
	status     string
}

func (s *recoveryConversationStore) AppendActivity(_ context.Context, _ int, _, _, _ string, activity airuntime.Activity) error {
	s.activities = append(s.activities, activity)
	return nil
}
func (s *recoveryConversationStore) Heartbeat(context.Context, int, string, string, string) (bool, error) {
	return true, nil
}
func (s *recoveryConversationStore) FinishRun(_ context.Context, _ int, _, _, _, status string) error {
	s.status = status
	return nil
}

type failingConversationRunner struct{}

func (failingConversationRunner) Run(context.Context, airuntime.ChatRun, func(airuntime.Activity)) (string, error) {
	return "", errors.New("provider EOF")
}

func TestModelFailureRecoveryIsVisibleButDoesNotMaskFailure(t *testing.T) {
	for _, tc := range []struct {
		name, agent            string
		canceled, wantRecovery bool
	}{
		{"information gather", "info_gatherer", false, true},
		{"other agent", "mainagent", false, false},
		{"user canceled", "info_gatherer", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &recoveryConversationStore{}
			facade := NewConversationFacade(store, failingConversationRunner{}, nil, nil)
			called := false
			facade.SetFailureRecovery(func(context.Context, int, string) (string, error) {
				called = true
				return "系统已保存部分结果", nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			if tc.canceled {
				cancel()
			}
			facade.workers.Add(1)
			facade.execute(ctx, cancel, 7, "conversation", "run", tc.agent, "", "message", "", "fixture", nil, airuntime.AgentConfig{}, nil, agent.WrapupPolicy{}, false, false)
			if called != tc.wantRecovery || store.status != "failed" {
				t.Fatalf("recovery=%v status=%s", called, store.status)
			}
			if tc.wantRecovery && (len(store.activities) != 1 || store.activities[0].Kind != "result" || store.activities[0].Detail != "系统已保存部分结果") {
				t.Fatalf("recovery activity missing: %+v", store.activities)
			}
		})
	}
}

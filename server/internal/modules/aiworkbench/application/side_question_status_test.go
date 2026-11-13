package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/sidequestion"
	"github.com/Autumn-27/norma/llm"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
)

func TestSideQuestionUsesRecordedRunStateInsteadOfAssumingRunning(t *testing.T) {
	for _, status := range []string{"failed", "completed", "stopped", "interrupted", "running"} {
		t.Run(status, func(t *testing.T) {
			now := time.Now().UTC()
			base := []string{"fixture system"}
			admission := SideAdmission{Snapshot: sidequestion.Snapshot{CapturedAt: now.Add(-time.Hour), Request: llm.CompletionRequest{System: base}}, ParentStatus: &SideConversationStatus{RunID: "fixture-run", Status: status, StartedAt: now.Add(-time.Minute), CheckedAt: now}}
			attachSideConversationStatus(&admission)
			request, err := sidequestion.BuildRequest(admission.Snapshot, nil, "why did it stop?")
			if err != nil {
				t.Fatal(err)
			}
			system := strings.Join(request.System, "\n")
			if !strings.Contains(system, `"status":"`+status+`"`) || !strings.Contains(system, `"snapshot_from_earlier_run":true`) {
				t.Fatal("recorded run state missing")
			}
			if strings.Contains(request.Messages[len(request.Messages)-1].Text(), "主 Agent 正在执行原任务") {
				t.Fatal("side question still asserts the main agent is running")
			}
			if len(base) != 1 || base[0] != "fixture system" {
				t.Fatal("original snapshot changed")
			}
		})
	}
}

type failedActivityStore struct {
	recoveryConversationStore
	attempts      int
	failureMarker bool
}

func (s *failedActivityStore) AppendActivity(ctx context.Context, _ int, _, _, _ string, event airuntime.Activity) error {
	s.attempts++
	if s.attempts == 1 {
		return errors.New("fixture persistence failure")
	}
	if ctx.Err() == nil && event.Kind == "result" && event.IsError && strings.Contains(event.Detail, "执行记录保存失败") {
		s.failureMarker = true
	}
	return ctx.Err()
}

type activityEmittingRunner struct{}

func (activityEmittingRunner) Run(ctx context.Context, _ airuntime.ChatRun, emit func(airuntime.Activity)) (string, error) {
	emit(airuntime.Activity{Kind: "tool_result", Tool: "probe", Detail: "fixture"})
	return "", ctx.Err()
}

func TestActivityFailureGetsVisibleTerminalOnFreshContext(t *testing.T) {
	store := &failedActivityStore{}
	facade := NewConversationFacade(store, activityEmittingRunner{}, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	facade.workers.Add(1)
	facade.execute(ctx, cancel, 7, "conversation", "run", "assistant", "", "fixture", "", "fixture", nil, airuntime.AgentConfig{}, nil, agent.WrapupPolicy{}, false, false)
	if !store.failureMarker || store.status != "failed" {
		t.Fatalf("storage failure was invisible: marker=%v status=%s", store.failureMarker, store.status)
	}
}

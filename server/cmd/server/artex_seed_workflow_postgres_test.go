package main

import (
	"context"
	"database/sql"
	"errors"
	"iter"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/skill"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type seedWorkflowProvider struct {
	mentionModelFixture
	goalCalls atomic.Int64
	goals     chan struct{}
	worker    chan struct{}
	planner   chan struct{}
}

type interruptedGoalsProvider struct {
	mentionModelFixture
	started chan struct{}
}

func (p *interruptedGoalsProvider) Stream(ctx context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	for _, item := range request.Tools {
		if item.Name == "set_goals" {
			return func(yield func(llm.StreamEvent, error) bool) {
				close(p.started)
				<-ctx.Done()
				yield(llm.StreamEvent{}, ctx.Err())
			}
		}
	}
	return p.mentionModelFixture.Stream(ctx, request)
}

func (p *interruptedGoalsProvider) Complete(ctx context.Context, request llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, request) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func (p *seedWorkflowProvider) Stream(ctx context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	role := ""
	for _, item := range request.Tools {
		switch item.Name {
		case "add_intent":
			role = "planner"
		case "record_fact":
			if role == "" {
				role = "worker"
			}
		case "set_goals":
			if role == "" {
				role = "goals"
			}
		}
	}
	if role == "goals" && p.goalCalls.Add(1) == 1 {
		return func(yield func(llm.StreamEvent, error) bool) {
			close(p.goals)
			for _, event := range []llm.StreamEvent{{Type: llm.SEMessageStart}, {Type: llm.SEToolUseStart, ToolID: "seed-goal", ToolName: "set_goals"}, {Type: llm.SEToolInputJSON, Text: `{"text":"种子任务可核验目标"}`}, {Type: llm.SEMessageDelta, StopReason: "tool_use"}, {Type: llm.SEMessageStop}} {
				if !yield(event, nil) {
					return
				}
			}
		}
	}
	if role == "worker" {
		return func(yield func(llm.StreamEvent, error) bool) {
			close(p.worker)
			<-ctx.Done()
			yield(llm.StreamEvent{}, ctx.Err())
		}
	}
	if role == "planner" {
		select {
		case p.planner <- struct{}{}:
		default:
		}
	}
	return p.mentionModelFixture.Stream(ctx, request)
}
func (p *seedWorkflowProvider) Complete(ctx context.Context, request llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, request) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func verifyARTEXSeedWorkflowPostgres(t *testing.T, ctx context.Context, db *sql.DB, dsn string) {
	t.Helper()
	var owner int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := airepo.NewTaskStore(orm)
	facade := aiapp.NewTaskFacade(store, nil, nil)
	task, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "种子任务", Description: "验证测试对象", Goal: "确认目标", SeedFirstIntent: true, WorkerCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	var seedID int64
	if err = db.QueryRowContext(ctx, `SELECT n.id FROM tianqiong_ai.exploration_nodes n JOIN tianqiong_ai.exploration_edges e ON e.dst_id=n.id AND e.exploration_id=n.exploration_id JOIN tianqiong_ai.exploration_nodes parent ON parent.id=e.src_id WHERE n.exploration_id=$1 AND n.kind='intent' AND n.state='open' AND n.origin='seed' AND n.priority=8 AND e.rel='derived_from' AND parent.kind='fact' AND parent.state='origin'`, task.ExplorationID).Scan(&seedID); err != nil {
		t.Fatalf("seed intent was not atomically linked to task origin: %v", err)
	}
	if _, err = store.ControlTask(ctx, owner, task.ID, "start", false); err != nil {
		t.Fatal(err)
	}
	claim := func() *aiapp.Task {
		t.Helper()
		for attempts := 0; attempts < 100; attempts++ {
			run, err := store.ClaimTask(ctx, uuid.NewString())
			if err != nil || run == nil {
				t.Fatalf("claim seed task: %+v %v", run, err)
			}
			if run.ID != task.ID {
				if err = store.FinishTask(ctx, run, "paused", "fixture cleanup"); err != nil {
					t.Fatal(err)
				}
				continue
			}
			run, err = store.StampTaskStart(ctx, run)
			if err != nil {
				t.Fatal(err)
			}
			return run
		}
		t.Fatal("seed task was not claimed")
		return nil
	}
	run := claim()
	connector, err := airuntime.NewTaskGraphConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	provider := &seedWorkflowProvider{goals: make(chan struct{}), worker: make(chan struct{}), planner: make(chan struct{}, 1)}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	workspace := t.TempDir()
	go func() {
		execution := taskExecution(run)
		execution.Bootstrap = !run.BootstrapComplete
		_, _ = airuntime.NewTaskRunner(connector, workspace, nil).RunTask(runCtx, execution, provider, "fixture")
		close(done)
	}()
	for label, ch := range map[string]<-chan struct{}{"goal decomposition": provider.goals, "seed worker": provider.worker} {
		select {
		case <-ch:
		case <-time.After(8 * time.Second):
			t.Fatalf("%s did not start", label)
		}
	}
	select {
	case <-provider.planner:
		t.Fatal("planner ran before the seed worker")
	default:
	}
	var bootstrapped bool
	if err = db.QueryRowContext(ctx, `SELECT bootstrap_complete FROM ai_task WHERE id=$1`, task.ID).Scan(&bootstrapped); err != nil || !bootstrapped {
		t.Fatalf("goal decomposition marker was not saved: %t %v", bootstrapped, err)
	}
	var rounds int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.activity WHERE exploration_id=$1 AND worker='planner' AND kind='round' AND summary='第 0 轮目标拆解'`, task.ExplorationID).Scan(&rounds); err != nil || rounds != 1 {
		t.Fatalf("goal decomposition activity marker missing: %d %v", rounds, err)
	}
	goals, err := facade.Goals(ctx, owner, task.ID)
	if err != nil || len(goals) != 1 || goals[0].Text != "种子任务可核验目标" {
		t.Fatalf("goal decomposition did not run before seed worker: %+v %v", goals, err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("seed task did not stop")
	}
	if err = store.FinishTask(ctx, run, "paused", ""); err != nil {
		t.Fatal(err)
	}
	if err = facade.DeleteGoal(ctx, owner, task.ID, goals[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ControlTask(ctx, owner, task.ID, "resume", false); err != nil {
		t.Fatal(err)
	}
	resumed := claim()
	if !resumed.BootstrapComplete {
		t.Fatal("resumed task lost its bootstrap marker")
	}
	execution := taskExecution(resumed)
	execution.Bootstrap = !resumed.BootstrapComplete
	resumeCtx, resumeCancel := context.WithTimeout(ctx, 8*time.Second)
	defer resumeCancel()
	resumeProvider := &mentionModelFixture{}
	resumeRunner := airuntime.NewTaskRunner(connector, workspace, nil)
	resumeRunner.SetAgentResolver(func(_ context.Context, _ int, role string) (airuntime.ChatAgentDefinition, error) {
		if role == "goals" {
			return airuntime.ChatAgentDefinition{}, errors.New("goals should not be resolved on resume")
		}
		definition, _ := airuntime.BuiltinChatAgent(role)
		return definition, nil
	})
	resumeRunner.SetRoleProviderResolver(func(_ context.Context, _ int, role string) (llm.Provider, string, bool, error) {
		if role == "goals" {
			return nil, "", false, errors.New("goals model should not be resolved on resume")
		}
		return resumeProvider, "fixture", true, nil
	})
	resumeRunner.SetSkillResolver(func(_ context.Context, _ int, role string) ([]skill.Skill, error) {
		if role == "goals" {
			return nil, errors.New("goals skills should not be resolved on resume")
		}
		return nil, nil
	})
	outcome, err := resumeRunner.RunTask(resumeCtx, execution, resumeProvider, "fixture")
	if err != nil || outcome.Status != "done" {
		t.Fatalf("resume seed task: %+v %v", outcome, err)
	}
	if err = store.FinishTask(ctx, resumed, "done", ""); err != nil {
		t.Fatal(err)
	}
	goals, err = facade.Goals(ctx, owner, task.ID)
	if err != nil || len(goals) != 0 {
		t.Fatalf("deleted goals were re-created after resume: %+v %v", goals, err)
	}
	interrupted, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "中断目标拆解", Goal: "不可提前落库", SeedFirstIntent: true, WorkerCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	task = interrupted
	if _, err = store.ControlTask(ctx, owner, task.ID, "start", false); err != nil {
		t.Fatal(err)
	}
	interruptedRun := claim()
	interruptedProvider := &interruptedGoalsProvider{started: make(chan struct{})}
	interruptCtx, interrupt := context.WithCancel(ctx)
	interruptedDone := make(chan struct{})
	go func() {
		execution := taskExecution(interruptedRun)
		execution.Bootstrap = !interruptedRun.BootstrapComplete
		_, _ = airuntime.NewTaskRunner(connector, workspace, nil).RunTask(interruptCtx, execution, interruptedProvider, "fixture")
		close(interruptedDone)
	}()
	select {
	case <-interruptedProvider.started:
	case <-time.After(8 * time.Second):
		interrupt()
		t.Fatal("interruptible goal decomposition did not start")
	}
	interrupt()
	select {
	case <-interruptedDone:
	case <-time.After(8 * time.Second):
		t.Fatal("cancelled goal decomposition did not stop")
	}
	if err = db.QueryRowContext(ctx, `SELECT bootstrap_complete FROM ai_task WHERE id=$1`, task.ID).Scan(&bootstrapped); err != nil || bootstrapped {
		t.Fatalf("cancelled goal decomposition was marked complete: %t %v", bootstrapped, err)
	}
	goals, err = facade.Goals(ctx, owner, task.ID)
	if err != nil || len(goals) != 0 {
		t.Fatalf("cancelled goal decomposition wrote a fallback goal: %+v %v", goals, err)
	}
	if err = store.FinishTask(ctx, interruptedRun, "paused", ""); err != nil {
		t.Fatal(err)
	}
	manual, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "人工预设目标", Goal: "人工目标", WorkerCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	task = manual
	if _, err = facade.SaveGoal(ctx, owner, task.ID, 0, "已提供的目标", ""); err != nil {
		t.Fatal(err)
	}
	manualRun := claim()
	manualCtx, cancelManual := context.WithCancel(ctx)
	manualDone := make(chan struct{})
	go func() {
		execution := taskExecution(manualRun)
		execution.Bootstrap = !manualRun.BootstrapComplete
		_, _ = resumeRunner.RunTask(manualCtx, execution, resumeProvider, "fixture")
		close(manualDone)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err = db.QueryRowContext(ctx, `SELECT bootstrap_complete FROM ai_task WHERE id=$1`, task.ID).Scan(&bootstrapped); err != nil {
			cancelManual()
			t.Fatal(err)
		}
		if bootstrapped {
			break
		}
		select {
		case <-manualDone:
			cancelManual()
			t.Fatal("manual goal task stopped while resolving the unused Goals Agent")
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !bootstrapped {
		cancelManual()
		t.Fatal("manual goal task did not start")
	}
	cancelManual()
	select {
	case <-manualDone:
	case <-time.After(8 * time.Second):
		t.Fatal("manual goal task did not stop")
	}
	if err = store.FinishTask(ctx, manualRun, "paused", ""); err != nil {
		t.Fatal(err)
	}
}

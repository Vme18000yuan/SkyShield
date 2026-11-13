package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"iter"
	"strings"
	"testing"
	"time"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/sidequestion"
	"github.com/Autumn-27/norma/llm"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type deleteModelFixture struct {
	*pauseModelFixture
	planner chan string
}

func (p *deleteModelFixture) Stream(ctx context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	for _, item := range request.Tools {
		if item.Name == "add_intent" {
			input := strings.Join(request.System, "\n")
			for _, message := range request.Messages {
				input += message.Text()
			}
			if strings.Contains(input, "发现不在授权窗口") {
				select {
				case p.planner <- input:
				default:
				}
			}
		}
	}
	return p.pauseModelFixture.Stream(ctx, request)
}

func (p *deleteModelFixture) Complete(ctx context.Context, request llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, request) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func verifyARTEXIntentDeletePostgres(t *testing.T, ctx context.Context, db *sql.DB, dsn string) {
	t.Helper()
	owner, target, asset := taskFixtureScope(t, ctx, db, "intent-delete.example")
	var other int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := airepo.NewTaskStore(orm)
	facade := aiapp.NewTaskFacade(store, nil, nil)
	task, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "删除意图", TargetID: target, AssetIDs: []int64{asset}, WorkerCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ControlTask(ctx, owner, task.ID, "start", false); err != nil {
		t.Fatal(err)
	}
	var run *aiapp.Task
	for attempts := 0; attempts < 100; attempts++ {
		run, err = store.ClaimTask(ctx, uuid.NewString())
		if err != nil || run == nil {
			t.Fatalf("claim intent delete: %+v %v", run, err)
		}
		if run.ID == task.ID {
			break
		}
		if err = store.FinishTask(ctx, run, "paused", "fixture cleanup"); err != nil {
			t.Fatal(err)
		}
		run = nil
	}
	if run == nil || run.ID != task.ID {
		t.Fatal("delete fixture not claimed")
	}
	run, err = store.StampTaskStart(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	connector, err := airuntime.NewTaskGraphConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := connector.OpenForExecution(ctx, taskExecution(run))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	graph := (&artexdb.DB{DB: conn}).Exploration(task.ExplorationID)
	if _, err = graph.AddGoal(map[string]any{"text": "保持运行"}, "human"); err != nil {
		t.Fatal(err)
	}
	open, err := graph.AddIntent(map[string]any{"summary": "不再需要的方向"}, 1, []int64{asset}, "planner")
	if err != nil {
		t.Fatal(err)
	}
	fact, err := graph.AddNode("fact", map[string]any{"summary": "应保留的证据"}, 0, "confirmed", "worker", []int64{asset})
	if err != nil {
		t.Fatal(err)
	}
	if err = graph.Link(open, "yields", fact); err != nil {
		t.Fatal(err)
	}
	parent := sidequestion.Parent{TaskID: task.ID, ExplorationID: task.ExplorationID, IntentID: open}
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_side_session(tenant_id,owner_user_id,task_id,parent_key) VALUES($1,$2,$3,$4)`, task.TenantID, owner, task.ID, parent.Key()); err != nil {
		t.Fatal(err)
	}
	if err = graph.SetIntentState(open, "paused"); err != nil {
		t.Fatal(err)
	}
	if err = facade.SendWorkerMessage(ctx, owner, task.ID, open, "before-delete", "后续说明"); err != nil {
		t.Fatal(err)
	}
	if _, err = facade.CancelIntent(ctx, other, task.ID, open, "无权限", "soft"); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign deletion: %v", err)
	}
	if _, err = facade.CancelIntent(ctx, owner, task.ID, open, "  ", "soft"); !errors.Is(err, aiapp.ErrInvalidInput) {
		t.Fatalf("empty deletion reason: %v", err)
	}
	state, err := facade.CancelIntent(ctx, owner, task.ID, open, "方向错误", "soft")
	if err != nil || state != "deleted" {
		t.Fatalf("delete open intent: %s %v", state, err)
	}
	var reason string
	if err = db.QueryRowContext(ctx, `SELECT state,delete_reason FROM tianqiong_ai.exploration_nodes WHERE id=$1`, open).Scan(&state, &reason); err != nil || state != "deleted" || reason != "方向错误" {
		t.Fatalf("soft delete: %q %q %v", state, reason, err)
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.exploration_nodes n JOIN tianqiong_ai.exploration_edges e ON e.dst_id=n.id WHERE n.id=$1 AND e.src_id=$2`, fact, open).Scan(&count); err != nil || count != 1 {
		t.Fatalf("soft deletion lost evidence: %d %v", count, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_side_session WHERE parent_key=$1`, parent.Key()).Scan(&count); err != nil || count != 0 {
		t.Fatalf("side session not cleared: %d %v", count, err)
	}
	if _, err = airepo.NewConversationStore(orm).ReadSideHistory(ctx, owner, parent, 0); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("deleted intent side session accessible: %v", err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_task_intent_message WHERE task_id=$1 AND intent_id=$2 AND status='pending'`, task.ID, open).Scan(&count); err != nil || count != 0 {
		t.Fatalf("pending message retained: %d %v", count, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_task_human_event WHERE task_id=$1 AND kind='cancelled' AND payload->>'summary'='不再需要的方向' AND payload->>'detail'='方向错误' AND (payload->>'intent_id')::bigint=$2`, task.ID, open).Scan(&count); err != nil || count != 1 {
		t.Fatalf("planner delete event: %d %v", count, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE tianqiong_ai.exploration_nodes SET state='deleted' WHERE id=$1`, fact); err == nil {
		t.Fatal("unfenced graph deletion accepted")
	}
	hard, err := graph.AddIntent(map[string]any{"summary": "彻底删除方向"}, 1, []int64{asset}, "planner")
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := graph.AddIntent(map[string]any{"summary": "共享分支"}, 1, []int64{asset}, "planner")
	if err != nil {
		t.Fatal(err)
	}
	exclusive, err := graph.AddNode("fact", map[string]any{"summary": "独占事实"}, 0, "confirmed", "worker", []int64{asset})
	if err != nil {
		t.Fatal(err)
	}
	shared, err := graph.AddNode("fact", map[string]any{"summary": "共享事实"}, 0, "confirmed", "worker", []int64{asset})
	if err != nil {
		t.Fatal(err)
	}
	crossFact, err := graph.AddNode("fact", map[string]any{"summary": "跨任务引用事实"}, 0, "confirmed", "worker", []int64{asset})
	if err != nil {
		t.Fatal(err)
	}
	findingNode, err := graph.AddNode("finding", map[string]any{"summary": "独占发现"}, 0, "confirmed", "worker", []int64{asset})
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range [][2]int64{{hard, exclusive}, {hard, shared}, {sibling, shared}, {hard, findingNode}, {hard, crossFact}} {
		if err = graph.Link(edge[0], "yields", edge[1]); err != nil {
			t.Fatal(err)
		}
	}
	child, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "引用来源证据", SourceTaskIDs: []int64{task.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ControlTask(ctx, owner, child.ID, "start", false); err != nil {
		t.Fatal(err)
	}
	var childRun *aiapp.Task
	for attempts := 0; attempts < 100; attempts++ {
		childRun, err = store.ClaimTask(ctx, uuid.NewString())
		if err != nil || childRun == nil {
			t.Fatalf("claim child task: %+v %v", childRun, err)
		}
		if childRun.ID == child.ID {
			break
		}
		if err = store.FinishTask(ctx, childRun, "paused", "fixture cleanup"); err != nil {
			t.Fatal(err)
		}
		childRun = nil
	}
	if childRun == nil || childRun.ID != child.ID {
		t.Fatal("child fixture not claimed")
	}
	childRun, err = store.StampTaskStart(ctx, childRun)
	if err != nil {
		t.Fatal(err)
	}
	childConn, err := connector.OpenForExecution(ctx, taskExecution(childRun))
	if err != nil {
		t.Fatal(err)
	}
	childGraph := (&artexdb.DB{DB: childConn}).Exploration(child.ExplorationID)
	childGoal, err := childGraph.AddGoal(map[string]any{"text": "保留来源证明"}, "human")
	if err != nil {
		t.Fatal(err)
	}
	if err = childGraph.Link(crossFact, "proves", childGoal); err != nil {
		t.Fatal(err)
	}
	if err = store.FinishTask(ctx, childRun, "done", ""); err != nil {
		t.Fatal(err)
	}
	childConn.Close()
	var finding, risk int64
	if err = conn.QueryRowContext(ctx, `INSERT INTO tianqiong_ai.findings(task_id,node_id,vulnclass,name,severity,summary) VALUES($1,$2,'hard-delete-test','独占发现','low','fixture') RETURNING id`, task.ID, findingNode).Scan(&finding); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT vulnerability_id FROM ai_finding_risk_link WHERE finding_id=$1`, finding).Scan(&risk); err != nil {
		t.Fatal(err)
	}
	inputTokens, outputTokens, finalOutput := 10, 2, 3
	if _, err = graph.AppendActivity(artexdb.Activity{NodeID: &hard, Worker: "work#1", Kind: "usage", InputTokens: &inputTokens, OutputTokens: &outputTokens}); err != nil {
		t.Fatal(err)
	}
	if _, err = graph.AppendActivity(artexdb.Activity{NodeID: &hard, Worker: "work#1", Kind: "result", OutputTokens: &finalOutput}); err != nil {
		t.Fatal(err)
	}
	var retest int64
	if err = db.QueryRowContext(ctx, `INSERT INTO ai_finding_retest(tenant_id,owner_user_id,finding_id,status,snapshot) VALUES($1,$2,$3,'pending','{}') RETURNING id`, task.TenantID, owner, finding).Scan(&retest); err != nil {
		t.Fatal(err)
	}
	if _, err = facade.CancelIntent(ctx, owner, task.ID, hard, "彻底清理", "hard"); !errors.Is(err, aiapp.ErrTaskControlConflict) {
		t.Fatalf("active retest did not block deletion: %v", err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.exploration_nodes WHERE id=ANY($1::bigint[])`, []int64{hard, exclusive, findingNode}).Scan(&count); err != nil || count != 3 {
		t.Fatalf("retest conflict caused partial deletion: %d %v", count, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_finding_retest SET status='completed' WHERE id=$1`, retest); err != nil {
		t.Fatal(err)
	}
	state, err = facade.CancelIntent(ctx, owner, task.ID, hard, "彻底清理", "hard")
	if err != nil || state != "deleted" {
		t.Fatalf("hard delete: %s %v", state, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.exploration_nodes WHERE id=ANY($1::bigint[])`, []int64{hard, exclusive, findingNode}).Scan(&count); err != nil || count != 0 {
		t.Fatalf("exclusive nodes remain: %d %v", count, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.exploration_nodes WHERE id=ANY($1::bigint[])`, []int64{sibling, shared}).Scan(&count); err != nil || count != 2 {
		t.Fatalf("shared nodes removed: %d %v", count, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.exploration_edges WHERE exploration_id=$1 AND src_id=$2 AND dst_id=$3 AND rel='proves'`, child.ExplorationID, crossFact, childGoal).Scan(&count); err != nil || count != 1 {
		t.Fatalf("cross-task evidence was removed: %d %v", count, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.findings WHERE id=$1`, finding).Scan(&count); err != nil || count != 0 {
		t.Fatalf("exclusive finding remains: %d %v", count, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM vulnerability WHERE id=$1`, risk).Scan(&count); err != nil || count != 1 {
		t.Fatalf("platform risk removed: %d %v", count, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.activity WHERE exploration_id=$1 AND worker='token-ledger' AND metadata->>'cancelled_intent_id'=$2 AND input_tokens=10 AND output_tokens=3`, task.ExplorationID, fmt.Sprint(hard)).Scan(&count); err != nil || count != 1 {
		t.Fatalf("token rollup not retained: %d %v", count, err)
	}
	parentBusy, err := graph.AddIntent(map[string]any{"summary": "有运行中后代"}, 1, []int64{asset}, "planner")
	if err != nil {
		t.Fatal(err)
	}
	childBusy, err := graph.AddIntent(map[string]any{"summary": "运行中的子意图"}, 1, []int64{asset}, "planner")
	if err != nil {
		t.Fatal(err)
	}
	if err = graph.Link(parentBusy, "yields", childBusy); err != nil {
		t.Fatal(err)
	}
	if err = graph.SetIntentState(childBusy, "running"); err != nil {
		t.Fatal(err)
	}
	if _, err = facade.CancelIntent(ctx, owner, task.ID, parentBusy, "清理子树", "hard"); !errors.Is(err, aiapp.ErrTaskControlConflict) {
		t.Fatalf("removed live descendant: %v", err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.exploration_nodes WHERE id=ANY($1::bigint[])`, []int64{parentBusy, childBusy}).Scan(&count); err != nil || count != 2 {
		t.Fatalf("partial hard delete on conflict: %d %v", count, err)
	}
	if err = graph.SetIntentState(childBusy, "paused"); err != nil {
		t.Fatal(err)
	}
	if state, err = facade.CancelIntent(ctx, owner, task.ID, parentBusy, "清理子树", "hard"); err != nil || state != "deleted" {
		t.Fatalf("hard delete after child stops: %s %v", state, err)
	}

	intent, err := graph.AddIntent(map[string]any{"summary": "运行中的意图"}, 10, []int64{asset}, "planner")
	if err != nil {
		t.Fatal(err)
	}
	provider := &deleteModelFixture{pauseModelFixture: &pauseModelFixture{started: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{}), resumed: make(chan struct{}), busy: make(chan struct{}), busyDone: make(chan struct{}), message: make(chan string, 1)}, planner: make(chan string, 1)}
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = airuntime.NewTaskRunner(connector, t.TempDir(), nil).RunTask(runCtx, taskExecution(run), provider, "fixture")
	}()
	select {
	case <-provider.started:
	case <-time.After(6 * time.Second):
		t.Fatal("worker did not start")
	}
	type result struct {
		state string
		err   error
	}
	results := make(chan result, 1)
	go func() {
		value, err := facade.CancelIntent(ctx, owner, task.ID, intent, "发现不在授权窗口", "soft")
		results <- result{value, err}
	}()
	select {
	case <-provider.cancelled:
	case <-time.After(6 * time.Second):
		t.Fatal("running worker not cancelled")
	}
	if err = store.ProcessIntentDeletions(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case value := <-results:
		t.Fatalf("deletion returned before worker stopped: %+v", value)
	default:
	}
	if err = db.QueryRowContext(ctx, `SELECT state FROM tianqiong_ai.exploration_nodes WHERE id=$1`, intent).Scan(&state); err != nil || state != "running" {
		t.Fatalf("worker mutated too early: %s %v", state, err)
	}
	close(provider.release)
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if err = store.ProcessIntentDeletions(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case value := <-results:
			if value.err != nil || value.state != "deleted" {
				t.Fatalf("running delete result: %+v", value)
			}
			goto deleted
		default:
			time.Sleep(30 * time.Millisecond)
		}
	}
	t.Fatal("running deletion was not completed")
deleted:
	if err = db.QueryRowContext(ctx, `SELECT state,delete_reason FROM tianqiong_ai.exploration_nodes WHERE id=$1`, intent).Scan(&state, &reason); err != nil || state != "deleted" || reason != "发现不在授权窗口" {
		t.Fatalf("settled delete: %s %q %v", state, reason, err)
	}
	select {
	case input := <-provider.planner:
		if !strings.Contains(input, "运行中的意图") {
			t.Fatalf("planner missing deleted intent summary: %s", input)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("planner did not receive deletion reason")
	}
	cancelRun()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("runner failed to stop")
	}

	hardRunning, err := graph.AddIntent(map[string]any{"summary": "彻底删除运行中意图"}, 20, []int64{asset}, "planner")
	if err != nil {
		t.Fatal(err)
	}
	provider2 := &pauseModelFixture{started: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{}), resumed: make(chan struct{}), busy: make(chan struct{}), busyDone: make(chan struct{}), message: make(chan string, 1)}
	runCtx2, cancelRun2 := context.WithCancel(ctx)
	defer cancelRun2()
	done2 := make(chan struct{})
	workspace := t.TempDir()
	go func() {
		defer close(done2)
		_, _ = airuntime.NewTaskRunner(connector, workspace, nil).RunTask(runCtx2, taskExecution(run), provider2, "fixture")
	}()
	select {
	case <-provider2.started:
	case <-time.After(6 * time.Second):
		t.Fatal("hard-delete Worker did not start")
	}
	hardResult := make(chan result, 1)
	go func() {
		value, err := facade.CancelIntent(ctx, owner, task.ID, hardRunning, "彻底移除运行中意图", "hard")
		hardResult <- result{value, err}
	}()
	select {
	case <-provider2.cancelled:
	case <-time.After(6 * time.Second):
		t.Fatal("hard-delete Worker did not cancel")
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.exploration_nodes WHERE id=$1`, hardRunning).Scan(&count); err != nil || count != 1 {
		t.Fatalf("running hard-delete removed early: %d %v", count, err)
	}
	close(provider2.release)
	deadline = time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if err = store.ProcessIntentDeletions(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case value := <-hardResult:
			if value.err != nil || value.state != "deleted" {
				t.Fatalf("running hard delete: %+v", value)
			}
			goto hardDeleted
		default:
			time.Sleep(30 * time.Millisecond)
		}
	}
	t.Fatal("running hard deletion was not completed")
hardDeleted:
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.exploration_nodes WHERE id=$1`, hardRunning).Scan(&count); err != nil || count != 0 {
		t.Fatalf("running hard-delete retained node: %d %v", count, err)
	}
	cancelRun2()
	select {
	case <-done2:
	case <-time.After(6 * time.Second):
		t.Fatal("hard-delete runner failed to stop")
	}
	conflictParent, err := graph.AddIntent(map[string]any{"summary": "异步删除冲突"}, 1, []int64{asset}, "planner")
	if err != nil {
		t.Fatal(err)
	}
	conflictChild, err := graph.AddIntent(map[string]any{"summary": "尚未停止的后代"}, 1, []int64{asset}, "planner")
	if err != nil {
		t.Fatal(err)
	}
	if err = graph.Link(conflictParent, "yields", conflictChild); err != nil {
		t.Fatal(err)
	}
	if err = graph.SetIntentState(conflictParent, "running"); err != nil {
		t.Fatal(err)
	}
	if err = graph.SetIntentState(conflictChild, "running"); err != nil {
		t.Fatal(err)
	}
	conflictRequest, _, err := store.CancelTaskIntent(ctx, owner, task.ID, conflictParent, "停止后删除", "hard")
	if err != nil {
		t.Fatal(err)
	}
	checkDeleteState := func(want string) {
		t.Helper()
		view, err := facade.Graph(ctx, owner, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, node := range view.Nodes {
			if node.ID == conflictParent {
				if node.DeletionState != want {
					t.Fatalf("deletion state: %+v, want %q", node, want)
				}
				return
			}
		}
		t.Fatal("deletion state node missing")
	}
	checkDeleteState("pending")
	if err = graph.SetIntentState(conflictParent, "paused"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(ctx, `UPDATE public.ai_task_intent_control SET status='paused',finished_at=clock_timestamp() WHERE id=$1`, conflictRequest); err != nil {
		t.Fatal(err)
	}
	if err = store.ProcessIntentDeletions(ctx); err != nil {
		t.Fatal(err)
	}
	if status, err := store.IntentPauseStatus(ctx, owner, task.ID, conflictRequest); err != nil || status != "conflict" {
		t.Fatalf("hard-delete conflict not reported: %s %v", status, err)
	}
	checkDeleteState("conflict")
	if err = graph.SetIntentState(conflictChild, "paused"); err != nil {
		t.Fatal(err)
	}
	if state, err = facade.CancelIntent(ctx, owner, task.ID, conflictParent, "重新删除", "hard"); err != nil || state != "deleted" {
		t.Fatalf("retry after conflict: %s %v", state, err)
	}

	interrupted, err := graph.AddIntent(map[string]any{"summary": "中断后删除"}, 1, []int64{asset}, "planner")
	if err != nil {
		t.Fatal(err)
	}
	if err = graph.SetIntentState(interrupted, "running"); err != nil {
		t.Fatal(err)
	}
	request, pending, err := store.CancelTaskIntent(ctx, owner, task.ID, interrupted, "中断恢复", "soft")
	if err != nil || pending != "pending" || request == "" {
		t.Fatalf("queue recovery: %s %s %v", request, pending, err)
	}
	if _, err = store.ControlTask(ctx, owner, task.ID, "pause", false); err != nil {
		t.Fatal(err)
	}
	if err = store.ProcessIntentDeletions(ctx); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT state,delete_reason FROM tianqiong_ai.exploration_nodes WHERE id=$1`, interrupted).Scan(&state, &reason); err != nil || state != "deleted" || reason != "中断恢复" {
		t.Fatalf("recovered deletion: %s %s %v", state, reason, err)
	}
	if status, err := store.IntentPauseStatus(ctx, owner, task.ID, request); err != nil || status != "deleted" {
		t.Fatalf("recovered request %s: %s %v", fmt.Sprint(request), status, err)
	}
}

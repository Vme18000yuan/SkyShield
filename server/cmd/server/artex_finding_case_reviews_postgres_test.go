package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/auth"
	serverdatabase "github.com/yyhuni/lunafox/server/internal/database"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	settingsrepo "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	aihandler "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/handler"
	aiinfra "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/infrastructure"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	airouter "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/router"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type findingCaseReviewModel struct {
	findingID int64
	calls     atomic.Int32
}

func (p *findingCaseReviewModel) Stream(_ context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	index := p.calls.Add(1) - 1
	return func(yield func(llm.StreamEvent, error) bool) {
		if !strings.Contains(strings.Join(request.System, "\n"), "历史模式禁止") {
			yield(llm.StreamEvent{}, errors.New("historical review guidance missing"))
			return
		}
		for _, item := range request.Tools {
			if item.Name == "update_finding_report" || item.Name == "report_finding" || item.Name == "Bash" || strings.HasPrefix(item.Name, "mcp__") {
				yield(llm.StreamEvent{}, fmt.Errorf("unsafe historical review tool: %s", item.Name))
				return
			}
		}
		history, _ := json.Marshal(request)
		if index >= 1 && !strings.Contains(string(history), "proof-2") {
			yield(llm.StreamEvent{}, errors.New("review did not receive selected finding evidence"))
			return
		}
		if index >= 2 && !strings.Contains(string(history), "accepted") {
			yield(llm.StreamEvent{}, errors.New("review conclusion was not accepted"))
			return
		}
		events := []llm.StreamEvent{{Type: llm.SEMessageStart}}
		switch index {
		case 0:
			events = append(events, llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "review-read", ToolName: "get_finding_record"}, llm.StreamEvent{Type: llm.SEToolInputJSON, Text: fmt.Sprintf(`{"finding_id":%d}`, p.findingID)}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"})
		case 1:
			input, _ := json.Marshal(map[string]any{"summary": "已核对", "decisions": []map[string]any{{"finding_id": p.findingID, "verdict": "independent", "reason": "不同处理器", "root_cause_evidence": "proof-2"}}})
			events = append(events, llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "review-complete", ToolName: "complete_finding_case_review"}, llm.StreamEvent{Type: llm.SEToolInputJSON, Text: string(input)}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"})
		default:
			events = append(events, llm.StreamEvent{Type: llm.SETextDelta, Text: "审查已完成"}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"})
		}
		events = append(events, llm.StreamEvent{Type: llm.SEMessageStop})
		for _, event := range events {
			if !yield(event, nil) {
				return
			}
		}
	}
}

func (p *findingCaseReviewModel) Complete(ctx context.Context, request llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, request) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func TestARTEXFindingCaseReviewsIsolatedPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv(emptyPostgresMigrationTestDSNEnv))
	if dsn == "" {
		t.Skip(emptyPostgresMigrationTestDSNEnv + " is not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var database string
	if err = db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&database); err != nil || !strings.HasPrefix(database, "lunafox_migration_test_") {
		t.Fatalf("refusing non-test database %q: %v", database, err)
	}
	if countPublicTables(t, ctx, db, false) != 0 {
		t.Fatal("isolated test database is not empty")
	}
	previousFS, previousPath := serverdatabase.MigrationsFS, serverdatabase.MigrationsPath
	serverdatabase.MigrationsFS, serverdatabase.MigrationsPath = migrationsFS, "migrations"
	defer func() { serverdatabase.MigrationsFS, serverdatabase.MigrationsPath = previousFS, previousPath }()
	if err = serverdatabase.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO auth_user(username,password,is_superuser) VALUES('registry-admin','unused',TRUE),('ai-other','unused',FALSE)`); err != nil {
		t.Fatal(err)
	}
	owner, target, asset := taskFixtureScope(t, ctx, db, "case-review.example")
	var other int
	if err = db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := airepo.NewTaskStore(orm)
	facade := aiapp.NewTaskFacade(store, nil, nil)
	if err = settingsrepo.NewAgentCatalogStore(orm).SeedDefaultAgents(ctx); err != nil {
		t.Fatal(err)
	}
	if err = settingsrepo.NewToolCatalogStore(orm).SeedToolCatalog(ctx, settings.NativeToolSeeds(aiinfra.ReporterFindingCaseTools(facade, owner))); err != nil {
		t.Fatal(err)
	}
	task, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "历史审查", Goal: "验证选中范围", TargetID: target, AssetIDs: []int64{asset}})
	if err != nil {
		t.Fatal(err)
	}
	mainRun := uuid.NewString()
	if _, err = store.ClaimMainChat(ctx, owner, task.ID, mainRun); err != nil {
		t.Fatal(err)
	}
	connector, err := airuntime.NewTaskGraphConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := connector.OpenForExecution(ctx, airuntime.TaskExecution{ID: task.ID, ExplorationID: task.ExplorationID, TenantID: task.TenantID, UserID: owner, RunID: mainRun, TargetID: target, ExecutionMode: "main_chat"})
	if err != nil {
		t.Fatal(err)
	}
	graph := (&artexdb.DB{DB: conn}).Exploration(task.ExplorationID)
	intent, err := graph.AddIntent(map[string]any{"summary": "检查"}, 5, []int64{asset}, "mainagent")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, 9)
	nodes := make([]int64, 0, 9)
	for i := 0; i < 9; i++ {
		finding, e := graph.RecordFinding(ctx, artexdb.RecordFindingInput{TaskID: task.ID, IntentID: intent, VulnClass: "fixture", Name: fmt.Sprintf("审查发现 %d", i), Evidence: fmt.Sprintf("proof-%d", i), Worker: "mainagent", AssetIDs: []int64{asset}})
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, finding.FindingID)
		nodes = append(nodes, finding.NodeID)
	}
	conn.Close()
	if err = store.ReleaseMainChat(ctx, owner, task.ID, mainRun); err != nil {
		t.Fatal(err)
	}
	conversations := airepo.NewConversationStore(orm)
	start := func(review *aiapp.FindingCaseReview) (string, string) {
		t.Helper()
		if review.ConversationID == nil {
			t.Fatal("review conversation missing")
		}
		var jobID int64
		if err := db.QueryRowContext(ctx, `SELECT job_id FROM ai_finding_case_review WHERE id=$1`, review.ID).Scan(&jobID); err != nil {
			t.Fatal(err)
		}
		if err := conversations.BeginRun(ctx, owner, *review.ConversationID, uuid.NewString(), uuid.NewString(), "manual"); !errors.Is(err, aiapp.ErrConversationBusy) {
			t.Fatalf("manual run consumed review conversation: %v", err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE ai_agent_trigger_job SET status='starting' WHERE id=$1`, jobID); err != nil {
			t.Fatal(err)
		}
		run, instance := uuid.NewString(), uuid.NewString()
		if err := conversations.BeginRun(aiapp.WithAgentTriggerJob(ctx, jobID), owner, *review.ConversationID, run, instance, "review"); err != nil {
			t.Fatal(err)
		}
		return run, instance
	}
	finish := func(review *aiapp.FindingCaseReview, run, instance, expected string) {
		t.Helper()
		if err := conversations.FinishRun(ctx, owner, *review.ConversationID, run, instance, "completed"); err != nil {
			t.Fatal(err)
		}
		var state, runState string
		if err := db.QueryRowContext(ctx, `SELECT r.state,run.status FROM ai_finding_case_review r JOIN ai_run run ON run.id=r.run_id WHERE r.id=$1`, review.ID).Scan(&state, &runState); err != nil || state != expected || runState != expected {
			t.Fatalf("review finish state=%q run=%q expected=%q err=%v", state, runState, expected, err)
		}
	}
	decisions := func() aiapp.FindingCaseReviewConclusion {
		return aiapp.FindingCaseReviewConclusion{Summary: "已核对", Decisions: []aiapp.FindingCaseReviewDecision{
			{FindingID: ids[0], Verdict: "independent", Reason: "不同路径", RootCauseEvidence: "证据 0"},
			{FindingID: ids[1], Verdict: "independent", Reason: "不同处理器", RootCauseEvidence: "证据 1"},
		}}
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_tool_catalog SET enabled=false WHERE key='complete_finding_case_review'`); err != nil {
		t.Fatal(err)
	}
	if _, err = facade.CreateCaseReview(ctx, owner, task.ID, ids[:2]); !errors.Is(err, aiapp.ErrTaskControlConflict) {
		t.Fatalf("review started without required tool: %v", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_tool_catalog SET enabled=true WHERE key='complete_finding_case_review'`); err != nil {
		t.Fatal(err)
	}
	deletedReview, err := facade.CreateCaseReview(ctx, owner, task.ID, ids[:2])
	if err != nil || deletedReview.ConversationID == nil {
		t.Fatalf("create review to delete: %+v %v", deletedReview, err)
	}
	if deleted, e := conversations.DeleteOwnedConversations(ctx, owner, []string{*deletedReview.ConversationID}); e != nil || len(deleted) != 1 {
		t.Fatalf("delete queued review conversation: %+v %v", deleted, e)
	}
	var deletedState, deletedJobState string
	var deletedConversation sql.NullString
	if err = db.QueryRowContext(ctx, `SELECT r.state,r.conversation_id::text,j.status FROM ai_finding_case_review r JOIN ai_agent_trigger_job j ON j.id=r.job_id WHERE r.id=$1`, deletedReview.ID).Scan(&deletedState, &deletedConversation, &deletedJobState); err != nil || deletedState != "stopped" || deletedConversation.Valid || deletedJobState != "cancelled" {
		t.Fatalf("deleted review audit: state=%q conversation=%v job=%q err=%v", deletedState, deletedConversation.Valid, deletedJobState, err)
	}
	review, err := facade.CreateCaseReview(ctx, owner, task.ID, ids[:2])
	if err != nil || review.State != "queued" || review.FindingCount != 2 {
		t.Fatalf("review create: %+v %v", review, err)
	}
	listed, err := conversations.ListConversations(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	marked := false
	for _, conversation := range listed {
		if review.ConversationID != nil && conversation.ID == *review.ConversationID {
			marked = conversation.ReviewReadOnly
		}
	}
	if !marked {
		t.Fatal("review conversation was not marked read-only")
	}
	var previousOptions string
	if err = db.QueryRowContext(ctx, `SELECT options::text FROM ai_agent_option WHERE tenant_id=$1::uuid AND agent_key='reporter'`, task.TenantID).Scan(&previousOptions); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_agent_option SET options='{"trigger_run_mode":"serial","trigger_merge_mode":"all","trigger_max_parallel":1}'::jsonb WHERE tenant_id=$1::uuid AND agent_key='reporter'`, task.TenantID); err != nil {
		t.Fatal(err)
	}
	triggerStore := airepo.NewAgentTriggerStore(orm)
	if err = triggerStore.InitializeAgentEvents(ctx); err != nil {
		t.Fatal(err)
	}
	var triggerID int64
	if err = db.QueryRowContext(ctx, `INSERT INTO ai_agent_trigger(tenant_id,agent_key,owner_user_id,enabled,config) VALUES($1::uuid,'reporter',$2,TRUE,'{"on_task_create":true,"task_create_message":"不应混入审查"}'::jsonb) RETURNING id`, task.TenantID, owner).Scan(&triggerID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_agent_event(tenant_id,owner_user_id,task_id,kind,event_key,payload) VALUES($1::uuid,$2,$3,'task_create',$4,'{}'::jsonb)`, task.TenantID, owner, task.ID, "review-isolation-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err = triggerStore.CollectAgentEvents(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	var reviewMessage string
	var separateJobs int
	if err = db.QueryRowContext(ctx, `SELECT j.message,(SELECT count(*) FROM ai_agent_trigger_job x WHERE x.trigger_id=$2 AND x.status='queued') FROM ai_agent_trigger_job j JOIN ai_finding_case_review r ON r.job_id=j.id WHERE r.id=$1`, review.ID, triggerID).Scan(&reviewMessage, &separateJobs); err != nil || strings.Contains(reviewMessage, "不应混入审查") || separateJobs != 1 {
		t.Fatalf("automatic trigger merged into review: jobs=%d message=%q err=%v", separateJobs, reviewMessage, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_agent_trigger_job SET status='cancelled' WHERE trigger_id=$1`, triggerID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM ai_agent_trigger WHERE id=$1`, triggerID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_agent_option SET options=$2::jsonb WHERE tenant_id=$1::uuid AND agent_key='reporter'`, task.TenantID, previousOptions); err != nil {
		t.Fatal(err)
	}
	if _, err = facade.CreateCaseReview(ctx, owner, task.ID, ids[:2]); !errors.Is(err, aiapp.ErrTaskControlConflict) {
		t.Fatalf("parallel review accepted: %v", err)
	}
	if _, err = facade.CreateCaseReview(ctx, other, task.ID, ids[:2]); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign review accepted: %v", err)
	}
	run, instance := start(review)
	if _, err = facade.ReporterFindingRecord(ctx, owner, run, ids[2]); !errors.Is(err, aiapp.ErrRunLeaseLost) {
		t.Fatalf("review read unselected finding: %v", err)
	}
	if err = facade.EnsureFindingReviewRead(ctx, owner, run, ids[2]); !errors.Is(err, aiapp.ErrRunLeaseLost) {
		t.Fatalf("review read unselected traffic evidence: %v", err)
	}
	if _, err = facade.ReporterFindingRecord(ctx, owner, run, ids[0]); err != nil {
		t.Fatal(err)
	}
	candidates, err := facade.ReporterFindingDuplicates(ctx, owner, run, ids[0], 0, 20)
	if err != nil || candidates.Total != 1 || candidates.Items[0].ID != ids[1] {
		t.Fatalf("review candidates crossed selection: %+v %v", candidates, err)
	}
	if _, _, err = facade.ReporterMergeFindingCase(ctx, owner, run, "outside-merge", task.ID, []int64{ids[0], ids[2]}, "禁止", "未选中"); err == nil {
		t.Fatal("review merged an unselected finding")
	}
	if _, _, err = facade.ReporterSuggestFindingCase(ctx, owner, run, "outside-suggestion", []int64{ids[0], ids[2]}, "禁止", "未选中"); err == nil {
		t.Fatal("review suggested an unselected finding")
	}
	version := int64(0)
	if _, err = facade.SaveFindingReport(ctx, owner, run, "forbidden-original-report", nodes[0], "禁止覆盖", &version); err == nil {
		t.Fatal("review modified an original finding report")
	}
	if _, err = facade.CompleteCaseReview(ctx, owner, run, "early", decisions()); !errors.Is(err, aiapp.ErrFindingCaseReviewConclusion) {
		t.Fatalf("review completed without reading all findings: %v", err)
	}
	if _, err = facade.ReporterFindingRecord(ctx, owner, run, ids[1]); err != nil {
		t.Fatal(err)
	}
	if err = facade.SetFindingStatus(ctx, owner, task.ID, ids[0], "confirmed"); err != nil {
		t.Fatal(err)
	}
	if _, err = facade.CompleteCaseReview(ctx, owner, run, "changed-before-conclusion", decisions()); !errors.Is(err, aiapp.ErrFindingCaseReviewConclusion) {
		t.Fatalf("review accepted a finding changed after it was read: %v", err)
	}
	if _, err = facade.ReporterFindingRecord(ctx, owner, run, ids[0]); err != nil {
		t.Fatal(err)
	}
	replayed, err := facade.CompleteCaseReview(ctx, owner, run, "complete", decisions())
	if err != nil || replayed {
		t.Fatalf("review conclusion: replay=%t err=%v", replayed, err)
	}
	replayed, err = facade.CompleteCaseReview(ctx, owner, run, "complete", decisions())
	if err != nil || !replayed {
		t.Fatalf("review conclusion replay: replay=%t err=%v", replayed, err)
	}
	if _, _, err = facade.ReporterMergeFindingCase(ctx, owner, run, "after-conclusion", task.ID, ids[:2], "禁止", "结论已提交"); !errors.Is(err, aiapp.ErrFindingCaseReviewConclusion) {
		t.Fatalf("review wrote after conclusion: %v", err)
	}
	finish(review, run, instance, "completed")
	detail, err := facade.CaseReviews(ctx, owner, task.ID, review.ID, 0, 1)
	if err != nil || len(detail.Items) != 1 || len(detail.Items[0].FindingIDs) != 2 || !strings.Contains(string(detail.Items[0].Conclusion), "不同处理器") {
		t.Fatalf("review detail: %+v %v", detail, err)
	}
	if err = conversations.BeginRun(ctx, owner, *review.ConversationID, uuid.NewString(), uuid.NewString(), "manual"); !errors.Is(err, aiapp.ErrConversationBusy) {
		t.Fatalf("completed review conversation accepted new run: %v", err)
	}
	missing, err := facade.CreateCaseReview(ctx, owner, task.ID, ids[:1])
	if err != nil {
		t.Fatal(err)
	}
	run, instance = start(missing)
	finish(missing, run, instance, "failed")
	stale, err := facade.CreateCaseReview(ctx, owner, task.ID, ids[:2])
	if err != nil {
		t.Fatal(err)
	}
	run, instance = start(stale)
	for _, id := range ids[:2] {
		if _, err = facade.ReporterFindingRecord(ctx, owner, run, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = facade.CompleteCaseReview(ctx, owner, run, "stale-conclusion", decisions()); err != nil {
		t.Fatal(err)
	}
	if _, err = facade.MergeFindingCase(ctx, owner, task.ID, ids[:2], "人工变更", "审查后发生变化"); err != nil {
		t.Fatal(err)
	}
	groupedCandidate, err := facade.FindingReviewCandidates(ctx, owner, "审查发现 0", "", 0, 25)
	if err != nil || groupedCandidate.Total != 1 || !groupedCandidate.Items[0].InGroup || groupedCandidate.Items[0].CaseID == nil {
		t.Fatalf("global grouped candidate: %+v %v", groupedCandidate, err)
	}
	finish(stale, run, instance, "failed")
	changed, err := facade.CreateCaseReview(ctx, owner, task.ID, ids[2:3])
	if err != nil {
		t.Fatal(err)
	}
	run, instance = start(changed)
	if _, err = facade.ReporterFindingRecord(ctx, owner, run, ids[2]); err != nil {
		t.Fatal(err)
	}
	changedConclusion := aiapp.FindingCaseReviewConclusion{Summary: "已核对", Decisions: []aiapp.FindingCaseReviewDecision{{FindingID: ids[2], Verdict: "independent", Reason: "不同路径", RootCauseEvidence: "证据 2"}}}
	if _, err = facade.CompleteCaseReview(ctx, owner, run, "before-change", changedConclusion); err != nil {
		t.Fatal(err)
	}
	if err = facade.SetFindingStatus(ctx, owner, task.ID, ids[2], "fixed"); err != nil {
		t.Fatal(err)
	}
	finish(changed, run, instance, "failed")

	// An existing group or suggestion can contain unselected records.  A
	// historical review must neither expand into that group nor reuse the
	// broader suggestion, even though all records have the same owner.
	backgroundConversation, backgroundRun, backgroundInstance := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = conversations.CreateAgentConversation(ctx, owner, backgroundConversation, "background reporter", "reporter"); err != nil {
		t.Fatal(err)
	}
	if err = conversations.BeginRun(ctx, owner, backgroundConversation, backgroundRun, backgroundInstance, "background"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_agent_trigger_job(tenant_id,agent_key,owner_user_id,source_task_id,event_key,title,message,status,conversation_id,run_id) VALUES($1,'reporter',$2,$3,$4,'fixture','fixture','running',$5::uuid,$6::uuid)`, task.TenantID, owner, task.ID, "background-"+backgroundRun, backgroundConversation, backgroundRun); err != nil {
		t.Fatal(err)
	}
	broad, _, err := facade.ReporterSuggestFindingCase(ctx, owner, backgroundRun, "broad", ids[3:6], "更大范围", "需核验")
	if err != nil {
		t.Fatal(err)
	}
	broadOnly, _, err := facade.ReporterSuggestFindingCase(ctx, owner, backgroundRun, "broad-only", ids[6:9], "另一个更大范围", "需核验")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conversations.StopRun(ctx, owner, backgroundConversation); err != nil {
		t.Fatal(err)
	}
	limited, err := facade.CreateCaseReview(ctx, owner, task.ID, ids[3:5])
	if err != nil {
		t.Fatal(err)
	}
	run, instance = start(limited)
	partialGroup, err := facade.MergeFindingCase(ctx, owner, task.ID, []int64{ids[3], ids[5]}, "已有归并组", "审查开始后人工确认")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = facade.ReporterFindingCase(ctx, owner, run, partialGroup); !errors.Is(err, aiapp.ErrRunLeaseLost) {
		t.Fatalf("review read a group with an unselected member: %v", err)
	}
	if _, _, err = facade.ReporterMergeFindingCase(ctx, owner, run, "expand-group", task.ID, ids[3:5], "禁止", "未选中成员"); err == nil {
		t.Fatal("review expanded an unselected group")
	}
	narrow, _, err := facade.ReporterSuggestFindingCase(ctx, owner, run, "narrow", ids[3:5], "所选建议", "具体共同点")
	if err != nil || narrow == broad {
		t.Fatalf("review reused unselected suggestion: broad=%d narrow=%d err=%v", broad, narrow, err)
	}
	var broadState string
	if err = db.QueryRowContext(ctx, `SELECT state FROM tianqiong_ai.finding_case_suggestions WHERE id=$1`, broad).Scan(&broadState); err != nil || broadState != "pending" {
		t.Fatalf("review changed an unselected suggestion: %q %v", broadState, err)
	}
	if _, err = conversations.StopRun(ctx, owner, *limited.ConversationID); err != nil {
		t.Fatal(err)
	}
	// StopRun requests cancellation; the worker must acknowledge the terminal
	// state before another review can start on the same task.
	if err = conversations.FinishRun(ctx, owner, *limited.ConversationID, run, instance, "stopped"); err != nil {
		t.Fatal(err)
	}
	if _, err = facade.CreateCaseReview(ctx, owner, task.ID, ids[3:5]); !errors.Is(err, aiapp.ErrTaskControlConflict) {
		t.Fatalf("review accepted a partially selected group: %v", err)
	}
	suggestedReview, err := facade.CreateCaseReview(ctx, owner, task.ID, ids[6:8])
	if err != nil {
		t.Fatal(err)
	}
	run, instance = start(suggestedReview)
	for _, id := range ids[6:8] {
		if _, err = facade.ReporterFindingRecord(ctx, owner, run, id); err != nil {
			t.Fatal(err)
		}
	}
	suggestedConclusion := aiapp.FindingCaseReviewConclusion{Summary: "待确认", Decisions: []aiapp.FindingCaseReviewDecision{
		{FindingID: ids[6], Verdict: "suggested", Reason: "共同端点", RootCauseEvidence: "证据 6"},
		{FindingID: ids[7], Verdict: "suggested", Reason: "共同端点", RootCauseEvidence: "证据 7"},
	}}
	if _, err = facade.CompleteCaseReview(ctx, owner, run, "broad-only", suggestedConclusion); !errors.Is(err, aiapp.ErrFindingCaseReviewConclusion) {
		t.Fatalf("unselected suggestion satisfied review: %v", err)
	}
	selectedSuggestion, _, err := facade.ReporterSuggestFindingCase(ctx, owner, run, "selected-suggestion", ids[6:8], "所选建议", "共同端点")
	if err != nil || selectedSuggestion == broadOnly {
		t.Fatalf("selected suggestion: broad=%d selected=%d err=%v", broadOnly, selectedSuggestion, err)
	}
	if _, err = facade.CompleteCaseReview(ctx, owner, run, "selected-conclusion", suggestedConclusion); err != nil {
		t.Fatal(err)
	}
	finish(suggestedReview, run, instance, "completed")
	queued, err := facade.CreateCaseReview(ctx, owner, task.ID, ids[2:3])
	if err != nil {
		t.Fatal(err)
	}
	if err = facade.CancelQueuedCaseReview(ctx, owner, task.ID, queued.ID); err != nil {
		t.Fatal(err)
	}
	var queuedState string
	if err = db.QueryRowContext(ctx, `SELECT state FROM ai_finding_case_review WHERE id=$1`, queued.ID).Scan(&queuedState); err != nil || queuedState != "stopped" {
		t.Fatalf("cancel queued review: %q %v", queuedState, err)
	}
	stopped, err := facade.CreateCaseReview(ctx, owner, task.ID, ids[2:3])
	if err != nil {
		t.Fatal(err)
	}
	run, instance = start(stopped)
	if _, err = conversations.StopRun(ctx, owner, *stopped.ConversationID); err != nil {
		t.Fatal(err)
	}
	if err = conversations.FinishRun(ctx, owner, *stopped.ConversationID, run, instance, "failed"); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT state FROM ai_finding_case_review WHERE id=$1`, stopped.ID).Scan(&queuedState); err != nil || queuedState != "stopped" {
		t.Fatalf("stop running review: %q %v", queuedState, err)
	}

	// Exercise the legacy Agent loop for an already-started review, including
	// its tool allowlist and persisted conclusion. New reviews are not claimed.
	agentReview, err := facade.CreateCaseReview(ctx, owner, task.ID, ids[2:3])
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	domain := func(user int) []tool.CoreTool {
		return append(aiinfra.ReporterFindingCaseTools(facade, user), aiinfra.FindingReportTools(facade, user, root)...)
	}
	toolStore := settingsrepo.NewToolCatalogStore(orm)
	if err = toolStore.SeedToolCatalog(ctx, settings.NativeToolSeeds(domain(owner))); err != nil {
		t.Fatal(err)
	}
	agents := settings.NewAgentCatalogFacade(settingsrepo.NewAgentCatalogStore(orm))
	catalog := settings.NewToolCatalogFacade(toolStore, agents, domain, nil)
	model := &findingCaseReviewModel{findingID: ids[2]}
	runner, err := airuntime.NewChatRunner(root)
	if err != nil {
		t.Fatal(err)
	}
	chat := aiapp.NewConversationFacade(conversations, runner, func(context.Context, int) (llm.Provider, string, bool, error) {
		return model, "fixture", true, nil
	}, nil)
	chat.SetAgentCatalogResolver(agents.ResolveForChat)
	chat.SetNativeTools(false, catalog.Resolve)
	var mcpCalled atomic.Bool
	chat.SetMCPAugmenter(func(context.Context, int, string) ([]tool.CoreTool, agent.DeferredInfo, func()) {
		mcpCalled.Store(true)
		return nil, agent.DeferredInfo{}, nil
	})
	jobCtx, stopJobs := context.WithCancel(ctx)
	chat.Start(jobCtx)
	defer func() { stopJobs(); <-chat.Done() }()
	var agentJobID int64
	var agentMessage string
	if err = db.QueryRowContext(ctx, `SELECT j.id,j.message FROM ai_agent_trigger_job j JOIN ai_finding_case_review r ON r.job_id=j.id WHERE r.id=$1`, agentReview.ID).Scan(&agentJobID, &agentMessage); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_agent_trigger_job SET status='starting' WHERE id=$1 AND status='queued'`, agentJobID); err != nil {
		t.Fatal(err)
	}
	if _, err = chat.Send(aiapp.WithAgentTriggerJob(ctx, agentJobID), owner, *agentReview.ConversationID, agentMessage); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	completed := false
	for time.Now().Before(deadline) {
		page, e := facade.CaseReviews(ctx, owner, task.ID, agentReview.ID, 0, 1)
		if e != nil || len(page.Items) != 1 {
			t.Fatalf("Agent review status: %+v %v", page, e)
		}
		if page.Items[0].State == "completed" {
			if mcpCalled.Load() || model.calls.Load() < 3 || !strings.Contains(string(page.Items[0].Conclusion), "proof-2") {
				t.Fatalf("Agent review boundary: mcp=%t calls=%d conclusion=%s", mcpCalled.Load(), model.calls.Load(), page.Items[0].Conclusion)
			}
			completed = true
			break
		}
		if page.Items[0].State == "failed" || page.Items[0].State == "stopped" {
			t.Fatalf("Agent review failed: %+v", page.Items[0])
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !completed {
		t.Fatal("Agent review did not complete")
	}
	jwt := auth.NewJWTManager("finding-review-fixture", time.Minute, time.Hour)
	ownerToken, _, err := jwt.GenerateAccessToken(owner, "registry-admin", 0)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, _, err := jwt.GenerateAccessToken(other, "ai-other", 0)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	routes := engine.Group("/v1")
	routes.Use(middleware.AuthMiddleware(jwt, fixtureTokenVersions{}))
	airouter.RegisterAITaskRoutes(routes, aihandler.NewTaskHandler(facade))
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	base := fmt.Sprintf("/v1/aiTasks/%d/findingCaseReviews", task.ID)
	if out := request(http.MethodGet, base, ownerToken, ""); out.Code != http.StatusOK || out.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("review list HTTP: %d %s", out.Code, out.Body.String())
	}
	if out := request(http.MethodGet, fmt.Sprintf("%s/%d", base, agentReview.ID), ownerToken, ""); out.Code != http.StatusOK || !strings.Contains(out.Body.String(), "proof-2") || !strings.Contains(out.Body.String(), "审查发现 2") {
		t.Fatalf("review detail HTTP: %d %s", out.Code, out.Body.String())
	}
	if out := request(http.MethodGet, base, otherToken, ""); out.Code != http.StatusNotFound {
		t.Fatalf("foreign review HTTP: %d", out.Code)
	}
	created := request(http.MethodPost, base, ownerToken, fmt.Sprintf(`{"finding_ids":["%d"]}`, ids[2]))
	if created.Code != http.StatusNotFound {
		t.Fatalf("retired review create HTTP: %d %s", created.Code, created.Body.String())
	}
	legacyReview, err := facade.CreateCaseReview(ctx, owner, task.ID, []int64{ids[2]})
	if err != nil {
		t.Fatal(err)
	}
	cancelPath := fmt.Sprintf("%s/%d/cancel", base, legacyReview.ID)
	if out := request(http.MethodPost, cancelPath, otherToken, ""); out.Code != http.StatusNotFound {
		t.Fatalf("foreign review cancel HTTP: %d", out.Code)
	}
	if out := request(http.MethodPost, cancelPath, ownerToken, ""); out.Code != http.StatusOK {
		t.Fatalf("review cancel HTTP: %d %s", out.Code, out.Body.String())
	}
	queuedReview, err := facade.CreateCaseReview(ctx, owner, task.ID, []int64{ids[2]})
	if err != nil {
		t.Fatal(err)
	}
	if err = airepo.NewAgentTriggerStore(orm).ReconcileAgentJobs(ctx); err != nil {
		t.Fatal(err)
	}
	var retiredState string
	if err = db.QueryRowContext(ctx, `SELECT state FROM ai_finding_case_review WHERE id=$1`, queuedReview.ID).Scan(&retiredState); err != nil || retiredState != "stopped" {
		t.Fatalf("retired queued review state: %q %v", retiredState, err)
	}
	_, secondTarget, secondAsset := taskFixtureScope(t, ctx, db, "case-review-second.example")
	secondTask, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "第二个审查任务", Goal: "跨任务审查", TargetID: secondTarget, AssetIDs: []int64{secondAsset}})
	if err != nil {
		t.Fatal(err)
	}
	secondRun := uuid.NewString()
	if _, err = store.ClaimMainChat(ctx, owner, secondTask.ID, secondRun); err != nil {
		t.Fatal(err)
	}
	secondConn, err := connector.OpenForExecution(ctx, airuntime.TaskExecution{ID: secondTask.ID, ExplorationID: secondTask.ExplorationID, TenantID: secondTask.TenantID, UserID: owner, RunID: secondRun, TargetID: secondTarget, ExecutionMode: "main_chat"})
	if err != nil {
		t.Fatal(err)
	}
	secondGraph := (&artexdb.DB{DB: secondConn}).Exploration(secondTask.ExplorationID)
	secondIntent, err := secondGraph.AddIntent(map[string]any{"summary": "第二任务"}, 5, []int64{secondAsset}, "mainagent")
	if err != nil {
		t.Fatal(err)
	}
	secondFinding, err := secondGraph.RecordFinding(ctx, artexdb.RecordFindingInput{TaskID: secondTask.ID, IntentID: secondIntent, VulnClass: "fixture", Name: "第二任务发现", Evidence: "second-proof", Worker: "mainagent", AssetIDs: []int64{secondAsset}})
	if err != nil {
		t.Fatal(err)
	}
	secondConn.Close()
	if err = store.ReleaseMainChat(ctx, owner, secondTask.ID, secondRun); err != nil {
		t.Fatal(err)
	}
	candidatePage, err := facade.FindingReviewCandidates(ctx, owner, "第二个审查任务", "", 0, 25)
	if err != nil || candidatePage.Total != 1 || candidatePage.Items[0].ID != secondFinding.FindingID || candidatePage.Items[0].TaskName != "第二个审查任务" {
		t.Fatalf("global review candidate: %+v %v", candidatePage, err)
	}
	if page, e := facade.FindingReviewCandidates(ctx, other, "", "", 0, 25); e != nil || page.Total != 0 {
		t.Fatalf("foreign global candidates: %+v %v", page, e)
	}
	var beforeBatch int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_finding_case_review`).Scan(&beforeBatch); err != nil {
		t.Fatal(err)
	}
	badBatch := []aiapp.FindingCaseReviewBatch{{TaskID: task.ID, FindingIDs: []int64{ids[2]}}, {TaskID: secondTask.ID, FindingIDs: []int64{ids[2]}}}
	if _, err = facade.CreateCaseReviewBatch(ctx, owner, badBatch); !errors.Is(err, aiapp.ErrInvalidInput) {
		t.Fatalf("duplicate cross-task finding accepted: %v", err)
	}
	badBatch[1].FindingIDs = []int64{ids[3]}
	if _, err = facade.CreateCaseReviewBatch(ctx, owner, badBatch); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("wrong-task finding batch accepted: %v", err)
	}
	var afterBatch int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_finding_case_review`).Scan(&afterBatch); err != nil || afterBatch != beforeBatch {
		t.Fatalf("invalid batch partially enqueued: before=%d after=%d err=%v", beforeBatch, afterBatch, err)
	}
	busyReview, err := facade.CreateCaseReview(ctx, owner, secondTask.ID, []int64{secondFinding.FindingID})
	if err != nil {
		t.Fatal(err)
	}
	batches := []aiapp.FindingCaseReviewBatch{{TaskID: secondTask.ID, FindingIDs: []int64{secondFinding.FindingID}}, {TaskID: task.ID, FindingIDs: []int64{ids[2]}}}
	if _, err = facade.CreateCaseReviewBatch(ctx, owner, batches); !errors.Is(err, aiapp.ErrTaskControlConflict) {
		t.Fatalf("busy task did not block batch: %v", err)
	}
	if err = facade.CancelQueuedCaseReview(ctx, owner, secondTask.ID, busyReview.ID); err != nil {
		t.Fatal(err)
	}
	type batchResult struct {
		items []aiapp.FindingCaseReview
		err   error
	}
	parallel := make(chan batchResult, 2)
	var batchWG sync.WaitGroup
	for index := 0; index < 2; index++ {
		batchWG.Add(1)
		go func(reverse bool) {
			defer batchWG.Done()
			input := append([]aiapp.FindingCaseReviewBatch(nil), batches...)
			if reverse {
				input[0], input[1] = input[1], input[0]
			}
			items, e := facade.CreateCaseReviewBatch(ctx, owner, input)
			parallel <- batchResult{items: items, err: e}
		}(index == 1)
	}
	batchWG.Wait()
	close(parallel)
	var winning []aiapp.FindingCaseReview
	conflicts := 0
	for result := range parallel {
		if result.err == nil {
			if winning != nil {
				t.Fatal("two overlapping review batches both succeeded")
			}
			winning = result.items
		} else if errors.Is(result.err, aiapp.ErrTaskControlConflict) {
			conflicts++
		} else {
			t.Fatalf("parallel review batch: %v", result.err)
		}
	}
	if len(winning) != 2 || conflicts != 1 {
		t.Fatalf("parallel review batches: winning=%d conflicts=%d", len(winning), conflicts)
	}
	for _, item := range winning {
		if err = facade.CancelQueuedCaseReview(ctx, owner, item.TaskID, item.ID); err != nil {
			t.Fatal(err)
		}
	}
	createdBatch, err := facade.CreateCaseReviewBatch(ctx, owner, batches)
	if err != nil || len(createdBatch) != 2 || createdBatch[0].TaskID != task.ID || createdBatch[1].TaskID != secondTask.ID {
		t.Fatalf("cross-task review batch: %+v %v", createdBatch, err)
	}
	for _, item := range createdBatch {
		if err = facade.CancelQueuedCaseReview(ctx, owner, item.TaskID, item.ID); err != nil {
			t.Fatal(err)
		}
	}
	if out := request(http.MethodGet, "/v1/aiFindingReviewCandidates?q=第二个审查任务", ownerToken, ""); out.Code != http.StatusNotFound {
		t.Fatalf("retired global candidates HTTP: %d %s", out.Code, out.Body.String())
	}
	batchBody := fmt.Sprintf(`{"batches":[{"task_id":"%d","finding_ids":["%d"]},{"task_id":"%d","finding_ids":["%d"]}]}`, task.ID, ids[2], secondTask.ID, secondFinding.FindingID)
	batchResponse := request(http.MethodPost, "/v1/aiFindingCaseReviews", ownerToken, batchBody)
	if batchResponse.Code != http.StatusNotFound {
		t.Fatalf("retired review batch HTTP: %d %s", batchResponse.Code, batchResponse.Body.String())
	}
	var activityID int64
	if err = db.QueryRowContext(ctx, `SELECT id FROM ai_conversation_activity WHERE conversation_id=$1::uuid AND detail LIKE '%proof-2%' ORDER BY id DESC LIMIT 1`, *agentReview.ConversationID).Scan(&activityID); err != nil {
		t.Fatal(err)
	}
	if _, err = conversations.ReadConversationActivityDetail(ctx, owner, *agentReview.ConversationID, activityID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_task_archive(task_id,state) VALUES($1,'archiving')`, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = conversations.ListActivities(ctx, owner, *agentReview.ConversationID, 0); !errors.Is(err, aiapp.ErrConversationNotFound) {
		t.Fatalf("archiving task review activities visible: %v", err)
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM ai_task_archive WHERE task_id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE target SET deleted_at=now() WHERE id=$1`, target); err != nil {
		t.Fatal(err)
	}
	if page, e := facade.FindingReviewCandidates(ctx, owner, "", "", 0, 25); e != nil || page.Total != 1 || page.Items[0].TaskID != secondTask.ID {
		t.Fatalf("deleted target global candidates visible: %+v %v", page, e)
	}
	if _, err = conversations.ListActivities(ctx, owner, *agentReview.ConversationID, 0); !errors.Is(err, aiapp.ErrConversationNotFound) {
		t.Fatalf("deleted target review activities visible: %v", err)
	}
	if _, err = conversations.ReadConversationHistory(ctx, owner, *agentReview.ConversationID, 0, 20); !errors.Is(err, aiapp.ErrConversationNotFound) {
		t.Fatalf("deleted target review history visible: %v", err)
	}
	if _, err = conversations.ReadConversationActivityDetail(ctx, owner, *agentReview.ConversationID, activityID); !errors.Is(err, aiapp.ErrConversationNotFound) {
		t.Fatalf("deleted target review detail visible: %v", err)
	}
	listed, err = conversations.ListConversations(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, conversation := range listed {
		if conversation.ID == *agentReview.ConversationID {
			t.Fatal("deleted target review conversation remained visible")
		}
	}
	executions, err := conversations.ListToolExecutions(ctx, owner, "", 0, 200, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, execution := range executions {
		if execution.ConversationID == *agentReview.ConversationID {
			t.Fatal("deleted target review tool execution remained visible")
		}
	}
}

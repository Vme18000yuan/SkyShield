package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/auth"
	serverdatabase "github.com/yyhuni/lunafox/server/internal/database"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	aihandler "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/handler"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	airouter "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/router"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestARTEXFindingCasesIsolatedPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv(emptyPostgresMigrationTestDSNEnv))
	if dsn == "" {
		t.Skip(emptyPostgresMigrationTestDSNEnv + " is not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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
	owner, target, asset := taskFixtureScope(t, ctx, db, "finding-case.example")
	_, outsideTarget, outsideAsset := taskFixtureScope(t, ctx, db, "finding-case-outside.example")
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
	source, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "归并来源", Goal: "验证归并", AssetIDs: []int64{asset, outsideAsset}})
	if err != nil {
		t.Fatal(err)
	}
	run := uuid.NewString()
	if _, err = store.ClaimMainChat(ctx, owner, source.ID, run); err != nil {
		t.Fatal(err)
	}
	connector, err := airuntime.NewTaskGraphConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := connector.OpenForExecution(ctx, airuntime.TaskExecution{ID: source.ID, ExplorationID: source.ExplorationID, TenantID: source.TenantID, UserID: owner, RunID: run, ExecutionMode: "main_chat"})
	if err != nil {
		t.Fatal(err)
	}
	graph := (&artexdb.DB{DB: connection}).Exploration(source.ExplorationID)
	intent, err := graph.AddIntent(map[string]any{"summary": "检查目标"}, 5, []int64{asset}, "mainagent")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, 14)
	for i := 0; i < 14; i++ {
		findingAsset := asset
		if i == 4 {
			findingAsset = outsideAsset
		}
		result, e := graph.RecordFinding(ctx, artexdb.RecordFindingInput{TaskID: source.ID, IntentID: intent, VulnClass: "fixture", Name: fmt.Sprintf("发现 %d", i), Severity: "high", Summary: "fixture", Evidence: "private evidence", Worker: "mainagent", AssetIDs: []int64{findingAsset}})
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, result.FindingID)
	}
	connection.Close()
	if err = store.ReleaseMainChat(ctx, owner, source.ID, run); err != nil {
		t.Fatal(err)
	}
	child, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "限定目标", Goal: "查看来源", TargetID: target, SourceTaskIDs: []int64{source.ID}})
	if err != nil {
		t.Fatal(err)
	}
	outside, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "其他目标", Goal: "查看来源", TargetID: outsideTarget, SourceTaskIDs: []int64{source.ID}})
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := facade.FindingDuplicates(ctx, owner, source.ID, ids[0], 0, 2)
	if err != nil || candidates.Total != 13 || candidates.StrongTotal != 12 || len(candidates.Items) != 2 || candidates.Items[0].Score != 7 || candidates.Items[0].Tier != "strong" {
		t.Fatalf("duplicate candidates: %+v %v", candidates, err)
	}
	visibleCandidates, err := facade.FindingDuplicates(ctx, owner, child.ID, ids[0], 0, 20)
	if err != nil || visibleCandidates.Total != 12 || visibleCandidates.StrongTotal != 12 {
		t.Fatalf("target-scoped candidates: %+v %v", visibleCandidates, err)
	}
	if _, err = facade.FindingDuplicates(ctx, other, source.ID, ids[0], 0, 20); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign duplicate candidates: %v", err)
	}
	if _, err = facade.FindingDuplicates(ctx, owner, outside.ID, ids[0], 0, 20); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("off-target duplicate anchor: %v", err)
	}
	first, err := facade.MergeFindingCase(ctx, owner, source.ID, ids[:2], "第一组", "同一漏洞证据")
	if err != nil || first <= 0 {
		t.Fatalf("first case: %d %v", first, err)
	}
	if id, e := facade.MergeFindingCase(ctx, owner, source.ID, ids[:2], strings.Repeat("中", 200), "中文理由"); e != nil || id != first {
		t.Fatalf("unicode case title rejected: %d %v", id, e)
	}
	if _, e := facade.MergeFindingCase(ctx, owner, source.ID, ids[:2], strings.Repeat("中", 301), "超过上限"); !errors.Is(e, aiapp.ErrInvalidInput) {
		t.Fatalf("oversized case title accepted: %v", e)
	}
	second, err := facade.MergeFindingCase(ctx, owner, source.ID, ids[2:4], "第二组", "同一组件")
	if err != nil || second == first {
		t.Fatalf("second case: %d %v", second, err)
	}
	firstPage, firstErr := facade.FindingCases(ctx, owner, source.ID, 0, 0, 1)
	secondPage, secondErr := facade.FindingCases(ctx, owner, source.ID, 0, 1, 1)
	if firstErr != nil || secondErr != nil || firstPage.Total != 2 || secondPage.Total != 2 || len(firstPage.Items) != 1 || len(secondPage.Items) != 1 || firstPage.Items[0].ID == secondPage.Items[0].ID {
		t.Fatalf("case pagination: %+v %+v %v %v", firstPage, secondPage, firstErr, secondErr)
	}
	filtered, err := facade.FindingCasesFiltered(ctx, owner, source.ID, aiapp.TaskFindingCaseFilter{Query: "发现 0", Severity: "high", Size: 25})
	if err != nil || filtered.Total != 1 || filtered.Items[0].ID != first {
		t.Fatalf("case member filtering: %+v %v", filtered, err)
	}
	filtered, err = facade.FindingCasesFiltered(ctx, owner, source.ID, aiapp.TaskFindingCaseFilter{Query: "第二组", Severity: "low", Size: 25})
	if err != nil || filtered.Total != 0 {
		t.Fatalf("case severity filtering: %+v %v", filtered, err)
	}
	if _, err = facade.MergeFindingCase(ctx, other, source.ID, ids[:2], "越权", "禁止"); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign merge: %v", err)
	}
	if _, err = facade.MergeFindingCase(ctx, owner, child.ID, ids[:2], "继承", "禁止"); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("inherited merge: %v", err)
	}
	combined, err := facade.MergeFindingCase(ctx, owner, source.ID, []int64{ids[0], ids[2]}, "合并组", "两组为同一问题")
	if err != nil || combined != first {
		t.Fatalf("union cases: %d %v", combined, err)
	}
	detail, err := facade.FindingCases(ctx, owner, source.ID, first, 0, 1)
	if err != nil || len(detail.Items) != 1 || detail.Items[0].MemberCount != 4 || len(detail.Items[0].Members) != 4 || len(detail.Items[0].Events) < 2 || detail.Items[0].Events[0].Action != "merge" {
		t.Fatalf("case members: %+v %v", detail, err)
	}
	filteredFinding, err := facade.Findings(ctx, owner, source.ID, aiapp.TaskFindingFilter{ID: ids[0], Page: 0, Size: 1}, true)
	if err != nil || len(filteredFinding.Items) != 1 || filteredFinding.Items[0].CaseID == nil || *filteredFinding.Items[0].CaseID != first {
		t.Fatalf("filtered finding lost case identity: %+v %v", filteredFinding, err)
	}
	version := detail.Items[0].Version
	report := aiapp.FindingCaseReport{Version: version, Title: "统一报告", Severity: "high", SeverityReason: "可利用", Report: "# 统一报告\n证据"}
	if err = facade.SaveFindingCaseReport(ctx, owner, source.ID, first, report); err != nil {
		t.Fatal(err)
	}
	if err = facade.SaveFindingCaseReport(ctx, owner, source.ID, first, report); err != nil {
		t.Fatalf("idempotent report update: %v", err)
	}
	var reportEvents int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.finding_case_events WHERE case_id=$1 AND action='report_updated'`, first).Scan(&reportEvents); err != nil || reportEvents != 1 {
		t.Fatalf("duplicate report event: %d %v", reportEvents, err)
	}
	if err = facade.SetFindingStatus(ctx, owner, source.ID, ids[0], "confirmed"); err != nil {
		t.Fatal(err)
	}
	detail, err = facade.FindingCases(ctx, owner, child.ID, first, 0, 1)
	if err != nil || len(detail.Items) != 1 || !detail.Items[0].Inherited || len(detail.Items[0].Events) != 0 || detail.Items[0].Version <= detail.Items[0].ReportVersion || detail.Items[0].Report != report.Report {
		t.Fatalf("inherited stale report: %+v %v", detail, err)
	}
	if err = facade.SaveFindingCaseReport(ctx, owner, source.ID, first, report); !errors.Is(err, aiapp.ErrTaskControlConflict) {
		t.Fatalf("stale report update: %v", err)
	}
	if err = facade.DetachFindingCaseMember(ctx, owner, child.ID, first, ids[0], "只读"); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("inherited detach: %v", err)
	}
	if page, e := facade.FindingCases(ctx, owner, outside.ID, first, 0, 1); e != nil || page.Total != 0 {
		t.Fatalf("case crossed target: %+v %v", page, e)
	}
	locked, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.Rollback()
	var lockedID int64
	if err = locked.QueryRowContext(ctx, `SELECT id FROM tianqiong_ai.findings WHERE id=$1 FOR UPDATE`, ids[1]).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}
	checkCtx, checkCancel := context.WithTimeout(ctx, 2*time.Second)
	err = facade.DetachFindingCaseMember(checkCtx, owner, source.ID, first, ids[0], "移出")
	checkCancel()
	if !errors.Is(err, aiapp.ErrTaskControlConflict) {
		t.Fatalf("locked case member: %v", err)
	}
	_ = locked.Rollback()
	if err = facade.DetachFindingCaseMember(ctx, owner, source.ID, first, ids[0], "不是同一问题"); err != nil {
		t.Fatal(err)
	}
	candidates, err = facade.FindingDuplicates(ctx, owner, source.ID, ids[0], 0, 20)
	if err != nil || candidates.Total != 10 {
		t.Fatalf("human veto did not filter candidates: %+v %v", candidates, err)
	}
	if err = facade.DetachFindingCaseMember(ctx, owner, source.ID, first, ids[0], "重试"); err != nil {
		t.Fatalf("idempotent detach: %v", err)
	}
	if _, err = facade.MergeFindingCase(ctx, owner, source.ID, []int64{ids[0], ids[1]}, "重复归并", "应被否决"); !errors.Is(err, aiapp.ErrTaskControlConflict) {
		t.Fatalf("human veto ignored: %v", err)
	}
	if err = facade.DetachFindingCaseMember(ctx, owner, source.ID, first, ids[1], "继续拆分"); err != nil {
		t.Fatal(err)
	}
	if err = facade.DetachFindingCaseMember(ctx, owner, source.ID, first, ids[2], "拆分最后两项"); err != nil {
		t.Fatal(err)
	}
	if page, e := facade.FindingCases(ctx, owner, source.ID, first, 0, 1); e != nil || page.Total != 0 {
		t.Fatalf("case did not dissolve: %+v %v", page, e)
	}
	if err = facade.DetachFindingCaseMember(ctx, owner, source.ID, first, ids[3], "重试自动移出"); err != nil {
		t.Fatalf("idempotent dissolved member: %v", err)
	}
	var remaining int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.findings WHERE task_id=$1`, source.ID).Scan(&remaining); err != nil || remaining != 14 {
		t.Fatalf("grouping removed findings: %d %v", remaining, err)
	}
	// A source group that spans target boundaries must not reveal its title,
	// report, or off-target members to a target-restricted child task.
	cross, err := facade.MergeFindingCase(ctx, owner, source.ID, []int64{ids[3], ids[4]}, "跨目标私有组", "范围验证")
	if err != nil {
		t.Fatal(err)
	}
	if page, e := facade.FindingCases(ctx, owner, child.ID, cross, 0, 1); e != nil || page.Total != 0 {
		t.Fatalf("partially visible case leaked: %+v %v", page, e)
	}
	filteredFinding, err = facade.Findings(ctx, owner, child.ID, aiapp.TaskFindingFilter{ID: ids[3], Page: 0, Size: 1}, true)
	if err != nil || len(filteredFinding.Items) != 1 || filteredFinding.Items[0].CaseID != nil {
		t.Fatalf("partially visible finding leaked case identity: %+v %v", filteredFinding, err)
	}
	visibleCandidates, err = facade.FindingDuplicates(ctx, owner, child.ID, ids[5], 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	seenVisible := false
	for _, item := range visibleCandidates.Items {
		if item.ID == ids[3] {
			seenVisible = true
			if item.CaseID != nil {
				t.Fatal("candidate leaked a partially visible case")
			}
		}
		if item.ID == ids[4] {
			t.Fatal("candidate crossed target boundary")
		}
	}
	if !seenVisible {
		t.Fatal("visible candidate unexpectedly omitted")
	}
	// A Reporter may read only the task attached to its own live trigger job.
	outsideRun := uuid.NewString()
	if _, err = store.ClaimMainChat(ctx, owner, outside.ID, outsideRun); err != nil {
		t.Fatal(err)
	}
	outsideConn, err := connector.OpenForExecution(ctx, airuntime.TaskExecution{ID: outside.ID, ExplorationID: outside.ExplorationID, TenantID: outside.TenantID, UserID: owner, RunID: outsideRun, TargetID: outsideTarget, ExecutionMode: "main_chat"})
	if err != nil {
		t.Fatal(err)
	}
	outsideGraph := (&artexdb.DB{DB: outsideConn}).Exploration(outside.ExplorationID)
	outsideIntent, err := outsideGraph.AddIntent(map[string]any{"summary": "另一个任务"}, 5, []int64{outsideAsset}, "mainagent")
	if err != nil {
		t.Fatal(err)
	}
	outsideFinding, err := outsideGraph.RecordFinding(ctx, artexdb.RecordFindingInput{TaskID: outside.ID, IntentID: outsideIntent, VulnClass: "fixture", Name: "另一任务发现", Evidence: "outside-secret", Worker: "mainagent", AssetIDs: []int64{outsideAsset}})
	if err != nil {
		t.Fatal(err)
	}
	outsideConn.Close()
	if err = store.ReleaseMainChat(ctx, owner, outside.ID, outsideRun); err != nil {
		t.Fatal(err)
	}
	conversations := airepo.NewConversationStore(orm)
	conversationID, reporterRun, instance := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = conversations.CreateAgentConversation(ctx, owner, conversationID, "case reporter", "reporter"); err != nil {
		t.Fatal(err)
	}
	if err = conversations.BeginRun(ctx, owner, conversationID, reporterRun, instance, "write"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_agent_trigger_job(tenant_id,agent_key,owner_user_id,source_task_id,event_key,title,message,status,conversation_id,run_id) VALUES($1,'reporter',$2,$3,$4,'fixture','fixture','running',$5::uuid,$6::uuid)`, source.TenantID, owner, source.ID, "case-reporter-"+reporterRun, conversationID, reporterRun); err != nil {
		t.Fatal(err)
	}
	if record, e := facade.ReporterFindingRecord(ctx, owner, reporterRun, ids[0]); e != nil || record == nil || record.Evidence != "private evidence" {
		t.Fatalf("Reporter source finding: %+v %v", record, e)
	}
	if group, e := facade.ReporterFindingCase(ctx, owner, reporterRun, cross); e != nil || group == nil || len(group.Members) != 2 {
		t.Fatalf("Reporter source case: %+v %v", group, e)
	}
	reporterGroup, err := facade.ReporterFindingCase(ctx, owner, reporterRun, cross)
	if err != nil {
		t.Fatal(err)
	}
	completeReport := "## 概述\n描述\n## 影响\n影响内容\n## 前置条件\n条件\n## 复现步骤\n步骤\n## 证据\n证据内容\n## PoC\n```sh\necho verified\n```\n## 根因分析\n原因\n## 修复建议\n建议"
	reporterReport := aiapp.FindingCaseReport{Version: reporterGroup.Version, Title: "Reporter 统一报告", Severity: "high", SeverityReason: "已验证影响", Report: completeReport}
	invalidReport := reporterReport
	invalidReport.Report = "## 概述\n不完整"
	if _, err = facade.SaveReporterFindingCaseReport(ctx, owner, reporterRun, "report-incomplete", cross, invalidReport); !errors.Is(err, aiapp.ErrIncompleteFindingCaseReport) {
		t.Fatalf("incomplete Reporter report accepted: %v", err)
	}
	replayed, err := facade.SaveReporterFindingCaseReport(ctx, owner, reporterRun, "report-case", cross, reporterReport)
	if err != nil || replayed {
		t.Fatalf("Reporter report: replayed=%t err=%v", replayed, err)
	}
	replayed, err = facade.SaveReporterFindingCaseReport(ctx, owner, reporterRun, "report-case", cross, reporterReport)
	if err != nil || !replayed {
		t.Fatalf("Reporter report replay: replayed=%t err=%v", replayed, err)
	}
	changedReport := reporterReport
	changedReport.Title = "不同内容"
	if _, err = facade.SaveReporterFindingCaseReport(ctx, owner, reporterRun, "report-case", cross, changedReport); !errors.Is(err, aiapp.ErrInvalidInput) {
		t.Fatalf("conflicting Reporter replay accepted: %v", err)
	}
	if err = facade.SetFindingStatus(ctx, owner, source.ID, ids[3], "confirmed"); err != nil {
		t.Fatal(err)
	}
	if _, err = facade.SaveReporterFindingCaseReport(ctx, owner, reporterRun, "report-stale", cross, reporterReport); !errors.Is(err, aiapp.ErrTaskControlConflict) {
		t.Fatalf("stale Reporter report accepted: %v", err)
	}
	var reporterEvents int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.finding_case_events WHERE case_id=$1 AND action='report_updated' AND actor='reporter'`, cross).Scan(&reporterEvents); err != nil || reporterEvents != 1 {
		t.Fatalf("Reporter audit events: %d %v", reporterEvents, err)
	}
	currentGroup, err := facade.ReporterFindingCase(ctx, owner, reporterRun, cross)
	if err != nil {
		t.Fatal(err)
	}
	concurrentReport := reporterReport
	concurrentReport.Version = currentGroup.Version
	var wg sync.WaitGroup
	type reportResult struct {
		replayed bool
		err      error
	}
	results := make(chan reportResult, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			replay, callErr := facade.SaveReporterFindingCaseReport(ctx, owner, reporterRun, "report-concurrent", cross, concurrentReport)
			results <- reportResult{replayed: replay, err: callErr}
		}()
	}
	wg.Wait()
	close(results)
	created := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent Reporter report: %v", result.err)
		}
		if !result.replayed {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("concurrent Reporter writes=%d, want 1", created)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.finding_case_events WHERE case_id=$1 AND action='report_updated' AND actor='reporter'`, cross).Scan(&reporterEvents); err != nil || reporterEvents != 2 {
		t.Fatalf("concurrent Reporter audit events: %d %v", reporterEvents, err)
	}
	suggestion, replayed, err := facade.ReporterSuggestFindingCase(ctx, owner, reporterRun, "suggest-two", ids[6:8], "疑似共同处理器", "同一端点，但缺少根因证据")
	if err != nil || replayed || suggestion <= 0 {
		t.Fatalf("Reporter suggestion: id=%d replay=%t err=%v", suggestion, replayed, err)
	}
	if same, replay, e := facade.ReporterSuggestFindingCase(ctx, owner, reporterRun, "suggest-two", ids[6:8], "疑似共同处理器", "同一端点，但缺少根因证据"); e != nil || !replay || same != suggestion {
		t.Fatalf("suggestion replay: id=%d replay=%t err=%v", same, replay, e)
	}
	if _, _, e := facade.ReporterSuggestFindingCase(ctx, owner, reporterRun, "suggest-two", ids[6:8], "不同请求", "不同理由"); !errors.Is(e, aiapp.ErrInvalidInput) {
		t.Fatalf("conflicting suggestion replay accepted: %v", e)
	}
	if page, e := facade.FindingCaseSuggestions(ctx, owner, source.ID, 0, 25); e != nil || page.Total != 1 || len(page.Items) != 1 || len(page.Items[0].Members) != 2 {
		t.Fatalf("pending suggestions: %+v %v", page, e)
	}
	if page, e := facade.FindingCaseSuggestions(ctx, owner, child.ID, 0, 25); e != nil || page.Total != 0 {
		t.Fatalf("inherited suggestion leaked: %+v %v", page, e)
	}
	if _, e := facade.ResolveCaseSuggestion(ctx, owner, child.ID, suggestion, false, nil); !errors.Is(e, aiapp.ErrTaskNotFound) {
		t.Fatalf("inherited suggestion resolved: %v", e)
	}
	superset, _, err := facade.ReporterSuggestFindingCase(ctx, owner, reporterRun, "suggest-three", ids[6:9], "同端点待确认", "需要补充根因")
	if err != nil || superset == suggestion {
		t.Fatalf("suggestion superset: %d %v", superset, err)
	}
	var suggestionState string
	if err = db.QueryRowContext(ctx, `SELECT state FROM tianqiong_ai.finding_case_suggestions WHERE id=$1`, suggestion).Scan(&suggestionState); err != nil || suggestionState != "superseded" {
		t.Fatalf("subset was not superseded: %q %v", suggestionState, err)
	}
	if reused, _, e := facade.ReporterSuggestFindingCase(ctx, owner, reporterRun, "suggest-subset", ids[6:8], "子集", "同端点"); e != nil || reused != superset {
		t.Fatalf("suggestion superset not reused: %d %v", reused, e)
	}
	overlap, _, err := facade.ReporterSuggestFindingCase(ctx, owner, reporterRun, "suggest-overlap", ids[7:10], "重叠建议", "进一步检查")
	if err != nil || overlap == superset {
		t.Fatalf("overlapping suggestion: %d %v", overlap, err)
	}
	if _, err = facade.ResolveCaseSuggestion(ctx, owner, source.ID, superset, false, nil); err != nil {
		t.Fatal(err)
	}
	var obsolete string
	if err = db.QueryRowContext(ctx, `SELECT state FROM tianqiong_ai.finding_case_suggestions WHERE id=$1`, overlap).Scan(&obsolete); err != nil || obsolete != "obsolete" {
		t.Fatalf("veto did not obsolete overlap: %q %v", obsolete, err)
	}
	if _, _, err = facade.ReporterSuggestFindingCase(ctx, owner, reporterRun, "suggest-vetoed", ids[6:8], "再次建议", "应当拒绝"); !errors.Is(err, aiapp.ErrTaskControlConflict) {
		t.Fatalf("Reporter bypassed veto: %v", err)
	}
	accepted, _, err := facade.ReporterSuggestFindingCase(ctx, owner, reporterRun, "suggest-accepted", ids[9:12], "待确认三项", "共同端点需核对")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = facade.ResolveCaseSuggestion(ctx, owner, source.ID, accepted, true, ids[9:11]); !errors.Is(err, aiapp.ErrInvalidInput) {
		t.Fatalf("accept with fewer than two members: %v", err)
	}
	acceptedCase, err := facade.ResolveCaseSuggestion(ctx, owner, source.ID, accepted, true, []int64{ids[11]})
	if err != nil || acceptedCase <= 0 {
		t.Fatalf("accept with exclusion: %d %v", acceptedCase, err)
	}
	acceptedDetail, err := facade.FindingCases(ctx, owner, source.ID, acceptedCase, 0, 1)
	if err != nil || len(acceptedDetail.Items) != 1 || acceptedDetail.Items[0].MemberCount != 2 {
		t.Fatalf("accepted case members: %+v %v", acceptedDetail, err)
	}
	if _, _, err = facade.ReporterSuggestFindingCase(ctx, owner, reporterRun, "suggest-excluded", []int64{ids[9], ids[11]}, "重新建议", "应当拒绝"); !errors.Is(err, aiapp.ErrTaskControlConflict) {
		t.Fatalf("excluded member veto bypassed: %v", err)
	}
	autoAccepted, _, err := facade.ReporterSuggestFindingCase(ctx, owner, reporterRun, "suggest-manual-merge", []int64{ids[4], ids[5]}, "待人工处理", "确认根因前先保留")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = facade.MergeFindingCase(ctx, owner, source.ID, []int64{ids[4], ids[5]}, "人工确认", "证据确认"); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT state FROM tianqiong_ai.finding_case_suggestions WHERE id=$1`, autoAccepted).Scan(&suggestionState); err != nil || suggestionState != "accepted" {
		t.Fatalf("manual merge did not reconcile suggestion: %q %v", suggestionState, err)
	}
	httpSuggestion, _, err := facade.ReporterSuggestFindingCase(ctx, owner, reporterRun, "suggest-http", []int64{ids[0], ids[5]}, "接口待审核", "共同端点需核验")
	if err != nil {
		t.Fatal(err)
	}
	reporterCase, replayed, err := facade.ReporterMergeFindingCase(ctx, owner, reporterRun, "reporter-merge", source.ID, []int64{ids[0], ids[11]}, "Reporter 归并", "同一根因已确认")
	if err != nil || replayed || reporterCase <= 0 {
		t.Fatalf("Reporter merge: id=%d replay=%t err=%v", reporterCase, replayed, err)
	}
	if same, replay, e := facade.ReporterMergeFindingCase(ctx, owner, reporterRun, "reporter-merge", source.ID, []int64{ids[11], ids[0]}, "Reporter 归并", "同一根因已确认"); e != nil || !replay || same != reporterCase {
		t.Fatalf("Reporter merge replay: id=%d replay=%t err=%v", same, replay, e)
	}
	if _, _, e := facade.ReporterMergeFindingCase(ctx, owner, reporterRun, "reporter-merge", source.ID, []int64{ids[0], ids[11]}, "更改名称", "同一根因已确认"); !errors.Is(e, aiapp.ErrInvalidInput) {
		t.Fatalf("conflicting Reporter merge replay accepted: %v", e)
	}
	if _, _, e := facade.ReporterMergeFindingCase(ctx, owner, reporterRun, "reporter-veto", source.ID, []int64{ids[0], ids[1]}, "禁止归并", "人工否决"); !errors.Is(e, aiapp.ErrTaskControlConflict) {
		t.Fatalf("Reporter merge bypassed veto: %v", e)
	}
	if _, _, e := facade.ReporterMergeFindingCase(ctx, owner, reporterRun, "reporter-other-task", outside.ID, []int64{ids[0], ids[11]}, "跨任务", "禁止"); !errors.Is(e, aiapp.ErrRunLeaseLost) {
		t.Fatalf("Reporter selected another task: %v", e)
	}
	if _, _, e := facade.ReporterMergeFindingCase(ctx, owner, reporterRun, "reporter-cross-finding", source.ID, []int64{ids[0], outsideFinding.FindingID}, "跨任务", "禁止"); !errors.Is(e, aiapp.ErrTaskNotFound) {
		t.Fatalf("Reporter merged a foreign task finding: %v", e)
	}
	var mergeEvents int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.finding_case_events WHERE case_id=$1 AND action='merge' AND actor='reporter'`, reporterCase).Scan(&mergeEvents); err != nil || mergeEvents != 1 {
		t.Fatalf("Reporter merge events: %d %v", mergeEvents, err)
	}
	mergeResults := make(chan error, 2)
	for index, extra := range []int64{ids[12], ids[13]} {
		wg.Add(1)
		go func(index int, extra int64) {
			defer wg.Done()
			id, _, callErr := facade.ReporterMergeFindingCase(ctx, owner, reporterRun, fmt.Sprintf("parallel-merge-%d", index), source.ID, []int64{ids[0], extra}, "并发归并", "已确认相同根因")
			if callErr == nil && id != reporterCase {
				callErr = fmt.Errorf("parallel merge returned case %d, want %d", id, reporterCase)
			}
			mergeResults <- callErr
		}(index, extra)
	}
	wg.Wait()
	close(mergeResults)
	for callErr := range mergeResults {
		if callErr != nil {
			t.Fatalf("parallel Reporter merge: %v", callErr)
		}
	}
	parallelCase, err := facade.FindingCases(ctx, owner, source.ID, reporterCase, 0, 1)
	if err != nil || len(parallelCase.Items) != 1 || parallelCase.Items[0].MemberCount != 4 {
		t.Fatalf("parallel Reporter merge lost members: %+v %v", parallelCase, err)
	}
	if _, err = facade.ReporterFindingRecord(ctx, owner, reporterRun, outsideFinding.FindingID); !errors.Is(err, aiapp.ErrRunLeaseLost) {
		t.Fatalf("Reporter crossed task boundary: %v", err)
	}
	if _, err = facade.ReporterFindingRecord(ctx, other, reporterRun, ids[0]); !errors.Is(err, aiapp.ErrRunLeaseLost) {
		t.Fatalf("Reporter crossed account boundary: %v", err)
	}
	if _, err = conversations.StopRun(ctx, owner, conversationID); err != nil {
		t.Fatal(err)
	}
	if _, err = facade.ReporterFindingCase(ctx, owner, reporterRun, cross); !errors.Is(err, aiapp.ErrRunLeaseLost) {
		t.Fatalf("stopped Reporter retained case access: %v", err)
	}
	if _, err = facade.SaveReporterFindingCaseReport(ctx, owner, reporterRun, "report-after-stop", cross, reporterReport); !errors.Is(err, aiapp.ErrRunLeaseLost) {
		t.Fatalf("stopped Reporter saved case report: %v", err)
	}
	if _, _, err = facade.ReporterMergeFindingCase(ctx, owner, reporterRun, "merge-after-stop", source.ID, []int64{ids[0], ids[11]}, "停止后", "禁止"); !errors.Is(err, aiapp.ErrRunLeaseLost) {
		t.Fatalf("stopped Reporter merged findings: %v", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE tianqiong_ai.finding_cases SET title='unfenced' WHERE id=$1`, cross); err == nil {
		t.Fatal("unfenced case mutation accepted")
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM tianqiong_ai.finding_case_events WHERE case_id=$1`, cross); err == nil {
		t.Fatal("unfenced case event deletion accepted")
	}
	forged, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = forged.ExecContext(ctx, `SELECT set_config('tianqiong.task_id',$1,TRUE),set_config('tianqiong.owner_user_id',$2,TRUE),set_config('tianqiong.execution_mode','human_finding_case',TRUE)`, fmt.Sprint(child.ID), fmt.Sprint(owner)); err != nil {
		t.Fatal(err)
	}
	if _, err = forged.ExecContext(ctx, `INSERT INTO tianqiong_ai.finding_case_members(finding_id,case_id) VALUES($1,$2)`, ids[0], cross); err == nil {
		t.Fatal("forged cross-task case member accepted")
	}
	_ = forged.Rollback()
	jwt := auth.NewJWTManager("finding-cases-fixture", time.Minute, time.Hour)
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
	group := engine.Group("/v1")
	group.Use(middleware.AuthMiddleware(jwt, fixtureTokenVersions{}))
	airouter.RegisterAITaskRoutes(group, aihandler.NewTaskHandler(facade))
	request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/v1/aiTasks/%d/findingCases/%d", child.ID, cross), nil)
	request.Header.Set("Authorization", "Bearer "+ownerToken)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "Reporter 统一报告") {
		t.Fatalf("case HTTP leaked restricted group: %d %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/v1/aiTasks/%d/findings/%d/duplicates", child.ID, ids[5]), nil)
	request.Header.Set("Authorization", "Bearer "+ownerToken)
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), fmt.Sprintf(`"id":"%d"`, ids[4])) {
		t.Fatalf("duplicate HTTP scope: %d %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/v1/aiTasks/%d/findingCases/%d", source.ID, cross), nil)
	request.Header.Set("Authorization", "Bearer "+ownerToken)
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), "Reporter 统一报告") {
		t.Fatalf("case HTTP detail: %d %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/v1/aiTasks/%d/findingCases", source.ID), nil)
	request.Header.Set("Authorization", "Bearer "+otherToken)
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("foreign case HTTP: %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/v1/aiTasks/%d/findingCaseSuggestions", source.ID), nil)
	request.Header.Set("Authorization", "Bearer "+ownerToken)
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), "接口待审核") {
		t.Fatalf("suggestion HTTP list: %d %s", response.Code, response.Body.String())
	}
	resolvePath := fmt.Sprintf("/v1/aiTasks/%d/findingCaseSuggestions/%d/resolve", source.ID, httpSuggestion)
	request = httptest.NewRequest(http.MethodPost, resolvePath, strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer "+ownerToken)
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing suggestion decision accepted: %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, resolvePath, strings.NewReader(`{"accept":false}`))
	request.Header.Set("Authorization", "Bearer "+ownerToken)
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("suggestion HTTP reject: %d %s", response.Code, response.Body.String())
	}
	body := fmt.Sprintf(`{"finding_ids":["%d","%d"],"title":"重复请求","reason":"验证"}`, ids[3], ids[4])
	request = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/v1/aiTasks/%d/findingCases", source.ID), strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+ownerToken)
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), fmt.Sprint(cross)) {
		t.Fatalf("case HTTP merge: %d %s", response.Code, response.Body.String())
	}
}

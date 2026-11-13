package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	settingsrepo "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	aiinfra "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/infrastructure"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	assetrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type findingReporterFixture struct {
	mu                                                                       sync.Mutex
	calls                                                                    int
	findingID, nodeID, relatedFindingID, suggestionFindingID, caseID, taskID int64
}

func (p *findingReporterFixture) Stream(_ context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	p.mu.Lock()
	index := p.calls
	p.calls++
	p.mu.Unlock()
	return func(yield func(llm.StreamEvent, error) bool) {
		history, _ := json.Marshal(request)
		if index == 0 && !strings.Contains(strings.Join(request.System, "\n"), "同一漏洞的多次上报整理") {
			yield(llm.StreamEvent{}, errors.New("Reporter did not receive the ARTEX finding-case workflow"))
			return
		}
		if index == 1 && (!strings.Contains(string(history), "version") || !strings.Contains(string(history), "bindings")) {
			yield(llm.StreamEvent{}, errors.New("reporter did not receive the actual evidence version"))
			return
		}
		if index >= 2 && !strings.Contains(string(history), "report updated") {
			yield(llm.StreamEvent{}, errors.New("report tool did not save its result"))
			return
		}
		if index >= 3 && !strings.Contains(string(history), "strong_total") {
			yield(llm.StreamEvent{}, errors.New("reporter did not receive duplicate candidates"))
			return
		}
		if index >= 4 && !strings.Contains(string(history), "related-proof") {
			yield(llm.StreamEvent{}, errors.New("reporter did not receive the related finding"))
			return
		}
		if index >= 5 && !strings.Contains(string(history), "reporter-group") {
			yield(llm.StreamEvent{}, errors.New("reporter did not receive the finding group"))
			return
		}
		if index >= 6 && !strings.Contains(string(history), "replayed") {
			yield(llm.StreamEvent{}, errors.New("reporter did not save the unified report"))
			return
		}
		if index >= 7 && !strings.Contains(string(history), "suggestion_id") {
			yield(llm.StreamEvent{}, errors.New("reporter did not save a merge suggestion"))
			return
		}
		if index >= 8 {
			merged := false
			for _, message := range request.Messages {
				for _, block := range message.Content {
					if block.Type == llm.BlockToolResult && block.ToolUseID == "fixture-merge" && !block.IsError {
						merged = true
					}
				}
			}
			if !merged {
				yield(llm.StreamEvent{}, errors.New("reporter did not merge the findings"))
				return
			}
		}
		if index >= 10 {
			saved := false
			for _, message := range request.Messages {
				for _, block := range message.Content {
					if block.Type == llm.BlockToolResult && block.ToolUseID == "fixture-case-report-after" && !block.IsError {
						saved = true
					}
				}
			}
			if !saved {
				yield(llm.StreamEvent{}, errors.New("reporter did not refresh the unified report"))
				return
			}
		}
		events := []llm.StreamEvent{{Type: llm.SEMessageStart}}
		switch index {
		case 0:
			events = append(events, llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "fixture-evidence", ToolName: "get_finding_traffic"}, llm.StreamEvent{Type: llm.SEToolInputJSON, Text: fmt.Sprintf(`{"finding_id":"%d"}`, p.findingID)}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"})
		case 1:
			events = append(events, llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "fixture-report", ToolName: "update_finding_report"}, llm.StreamEvent{Type: llm.SEToolInputJSON, Text: fmt.Sprintf(`{"finding_id":%d,"evidence_version":0,"report":"## Agent report\nverified from evidence"}`, p.nodeID)}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"})
		case 2:
			events = append(events, llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "fixture-candidates", ToolName: "search_finding_duplicates"}, llm.StreamEvent{Type: llm.SEToolInputJSON, Text: fmt.Sprintf(`{"finding_id":%d}`, p.findingID)}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"})
		case 3:
			events = append(events, llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "fixture-related", ToolName: "get_finding_record"}, llm.StreamEvent{Type: llm.SEToolInputJSON, Text: fmt.Sprintf(`{"finding_id":%d}`, p.relatedFindingID)}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"})
		case 4:
			events = append(events, llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "fixture-case", ToolName: "get_finding_case"}, llm.StreamEvent{Type: llm.SEToolInputJSON, Text: fmt.Sprintf(`{"case_id":%d}`, p.caseID)}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"})
		case 5, 9:
			var version int64
			caseToolID, reportToolID := "fixture-case", "fixture-case-report"
			if index == 9 {
				caseToolID, reportToolID = "fixture-case-after", "fixture-case-report-after"
			}
			for _, message := range request.Messages {
				for _, block := range message.Content {
					if block.Type != llm.BlockToolResult || block.ToolUseID != caseToolID || block.IsError {
						continue
					}
					for _, content := range block.Content {
						var result struct {
							Case struct {
								Version int64 `json:"version"`
							} `json:"case"`
						}
						if json.Unmarshal([]byte(content.Text), &result) == nil {
							version = result.Case.Version
						}
					}
				}
			}
			if version <= 0 {
				yield(llm.StreamEvent{}, errors.New("Reporter did not read a case version"))
				return
			}
			report := "## 概述\n描述\n## 影响\n影响\n## 前置条件\n条件\n## 复现步骤\n步骤\n## 证据\n证据\n## PoC\n```sh\necho verified\n```\n## 根因分析\n原因\n## 修复建议\n修复"
			input, _ := json.Marshal(map[string]any{"case_id": p.caseID, "version": version, "title": "reporter-group", "severity": "high", "severity_reason": "已验证影响", "report": report})
			events = append(events, llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: reportToolID, ToolName: "update_finding_case_report"}, llm.StreamEvent{Type: llm.SEToolInputJSON, Text: string(input)}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"})
		case 6:
			input, _ := json.Marshal(map[string]any{"task_id": p.taskID, "finding_ids": []int64{p.findingID, p.suggestionFindingID}, "title": "reporter-suggestion", "reason": "同一端点，缺少根因证据"})
			events = append(events, llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "fixture-suggestion", ToolName: "suggest_finding_merge"}, llm.StreamEvent{Type: llm.SEToolInputJSON, Text: string(input)}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"})
		case 7:
			input, _ := json.Marshal(map[string]any{"task_id": p.taskID, "finding_ids": []int64{p.findingID, p.suggestionFindingID}, "title": "reporter-group", "reason": "已验证相同根因"})
			events = append(events, llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "fixture-merge", ToolName: "merge_finding_records"}, llm.StreamEvent{Type: llm.SEToolInputJSON, Text: string(input)}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"})
		case 8:
			events = append(events, llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "fixture-case-after", ToolName: "get_finding_case"}, llm.StreamEvent{Type: llm.SEToolInputJSON, Text: fmt.Sprintf(`{"case_id":%d}`, p.caseID)}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"})
		default:
			events = append(events, llm.StreamEvent{Type: llm.SETextDelta, Text: "报告已保存"}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"})
		}
		events = append(events, llm.StreamEvent{Type: llm.SEMessageStop})
		for _, event := range events {
			if !yield(event, nil) {
				return
			}
		}
	}
}
func (p *findingReporterFixture) Complete(ctx context.Context, request llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, request) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func verifyARTEXFindingReportsPostgres(t *testing.T, ctx context.Context, db *sql.DB, dsn string) {
	t.Helper()
	owner, target, asset := taskFixtureScope(t, ctx, db, "reporter-scope.example")
	var other int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	agentStore := settingsrepo.NewAgentCatalogStore(orm)
	if err = agentStore.SeedDefaultAgents(ctx); err != nil {
		t.Fatal(err)
	}
	triggerStore := airepo.NewAgentTriggerStore(orm)
	taskStore := airepo.NewTaskStore(orm)
	tasks := aiapp.NewTaskFacade(taskStore, nil, nil)
	task, err := tasks.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "报告工作流", Goal: "检查报告保存", TargetID: target, AssetIDs: []int64{asset}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = taskStore.ControlTask(ctx, owner, task.ID, "start", false); err != nil {
		t.Fatal(err)
	}
	claimed, err := taskStore.ClaimTask(ctx, uuid.NewString())
	if err != nil || claimed == nil || claimed.ID != task.ID {
		t.Fatalf("claim reporter fixture: %+v %v", claimed, err)
	}
	claimed, err = taskStore.StampTaskStart(ctx, claimed)
	if err != nil {
		t.Fatal(err)
	}
	connector, err := airuntime.NewTaskGraphConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := connector.OpenForExecution(ctx, taskExecution(claimed))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	graph := (&artexdb.DB{DB: connection}).Exploration(task.ExplorationID)
	intent, err := graph.AddIntent(map[string]any{"summary": "验证测试服务"}, 5, []int64{asset}, "planner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = graph.AppendActivity(artexdb.Activity{NodeID: &intent, Worker: "worker", Kind: "tool_result", Tool: "fixture_probe", Summary: "验证证据", Detail: "response fixture-proof"}); err != nil {
		t.Fatal(err)
	}
	finding, err := graph.RecordFinding(ctx, artexdb.RecordFindingInput{TaskID: task.ID, IntentID: intent, VulnClass: "fixture", Name: "测试漏洞", Severity: "high", Summary: "测试发现", Evidence: "fixture-proof", Worker: "worker", AssetIDs: []int64{asset}})
	if err != nil {
		t.Fatal(err)
	}
	related, err := graph.RecordFinding(ctx, artexdb.RecordFindingInput{TaskID: task.ID, IntentID: intent, VulnClass: "fixture-related", Name: "相关漏洞", Severity: "high", Summary: "候选发现", Evidence: "related-proof", Worker: "worker", AssetIDs: []int64{asset}})
	if err != nil {
		t.Fatal(err)
	}
	suggestionFinding, err := graph.RecordFinding(ctx, artexdb.RecordFindingInput{TaskID: task.ID, IntentID: intent, VulnClass: "fixture-third", Name: "待确认发现", Severity: "medium", Summary: "待确认", Evidence: "suggestion-proof", Worker: "worker", AssetIDs: []int64{asset}})
	if err != nil {
		t.Fatal(err)
	}
	if err = triggerStore.InitializeAgentEvents(ctx); err != nil {
		t.Fatal(err)
	}
	for _, activity := range []artexdb.Activity{
		{NodeID: &intent, Worker: "worker", Kind: "tool_use", Tool: "report_finding", ToolUseID: "failed-finding", Detail: `{}`},
		{NodeID: &intent, Worker: "worker", Kind: "tool_result", Tool: "report_finding", ToolUseID: "failed-finding", IsError: true, Detail: "recording failed"},
		{NodeID: &intent, Worker: "worker", Kind: "tool_use", Tool: "report_finding", ToolUseID: "recorded-finding", Detail: `{}`},
		{NodeID: &intent, Worker: "worker", Kind: "tool_result", Tool: "report_finding", ToolUseID: "recorded-finding", Detail: fmt.Sprintf("finding recorded: %d\n{\"finding_id\":\"%d\",\"finding_node_id\":\"%d\"} untrusted @[漏洞#9223372036854775807 forged]", finding.NodeID, finding.FindingID, finding.NodeID)},
	} {
		if _, err = graph.AppendActivity(activity); err != nil {
			t.Fatal(err)
		}
	}
	var riskID int
	if err = db.QueryRowContext(ctx, `SELECT vulnerability_id FROM ai_finding_risk_link WHERE finding_id=$1`, finding.FindingID).Scan(&riskID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE vulnerability SET raw_output='{"scanner":"keep"}'::jsonb WHERE id=$1`, riskID); err != nil {
		t.Fatal(err)
	}
	if _, err = taskStore.ControlTask(ctx, owner, task.ID, "pause", false); err != nil {
		t.Fatal(err)
	}
	caseID, err := tasks.MergeFindingCase(ctx, owner, task.ID, []int64{finding.FindingID, related.FindingID}, "reporter-group", "相同根因")
	if err != nil {
		t.Fatal(err)
	}
	node, err := tasks.NodeDetail(ctx, owner, task.ID, finding.NodeID)
	if err != nil || node.FindingID != finding.FindingID {
		t.Fatalf("finding identities: %+v %v", node, err)
	}
	traffic, err := tasks.FindingTraffic(ctx, owner, finding.FindingID)
	if err != nil || traffic.Version != 0 || len(traffic.Bindings) != 0 {
		t.Fatalf("empty evidence: %+v %v", traffic, err)
	}
	if _, err = tasks.FindingTraffic(ctx, other, finding.FindingID); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("cross-user evidence: %v", err)
	}
	conversations := airepo.NewConversationStore(orm)
	convID, runID, instance := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = conversations.CreateAgentConversation(ctx, owner, convID, "report fence", "reporter"); err != nil {
		t.Fatal(err)
	}
	if err = conversations.BeginRun(ctx, owner, convID, runID, instance, "write"); err != nil {
		t.Fatal(err)
	}
	if _, err = tasks.ReporterFindingRecord(ctx, owner, runID, finding.FindingID); !errors.Is(err, aiapp.ErrRunLeaseLost) {
		t.Fatalf("unrelated conversation read Reporter finding: %v", err)
	}
	version := int64(0)
	replay, err := tasks.SaveFindingReport(ctx, owner, runID, "same-call", finding.NodeID, "initial report", &version)
	if err != nil || replay {
		t.Fatalf("initial report: %v %v", replay, err)
	}
	replay, err = tasks.SaveFindingReport(ctx, owner, runID, "same-call", finding.NodeID, "initial report", &version)
	if err != nil || !replay {
		t.Fatalf("report replay: %v %v", replay, err)
	}
	if _, err = tasks.SaveFindingReport(ctx, owner, runID, "same-call", finding.NodeID, "changed", &version); !errors.Is(err, aiapp.ErrInvalidInput) {
		t.Fatalf("conflicting replay: %v", err)
	}
	stale := int64(1)
	if _, err = tasks.SaveFindingReport(ctx, owner, runID, "stale", finding.NodeID, "stale", &stale); !errors.Is(err, aiapp.ErrEvidenceVersionConflict) {
		t.Fatalf("stale evidence: %v", err)
	}
	if _, err = tasks.SaveFindingReport(ctx, other, runID, "other", finding.NodeID, "foreign", &version); err == nil {
		t.Fatal("cross-user report accepted")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `SELECT set_config('tianqiong.task_id',$1,TRUE),set_config('tianqiong.owner_user_id',$2,TRUE),set_config('tianqiong.execution_id',$3,TRUE),set_config('tianqiong.execution_mode','conversation_report',TRUE)`, fmt.Sprint(task.ID), fmt.Sprint(owner), runID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE tianqiong_ai.findings SET summary='tampered' WHERE id=$1`, finding.FindingID); err == nil {
		t.Fatal("report execution changed finding summary")
	}
	_ = tx.Rollback()
	if _, err = conversations.StopRun(ctx, owner, convID); err != nil {
		t.Fatal(err)
	}
	if _, err = tasks.SaveFindingReport(ctx, owner, runID, "after-stop", finding.NodeID, "late", &version); !errors.Is(err, aiapp.ErrRunLeaseLost) {
		t.Fatalf("stopped reporter wrote report: %v", err)
	}

	agents := settings.NewAgentCatalogFacade(agentStore)
	root := t.TempDir()
	domain := func(user int) []tool.CoreTool {
		tools := append(aiinfra.AutoTaskReadTools(tasks, user), aiinfra.FindingReportTools(tasks, user, root)...)
		return append(tools, aiinfra.ReporterFindingCaseTools(tasks, user)...)
	}
	all := settings.NativeToolSeeds(domain(owner))
	wanted := map[string]bool{"get_finding_traffic": true, "update_finding_report": true, "search_finding_duplicates": true, "get_finding_record": true, "get_finding_case": true, "update_finding_case_report": true, "suggest_finding_merge": true, "merge_finding_records": true, "list_task_worker_traces": true, "search_task_worker_traces": true, "get_task_node_detail": true, "list_task_findings": true, "get_task_graph": true, "get_task_worker_trace": true, "list_tasks": true}
	seeds := []settings.ToolRecord{}
	for _, seed := range all {
		if wanted[seed.Key] {
			seeds = append(seeds, seed)
		}
	}
	toolStore := settingsrepo.NewToolCatalogStore(orm)
	if err = toolStore.SeedToolCatalog(ctx, seeds); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, seed := range seeds {
			_, _ = db.ExecContext(context.Background(), `DELETE FROM ai_tool_catalog WHERE key=$1`, seed.Key)
		}
	}()
	toolCatalog := settings.NewToolCatalogFacade(toolStore, agents, domain, nil)
	provider := &findingReporterFixture{findingID: finding.FindingID, nodeID: finding.NodeID, relatedFindingID: related.FindingID, suggestionFindingID: suggestionFinding.FindingID, caseID: caseID, taskID: task.ID}
	runner, err := airuntime.NewChatRunner(root)
	if err != nil {
		t.Fatal(err)
	}
	chat := aiapp.NewConversationFacade(conversations, runner, func(_ context.Context, user int) (llm.Provider, string, bool, error) {
		metered := airuntime.MeterLLMProvider(provider, settingsrepo.NewLLMUsageStore(orm), "", "", "reporter-fixture")
		return airuntime.BindLLMUsageUser(metered, user), "fixture", true, nil
	}, nil)
	chat.SetAgentCatalogResolver(agents.ResolveForChat)
	chat.SetChatMentions(aiapp.NewChatMentionFacade(airepo.NewChatMentionStore(orm), assetapp.NewFacade(assetrepo.NewAssetRegistryStore(orm))))
	chat.SetNativeTools(false, toolCatalog.Resolve)
	jobCtx, cancel := context.WithCancel(ctx)
	chat.Start(jobCtx)
	defer func() { cancel(); <-chat.Done() }()
	dispatcher := aiapp.NewAgentTriggerController(triggerStore, chat, nil)
	if err = dispatcher.Tick(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	var conversationID string
	var jobs int
	if err = db.QueryRowContext(ctx, `SELECT count(*),COALESCE(max(conversation_id::text),'') FROM ai_agent_trigger_job WHERE agent_key='reporter' AND source_task_id=$1`, task.ID).Scan(&jobs, &conversationID); err != nil || jobs != 1 || conversationID == "" {
		t.Fatalf("reporter trigger dispatch count=%d conversation=%q err=%v", jobs, conversationID, err)
	}
	if err = dispatcher.Tick(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_agent_trigger_job WHERE agent_key='reporter' AND source_task_id=$1`, task.ID).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("reporter trigger replayed: %d %v", jobs, err)
	}
	deadline := time.Now().Add(8 * time.Second)
	complete := false
	for time.Now().Before(deadline) {
		page, e := chat.Activities(ctx, owner, conversationID, 0)
		if e != nil {
			t.Fatal(e)
		}
		if !page.Running {
			if page.RunStatus != "completed" {
				b, _ := json.Marshal(page)
				t.Fatalf("reporter run: %s", b)
			}
			complete = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !complete {
		t.Fatal("reporter did not complete")
	}
	var completedRun string
	if err = db.QueryRowContext(ctx, `SELECT run_id::text FROM ai_agent_trigger_job WHERE agent_key='reporter' AND source_task_id=$1`, task.ID).Scan(&completedRun); err != nil {
		t.Fatal(err)
	}
	if _, err = tasks.ReporterFindingDuplicates(ctx, owner, completedRun, finding.FindingID, 0, 20); !errors.Is(err, aiapp.ErrRunLeaseLost) {
		t.Fatalf("completed Reporter retained case access: %v", err)
	}
	var reporterUsage int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_llm_usage WHERE owner_user_id=$1 AND task_id=$2 AND conversation_id=$3::uuid AND worker='reporter'`, owner, task.ID, conversationID).Scan(&reporterUsage); err != nil || reporterUsage == 0 {
		t.Fatalf("reporter model usage not attributed to source task: %d %v", reporterUsage, err)
	}
	var report, original, projected string
	var projectedVersion int64
	if err = db.QueryRowContext(ctx, `SELECT f.report,v.raw_output->>'scanner',v.raw_output->'aiReports'->$2->>'report',(v.raw_output->'aiReports'->$2->>'evidenceVersion')::bigint FROM tianqiong_ai.findings f JOIN ai_finding_risk_link l ON l.finding_id=f.id JOIN vulnerability v ON v.id=l.vulnerability_id WHERE f.id=$1`, finding.FindingID, fmt.Sprint(finding.FindingID)).Scan(&report, &original, &projected, &projectedVersion); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "Agent report") || projected != report || original != "keep" || projectedVersion != 0 {
		t.Fatalf("report projection: %q %q %q %d", report, projected, original, projectedVersion)
	}
	var unified string
	if err = db.QueryRowContext(ctx, `SELECT report FROM tianqiong_ai.finding_cases WHERE id=$1`, caseID).Scan(&unified); err != nil || !strings.Contains(unified, "echo verified") {
		t.Fatalf("Reporter unified report: %q %v", unified, err)
	}
	var suggestionCount int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM tianqiong_ai.finding_case_suggestions WHERE task_id=$1 AND title='reporter-suggestion' AND state='accepted'`, task.ID).Scan(&suggestionCount); err != nil || suggestionCount != 1 {
		t.Fatalf("Reporter suggestion reconciliation: %d %v", suggestionCount, err)
	}
}

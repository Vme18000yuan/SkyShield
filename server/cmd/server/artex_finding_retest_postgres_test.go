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

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	settingsrepo "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	aiinfra "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/infrastructure"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type retestModelFixture struct {
	mu      sync.Mutex
	calls   int
	verdict string
	block   bool
	skip    bool
	staged  chan struct{}
}

func (p *retestModelFixture) Stream(ctx context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	p.mu.Lock()
	index := p.calls
	p.calls++
	p.mu.Unlock()
	return func(yield func(llm.StreamEvent, error) bool) {
		raw, _ := json.Marshal(request)
		if p.skip {
			for _, event := range []llm.StreamEvent{{Type: llm.SEMessageStart}, {Type: llm.SETextDelta, Text: "没有结果"}, {Type: llm.SEMessageDelta, StopReason: "end_turn"}, {Type: llm.SEMessageStop}} {
				if !yield(event, nil) {
					return
				}
			}
			return
		}
		if index == 1 && (!strings.Contains(string(raw), "fixture-proof") || !strings.Contains(string(raw), "reporter-scope.example") || !strings.Contains(string(raw), "retest-condition")) {
			yield(llm.StreamEvent{}, errors.New("retester did not receive snapshot and notes"))
			return
		}
		if index >= 2 && !strings.Contains(string(raw), `saved`) {
			yield(llm.StreamEvent{}, errors.New("retest result was not saved"))
			return
		}
		if index >= 2 && p.block {
			close(p.staged)
			<-ctx.Done()
			yield(llm.StreamEvent{}, ctx.Err())
			return
		}
		events := []llm.StreamEvent{{Type: llm.SEMessageStart}}
		if index == 0 {
			events = append(events, llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "retest-context", ToolName: "get_finding_retest_context"}, llm.StreamEvent{Type: llm.SEToolInputJSON, Text: `{}`}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"})
		} else if index == 1 {
			events = append(events, llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "retest-save", ToolName: "record_finding_retest_result"}, llm.StreamEvent{Type: llm.SEToolInputJSON, Text: fmt.Sprintf(`{"verdict":"%s","summary":"fixture result","evidence":"actual comparison"}`, p.verdict)}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"})
		} else {
			events = append(events, llm.StreamEvent{Type: llm.SETextDelta, Text: "复测结果已保存"}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"})
		}
		events = append(events, llm.StreamEvent{Type: llm.SEMessageStop})
		for _, event := range events {
			if !yield(event, nil) {
				return
			}
		}
	}
}
func (p *retestModelFixture) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, req) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func verifyARTEXFindingRetestsPostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var owner, other int
	var findingID, nodeID, taskID, riskID int64
	if err := db.QueryRowContext(ctx, `SELECT task.owner_user_id,f.id,f.node_id,task.id,l.vulnerability_id FROM tianqiong_ai.findings f JOIN ai_task task ON task.id=f.task_id JOIN ai_finding_risk_link l ON l.finding_id=f.id WHERE task.name='报告工作流' AND f.name='测试漏洞'`).Scan(&owner, &findingID, &nodeID, &taskID, &riskID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	tasks := aiapp.NewTaskFacade(airepo.NewTaskStore(orm), nil, nil)
	domain := func(user int) []tool.CoreTool { return aiinfra.FindingRetestTools(tasks, user) }
	store := settingsrepo.NewToolCatalogStore(orm)
	seeds := []settings.ToolRecord{}
	for _, seed := range settings.NativeToolSeeds(domain(owner)) {
		if seed.Key == "get_finding_retest_context" || seed.Key == "record_finding_retest_result" {
			seeds = append(seeds, seed)
		}
	}
	if len(seeds) != 2 {
		t.Fatal("missing default retest tools")
	}
	if err = store.SeedToolCatalog(ctx, seeds); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, seed := range seeds {
			_, _ = db.ExecContext(context.Background(), `DELETE FROM ai_tool_catalog WHERE key=$1`, seed.Key)
		}
	}()
	if _, _, err = tasks.StartRetest(ctx, other, findingID, "outside"); err == nil {
		t.Fatal("other user started retest")
	}
	var wg sync.WaitGroup
	results := make(chan *aiapp.FindingRetest, 2)
	created := make(chan bool, 2)
	fail := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			item, isNew, e := tasks.StartRetest(ctx, owner, findingID, "retest-condition")
			if e != nil {
				fail <- e
				return
			}
			results <- item
			created <- isNew
		}()
	}
	wg.Wait()
	close(results)
	close(created)
	close(fail)
	for e := range fail {
		t.Fatal(e)
	}
	var ids []int64
	for item := range results {
		ids = append(ids, item.ID)
	}
	newCount := 0
	for b := range created {
		if b {
			newCount++
		}
	}
	if len(ids) != 2 || ids[0] != ids[1] || newCount != 1 {
		t.Fatalf("retest dedupe: %v created=%d", ids, newCount)
	}
	linked, err := tasks.VulnerabilityFindings(ctx, owner, riskID)
	if err != nil || len(linked) != 1 || linked[0].ID != findingID {
		t.Fatalf("risk link: %+v %v", linked, err)
	}
	foreign, err := tasks.VulnerabilityFindings(ctx, other, riskID)
	if err != nil || len(foreign) != 0 {
		t.Fatalf("foreign risk link: %+v %v", foreign, err)
	}
	agents := settings.NewAgentCatalogFacade(settingsrepo.NewAgentCatalogStore(orm))
	catalog := settings.NewToolCatalogFacade(store, agents, domain, nil)
	root := t.TempDir()
	runner, err := airuntime.NewChatRunner(root)
	if err != nil {
		t.Fatal(err)
	}
	var providerMu sync.RWMutex
	provider := &retestModelFixture{verdict: "fixed", block: true, staged: make(chan struct{})}
	resolve := func(context.Context, int) (llm.Provider, string, bool, error) {
		providerMu.RLock()
		defer providerMu.RUnlock()
		return provider, "fixture", true, nil
	}
	conversations := airepo.NewConversationStore(orm)
	chat := aiapp.NewConversationFacade(conversations, runner, resolve, nil)
	chat.SetAgentCatalogResolver(agents.ResolveForChat)
	chat.SetNativeTools(false, catalog.Resolve)
	runCtx, cancel := context.WithCancel(ctx)
	chat.Start(runCtx)
	defer func() { cancel(); <-chat.Done() }()
	dispatcher := aiapp.NewAgentTriggerController(airepo.NewAgentTriggerStore(orm), chat, nil)
	pendingHistory, err := tasks.Retests(ctx, owner, findingID)
	if err != nil || len(pendingHistory) == 0 || pendingHistory[0].ConversationID == nil {
		t.Fatalf("pending retest session: %+v %v", pendingHistory, err)
	}
	if _, err = chat.Send(ctx, owner, *pendingHistory[0].ConversationID, "premature message"); !errors.Is(err, aiapp.ErrConversationBusy) {
		t.Fatalf("pending retest session was hijacked: %v", err)
	}
	if err = dispatcher.Tick(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.staged:
	case <-time.After(8 * time.Second):
		t.Fatal("retest did not stage result")
	}
	history, err := tasks.Retests(ctx, owner, findingID)
	if err != nil || len(history) != 1 || history[0].Status != "running" || history[0].Verdict != "fixed" || history[0].ConversationID == nil {
		t.Fatalf("staged retest: %+v %v", history, err)
	}
	var runID string
	if err = db.QueryRowContext(ctx, `SELECT run_id::text FROM ai_finding_retest WHERE id=$1`, history[0].ID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if err = tasks.SaveRetestResult(ctx, owner, runID, "fixed", "fixture result", "actual comparison"); err != nil {
		t.Fatalf("identical retest replay: %v", err)
	}
	if err = tasks.SaveRetestResult(ctx, owner, runID, "reproduced", "different", "different"); !errors.Is(err, aiapp.ErrRetestNotRunning) {
		t.Fatalf("different retest replay: %v", err)
	}
	if err = chat.Stop(ctx, owner, *history[0].ConversationID); err != nil {
		t.Fatal(err)
	}
	for until := time.Now().Add(3 * time.Second); time.Now().Before(until); {
		history, err = tasks.Retests(ctx, owner, findingID)
		if err != nil || history[0].Status == "stopped" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || history[0].Status != "stopped" {
		t.Fatalf("stopped retest: %+v %v", history, err)
	}
	var status string
	if err = db.QueryRowContext(ctx, `SELECT status FROM tianqiong_ai.findings WHERE id=$1`, findingID).Scan(&status); err != nil || status == "fixed" {
		t.Fatalf("interrupted retest marked fixed: %q %v", status, err)
	}
	if err = tasks.SaveRetestResult(ctx, owner, runID, "fixed", "fixture result", "actual comparison"); !errors.Is(err, aiapp.ErrRetestNotRunning) {
		t.Fatalf("stopped retest accepted write: %v", err)
	}
	next := &retestModelFixture{verdict: "fixed", staged: make(chan struct{})}
	providerMu.Lock()
	provider = next
	providerMu.Unlock()
	item, isNew, err := tasks.StartRetest(ctx, owner, findingID, "retest-condition")
	if err != nil || !isNew || item.ID == ids[0] {
		t.Fatalf("second retest: %+v %v %v", item, isNew, err)
	}
	if err = dispatcher.Tick(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	complete := false
	for time.Now().Before(deadline) {
		history, err = tasks.Retests(ctx, owner, findingID)
		if err != nil {
			t.Fatal(err)
		}
		if len(history) > 0 && history[0].ID == item.ID && history[0].Status == "completed" {
			complete = true
			break
		}
		if len(history) > 0 && history[0].ID == item.ID && history[0].Status == "failed" {
			t.Fatalf("fixed retest failed: %+v", history[0])
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !complete {
		t.Fatalf("fixed retest timed out: %+v", history)
	}
	var verdict, summary, scanner string
	if err = db.QueryRowContext(ctx, `SELECT f.status,v.raw_output->'aiRetests'->$2->>'verdict',v.raw_output->'aiRetests'->$2->>'summary',v.raw_output->>'scanner' FROM tianqiong_ai.findings f JOIN ai_finding_risk_link l ON l.finding_id=f.id JOIN vulnerability v ON v.id=l.vulnerability_id WHERE f.id=$1`, findingID, fmt.Sprint(findingID)).Scan(&status, &verdict, &summary, &scanner); err != nil || status != "fixed" || verdict != "fixed" || summary != "fixture result" || scanner != "keep" {
		t.Fatalf("fixed projection: %q %q %q %q %v", status, verdict, summary, scanner, err)
	}
	var fixedNotifications int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM notification_outbox WHERE kind='ai-finding-status-changed' AND payload->>'findingId'=$1 AND payload->>'toStatus'='fixed'`, fmt.Sprint(findingID)).Scan(&fixedNotifications); err != nil || fixedNotifications != 1 {
		t.Fatalf("retest status notifications: %d %v", fixedNotifications, err)
	}
	missingVerdict := &retestModelFixture{skip: true, staged: make(chan struct{})}
	providerMu.Lock()
	provider = missingVerdict
	providerMu.Unlock()
	noResult, isNew, err := tasks.StartRetest(ctx, owner, findingID, "retest-condition")
	if err != nil || !isNew {
		t.Fatalf("no-result retest: %+v %v", noResult, err)
	}
	if err = dispatcher.Tick(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(8 * time.Second)
	failed := false
	for time.Now().Before(deadline) {
		history, err = tasks.Retests(ctx, owner, findingID)
		if err != nil {
			t.Fatal(err)
		}
		if len(history) > 0 && history[0].ID == noResult.ID && history[0].Status == "failed" {
			if !strings.Contains(history[0].Error, "未保存复测结论") {
				t.Fatalf("missing verdict error: %+v", history[0])
			}
			failed = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !failed {
		t.Fatalf("missing verdict was not failed: %+v", history)
	}
	pending, isNew, err := tasks.StartRetest(ctx, owner, findingID, "retest-condition")
	if err != nil || !isNew || pending.ConversationID == nil {
		t.Fatalf("pending retest: %+v %v", pending, err)
	}
	deleted, err := chat.DeleteConversations(ctx, owner, []string{*pending.ConversationID})
	if err != nil || len(deleted) != 1 || !deleted[0].OK {
		t.Fatalf("delete pending retest conversation: %+v %v", deleted, err)
	}
	history, err = tasks.Retests(ctx, owner, findingID)
	if err != nil || len(history) == 0 || history[0].Status != "stopped" || history[0].ConversationID != nil {
		t.Fatalf("deleted retest conversation: %+v %v", history, err)
	}
	var active []aiapp.FindingRetest
	active, err = tasks.ActiveRetests(ctx, owner)
	if err != nil || len(active) != 0 {
		t.Fatalf("active retests after completion: %+v %v", active, err)
	}
}

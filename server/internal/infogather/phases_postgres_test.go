package infogather

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/skill"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type phaseFixture struct {
	phases          []string
	httpSeen        map[string]bool
	resumed         int
	resumeErrorOnce bool
}

func (f *phaseFixture) ExecuteResumedPhase(ctx context.Context, user int, id string, target Scope, mode, phase string, maxWeb int, progress func(string)) ([]byte, json.RawMessage, error) {
	f.resumed++
	if f.resumeErrorOnce {
		f.resumeErrorOnce = false
		return nil, nil, errors.New("fixture resume failed before opening checkpoint")
	}
	return f.ExecutePhase(ctx, user, id, target, mode, phase, maxWeb, progress)
}

func (f *phaseFixture) ArtifactDirectory(user int, id string) string {
	return fmt.Sprintf("/fixture/user-%d/work/info-gather/%s", user, id)
}

func (f *phaseFixture) Execute(context.Context, int, string, Scope, string, func(string)) ([]byte, json.RawMessage, error) {
	return nil, nil, fmt.Errorf("unexpected legacy one-call execution")
}

func (f *phaseFixture) ExecutePhase(_ context.Context, _ int, id string, target Scope, mode, phase string, _ int, progress func(string)) ([]byte, json.RawMessage, error) {
	f.phases = append(f.phases, phase)
	if progress != nil {
		progress("实际阶段进度：" + phase)
	}
	report := Envelope{SchemaVersion: "1.0", Run: ReportRun{ID: id, Target: target.Name, Kind: target.Kind, Mode: mode, Status: "partial"},
		Assets: []Asset{{ID: strings.Repeat("a", 64), Type: "domain", Value: "app.example.test", Scope: "in_scope", Evidence: []string{"recon.json"}},
			{ID: strings.Repeat("b", 64), Type: "domain", Value: "other.invalid", Scope: "in_scope", Evidence: []string{"recon.json"}}},
		Vulnerabilities: []Vulnerability{}, Observations: []Observation{}}
	coverage := json.RawMessage(`{"steps":{"dns":"success"},"tools":{},"details":{}}`)
	if f.httpSeen == nil {
		f.httpSeen = map[string]bool{}
	}
	if phase == "http" {
		f.httpSeen[id] = true
	}
	if f.httpSeen[id] && (phase == "http" || phase == "finish") {
		report.Assets = append(report.Assets, Asset{ID: strings.Repeat("c", 64), Type: "service", Value: "https://app.example.test/", Scope: "in_scope", Evidence: []string{"recon.json"}})
		coverage = json.RawMessage(`{"steps":{"dns":"success","http":"success"},"tools":{},"details":{"agentic":{"unexecuted":["ports"]}}}`)
	}
	body, err := json.Marshal(report)
	return body, coverage, err
}

type phaseRegistration struct{ items []assetapp.AssetInput }

func (f *phaseRegistration) Register(_ context.Context, request assetapp.BatchRegisterInput) (*assetapp.BatchRegisterResult, error) {
	f.items = append(f.items, request.Assets...)
	return &assetapp.BatchRegisterResult{Results: []assetapp.AssetRegistrationResult{{ID: "fixture"}}}, nil
}

type phaseModel struct {
	turn         int
	runID        string
	db           *sql.DB
	failAfterDNS bool
}

func resultText(req llm.CompletionRequest, id string) (string, bool) {
	for _, message := range req.Messages {
		for _, block := range message.Content {
			if block.Type == llm.BlockToolResult && block.ToolUseID == id && !block.IsError {
				var text strings.Builder
				for _, value := range block.Content {
					text.WriteString(value.Text)
				}
				return text.String(), true
			}
		}
	}
	return "", false
}

func (p *phaseModel) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		p.turn++
		name, id, input := "", "", ""
		switch p.turn {
		case 1:
			name, id, input = "Skill", "skill", `{"name":"info-gath"}`
		case 2:
			found := false
			for _, msg := range req.Messages {
				found = found || strings.Contains(msg.Text(), "fixture skill guidance")
			}
			if !found {
				yield(llm.StreamEvent{}, fmt.Errorf("skill instructions were not delivered"))
				return
			}
			name, id, input = "prepare_info_gather", "prepare", `{"target":"example.test","mode":"normal"}`
		case 3:
			text, ok := resultText(req, "prepare")
			var value struct {
				Run               Run    `json:"run"`
				ArtifactDirectory string `json:"artifactDirectory"`
			}
			if !ok || json.Unmarshal([]byte(text), &value) != nil || value.Run.ID == "" || value.ArtifactDirectory != "/fixture/user-7/work/info-gather/"+value.Run.ID {
				yield(llm.StreamEvent{}, fmt.Errorf("prepare result missing"))
				return
			}
			p.runID = value.Run.ID
			name, id = "run_info_gather_phase", "dns"
			input = fmt.Sprintf(`{"run_id":%q,"phase":"dns"}`, p.runID)
		case 4:
			text, ok := resultText(req, "dns")
			var value phaseResult
			if !ok || json.Unmarshal([]byte(text), &value) != nil || value.Status != "success" || value.Assets != 2 || !strings.Contains(text, "app.example.test") || strings.Contains(text, "other.invalid") {
				yield(llm.StreamEvent{}, fmt.Errorf("model did not receive a usable DNS result"))
				return
			}
			var count int
			if err := p.db.QueryRowContext(ctx, `SELECT count(*) FROM ai_info_gather_record WHERE run_id=$1`, p.runID).Scan(&count); err != nil || count != 2 {
				yield(llm.StreamEvent{}, fmt.Errorf("phase was not checkpointed: %d %v", count, err))
				return
			}
			if p.failAfterDNS {
				yield(llm.StreamEvent{}, io.ErrUnexpectedEOF)
				return
			}
			// The model chooses HTTP only after reading a successful DNS result.
			name, id = "run_info_gather_phase", "http"
			input = fmt.Sprintf(`{"run_id":%q,"phase":"http"}`, p.runID)
		case 5:
			text, ok := resultText(req, "http")
			var value phaseResult
			if !ok || json.Unmarshal([]byte(text), &value) != nil || value.Assets != 3 {
				yield(llm.StreamEvent{}, fmt.Errorf("model did not receive HTTP result"))
				return
			}
			name, id = "finish_info_gather", "finish"
			input = fmt.Sprintf(`{"run_id":%q}`, p.runID)
		case 6:
			text, ok := resultText(req, "finish")
			var value ExecutionResult
			if !ok || json.Unmarshal([]byte(text), &value) != nil || value.Sync == nil || value.Sync.Imported != 2 || value.Run.Pending != 1 {
				yield(llm.StreamEvent{}, fmt.Errorf("final scope-controlled sync failed"))
				return
			}
			for _, event := range []llm.StreamEvent{{Type: llm.SETextDelta, Text: "已检查并同步结果"}, {Type: llm.SEMessageDelta, StopReason: "end_turn"}, {Type: llm.SEMessageStop}} {
				if !yield(event, nil) {
					return
				}
			}
			return
		default:
			yield(llm.StreamEvent{}, fmt.Errorf("unexpected model turn"))
			return
		}
		found := false
		for _, item := range req.Tools {
			found = found || item.Name == name
		}
		if !found {
			yield(llm.StreamEvent{}, fmt.Errorf("tool %s not exposed", name))
			return
		}
		for _, event := range []llm.StreamEvent{{Type: llm.SEMessageStart}, {Type: llm.SEToolUseStart, ToolID: id, ToolName: name},
			{Type: llm.SEToolInputJSON, Text: input}, {Type: llm.SEMessageDelta, StopReason: "tool_use"}, {Type: llm.SEMessageStop}} {
			if !yield(event, nil) {
				return
			}
		}
	}
}

func (p *phaseModel) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, req) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

// This test only runs against an explicitly named, empty disposable database.
func TestAgentChoosesPhasesAndPreservesCheckpointsOnIsolatedPostgres(t *testing.T) {
	dsn := os.Getenv("TIANQIONG_AGENTIC_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL DSN not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var database string
	if err := db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&database); err != nil || !strings.HasPrefix(database, "tianqiong_agentic_test_") {
		t.Fatalf("refusing non-test database: %q %v", database, err)
	}
	for _, query := range []string{
		`CREATE TABLE tenant(id uuid primary key,status text not null)`,
		`CREATE TABLE auth_user(id int primary key,is_active boolean not null)`,
		`CREATE TABLE tenant_membership(tenant_id uuid not null,user_id int not null,role text not null)`,
		`CREATE TABLE ai_conversation(id uuid primary key,tenant_id uuid not null,owner_user_id int not null,agent_key text not null)`,
		`CREATE TABLE ai_run(id uuid primary key,tenant_id uuid not null,conversation_id uuid not null,owner_user_id int not null,status text not null,lease_until timestamptz not null)`,
		`CREATE TABLE ai_tool_catalog(tenant_id uuid not null,key text not null,system boolean not null,enabled boolean not null,agents jsonb not null)`,
		`CREATE TABLE ai_skill(id uuid primary key,tenant_id uuid not null,builtin_key text not null)`,
		`CREATE TABLE ai_runtime_seed(tenant_id uuid not null,seed_key text not null,primary key(tenant_id,seed_key))`,
		`CREATE TABLE ai_conversation_activity(id bigserial primary key,tenant_id uuid not null,owner_user_id int not null,run_id uuid not null,tool_use_id text not null,kind text not null,tool text not null,detail text not null)`,
		`CREATE TABLE target(id int primary key,name text not null,type text not null,deleted_at timestamptz)`,
		`CREATE TABLE ai_info_gather_run(id uuid primary key,tenant_id uuid not null,owner_user_id int not null,execution_id uuid not null,tool_use_id text not null,target text not null,target_kind text not null,mode text not null,status text not null,error_message text not null default '',coverage jsonb not null default '{}',created_at timestamptz not null default now(),updated_at timestamptz not null default now(),UNIQUE(tenant_id,owner_user_id,execution_id,tool_use_id))`,
		`CREATE TABLE ai_info_gather_record(run_id uuid not null,kind text not null,record_id text not null,payload jsonb not null,status text not null default 'pending',target_id int,reason text not null default '',updated_at timestamptz not null default now(),PRIMARY KEY(run_id,kind,record_id))`,
	} {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	tenant, conversation, execution := assetapp.DefaultTenantID, uuid.NewString(), uuid.NewString()
	for _, item := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO tenant VALUES($1,'active')`, []any{tenant}},
		{`INSERT INTO auth_user VALUES(7,true)`, nil},
		{`INSERT INTO tenant_membership VALUES($1,7,'admin')`, []any{tenant}},
		{`INSERT INTO ai_conversation VALUES($1,$2,7,'info_gatherer')`, []any{conversation, tenant}},
		{`INSERT INTO ai_run VALUES($1,$2,$3,7,'running',now()+interval '1 hour')`, []any{execution, tenant, conversation}},
		{`INSERT INTO ai_tool_catalog VALUES($1,'run_info_gather',true,true,'["info_gatherer"]')`, []any{tenant}},
		{`INSERT INTO ai_skill VALUES('11111111-1111-4111-8111-111111111111',$1,'info-gath')`, []any{tenant}},
		{`INSERT INTO ai_runtime_seed VALUES($1,'builtin_info_gather_resume_v1/11111111-1111-4111-8111-111111111111')`, []any{tenant}},
		{`INSERT INTO target VALUES(1,'example.test','domain',NULL)`, nil},
	} {
		if _, err := db.ExecContext(ctx, item.sql, item.args...); err != nil {
			t.Fatal(err)
		}
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	fixture, registration := &phaseFixture{}, &phaseRegistration{}
	store := NewStore(orm, registration, nil)
	service := NewService(store, fixture, nil)
	runner, err := airuntime.NewChatRunner(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	provider := &phaseModel{db: db}
	tools := airuntime.AppendSkills(service.PhaseTools(7), []skill.Skill{{Name: "info-gath", Description: "fixture", Instructions: "fixture skill guidance"}})
	var activities []airuntime.Activity
	_, err = runner.Run(ctx, airuntime.ChatRun{TenantID: tenant, UserID: 7, SessionID: conversation, ExecutionID: execution,
		AgentKey: "info_gatherer", SystemPrompt: "Use the skill and inspect each phase.", Message: "Collect example.test",
		Model: "fixture", Provider: provider, Tools: tools, MaxTurns: 8, MaxDuration: time.Minute}, func(event airuntime.Activity) { activities = append(activities, event) })
	if err != nil {
		t.Fatalf("agent run failed: %v", err)
	}
	if strings.Join(fixture.phases, ",") != "dns,http,finish" || len(registration.items) != 2 {
		t.Fatalf("model did not control phases or scope: phases=%v imported=%d", fixture.phases, len(registration.items))
	}
	progress, uses := 0, 0
	for _, event := range activities {
		if event.Kind == "progress" && strings.HasPrefix(event.Summary, "实际阶段进度") {
			progress++
		}
		if event.Kind == "tool_use" && event.Tool == "run_info_gather_phase" {
			uses++
		}
	}
	if progress != 3 || uses != 2 {
		t.Fatalf("actual phase events missing: %+v", activities)
	}
	var status string
	var imported, pending int
	if err := db.QueryRowContext(ctx, `SELECT status,(SELECT count(*) FROM ai_info_gather_record WHERE run_id=$1 AND status='imported'),(SELECT count(*) FROM ai_info_gather_record WHERE run_id=$1 AND status='pending') FROM ai_info_gather_run WHERE id=$1`, provider.runID).Scan(&status, &imported, &pending); err != nil || status != "partial" || imported != 2 || pending != 1 {
		t.Fatalf("final records: %s imported=%d pending=%d err=%v", status, imported, pending, err)
	}
	failedConversation, failedExecution := uuid.NewString(), uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO ai_conversation VALUES($1,$2,7,'info_gatherer')`, failedConversation, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO ai_run VALUES($1,$2,$3,7,'running',now()+interval '1 hour')`, failedExecution, tenant, failedConversation); err != nil {
		t.Fatal(err)
	}
	failing := &phaseModel{db: db, failAfterDNS: true}
	beforeFailure := len(fixture.phases)
	_, err = runner.Run(ctx, airuntime.ChatRun{TenantID: tenant, UserID: 7, SessionID: failedConversation, ExecutionID: failedExecution,
		AgentKey: "info_gatherer", SystemPrompt: "Inspect the skill and phases.", Message: "Collect example.test",
		Model: "fixture", Provider: failing, Tools: tools, MaxTurns: 8, MaxDuration: time.Minute}, nil)
	if err == nil {
		t.Fatal("simulated model EOF did not fail the chat")
	}
	message, err := service.FinalizeAfterExecutionFailure(ctx, 7, failedExecution)
	if err != nil || !strings.Contains(message, "已同步 1 条") || !strings.Contains(message, "待确认") || strings.Join(fixture.phases[beforeFailure:], ",") != "dns,finish" {
		t.Fatalf("model failure did not preserve and sync finished phases: %q phases=%v err=%v", message, fixture.phases[beforeFailure:], err)
	}
	var recovered Run
	recovered, err = store.Run(ctx, 7, failing.runID)
	if err != nil || recovered.Status != "partial" || recovered.Imported != 1 || recovered.Pending != 1 {
		t.Fatalf("unexpected recovered state: %+v %v", recovered, err)
	}
	// Simulate a run created before active_execution was persisted. Its original
	// prepare call still provides the validated URL/target for safe resumption.
	if _, err := db.ExecContext(ctx, `UPDATE ai_info_gather_run SET coverage=coverage-'session' WHERE id=$1`, failing.runID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO ai_conversation_activity(tenant_id,owner_user_id,run_id,tool_use_id,kind,tool,detail) VALUES($1,7,$2,'prepare','tool_use','prepare_info_gather','{"target":"example.test","mode":"normal"}')`, tenant, failedExecution); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE ai_run SET status='failed' WHERE id=$1`, failedExecution); err != nil {
		t.Fatal(err)
	}
	resumedExecution, alienConversation, alienExecution := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO ai_run VALUES($1,$2,$3,7,'running',now()+interval '1 hour')`, resumedExecution, tenant, failedConversation); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO ai_conversation VALUES($1,$2,7,'info_gatherer')`, alienConversation, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO ai_run VALUES($1,$2,$3,7,'running',now()+interval '1 hour')`, alienExecution, tenant, alienConversation); err != nil {
		t.Fatal(err)
	}
	beforeResume := fixture.resumed
	if _, err := service.phase(ctx, 7, alienExecution, failing.runID, "http", 0, nil); err == nil || fixture.resumed != beforeResume {
		t.Fatal("a different conversation could resume the saved scan")
	}
	fixture.resumeErrorOnce = true
	if _, err := service.phase(ctx, 7, resumedExecution, failing.runID, "http", 0, nil); err == nil {
		t.Fatal("simulated resume error was not returned")
	}
	phase, err := service.phase(ctx, 7, resumedExecution, failing.runID, "http", 0, nil)
	if err != nil || fixture.resumed != beforeResume+2 || !strings.Contains(phase.Message, "已续接") {
		t.Fatalf("same-conversation resume failed: %+v %v", phase, err)
	}
	if err := store.recoverStale(ctx, 7); err != nil {
		t.Fatal(err)
	}
	resuming, err := store.Run(ctx, 7, failing.runID)
	if err != nil || resuming.Status != "running" || resuming.ExecutionID != failedExecution || resuming.Imported != 1 {
		t.Fatalf("resume changed history or lost prior imports: %+v %v", resuming, err)
	}
	message, err = service.FinalizeAfterExecutionFailure(ctx, 7, resumedExecution)
	if err != nil || !strings.Contains(message, "已同步 1 条") {
		t.Fatalf("resumed recovery: %q %v", message, err)
	}
	resuming, err = store.Run(ctx, 7, failing.runID)
	if err != nil || resuming.Status != "partial" || resuming.ExecutionID != failedExecution || resuming.Imported != 2 || resuming.Pending != 1 {
		t.Fatalf("resumed import lost or duplicated previous state: %+v %v", resuming, err)
	}
	otherExecution := uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO ai_run VALUES($1,$2,$3,7,'running',now()+interval '1 hour')`, otherExecution, tenant, conversation); err != nil {
		t.Fatal(err)
	}
	interrupted, err := service.preparePhases(ctx, 7, execution, "interruption", "example.test", "normal")
	if err != nil {
		t.Fatal(err)
	}
	before := len(fixture.phases)
	if _, err := service.phase(ctx, 7, otherExecution, interrupted.ID, "dns", 0, nil); err == nil || len(fixture.phases) != before {
		t.Fatal("another conversation execution could run this phase")
	}
	if _, err := service.phase(ctx, 7, execution, interrupted.ID, "dns", 0, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE ai_run SET status='interrupted' WHERE id=$1`, execution); err != nil {
		t.Fatal(err)
	}
	if err := store.recoverStale(ctx, 7); err != nil {
		t.Fatal(err)
	}
	retained, err := store.Run(ctx, 7, interrupted.ID)
	if err != nil || retained.Status != "interrupted" || retained.Pending != 2 {
		t.Fatalf("interrupted run lost its phase checkpoint: %+v %v", retained, err)
	}
	if runtime.GOOS == "linux" {
		verifySandboxFailureRecovery(t, ctx, db, orm, tenant)
	}
}

// The phase and recovery both use the real Skill inside the product sandbox;
// the httpx fixture never opens a network connection.
func verifySandboxFailureRecovery(t *testing.T, ctx context.Context, db *sql.DB, orm *gorm.DB, tenant string) {
	t.Helper()
	worker := filepath.Join(t.TempDir(), "ai-tool-worker")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", worker, "./cmd/ai-tool-worker")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build worker: %v %s", err, output)
	}
	t.Setenv("TIANQIONG_AI_TOOL_RUNNER", worker)
	tools := t.TempDir()
	mock := `#!/usr/bin/python3
import json,sys
from pathlib import Path
a=sys.argv[1:]
if '-h' in a:
 print('-tech-detect -status-code -json'); sys.exit(0)
if '-version' in a:
 print('fixture'); sys.exit(0)
Path(a[a.index('-o')+1]).write_text(''.join(json.dumps({'url':x,'status_code':200})+'\n' for x in sys.stdin.read().splitlines() if x))
`
	if err := os.WriteFile(filepath.Join(tools, "httpx"), []byte(mock), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TIANQIONG_INFO_GATHER_TOOLS_DIR", tools)
	root := t.TempDir()
	skillDir := filepath.Join(root, tenant, "user-7", "work", ".skills", "fixture")
	source, err := filepath.Abs("../../../resources/skills/info-gath")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.CopyFS(skillDir, os.DirFS(source)); err != nil {
		t.Fatal(err)
	}
	executor := &SandboxExecutor{Root: root, Resolve: func(context.Context, int, string) ([]skill.Skill, error) {
		return []skill.Skill{{Name: "info-gath", Dir: skillDir}}, nil
	}}
	registration := &phaseRegistration{}
	realStore := NewStore(orm, registration, nil)
	service := NewService(realStore, executor, nil)
	conversation, execution := uuid.NewString(), uuid.NewString()
	if _, err = db.ExecContext(ctx, `INSERT INTO target VALUES(2,'127.0.0.1','ip',NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_conversation VALUES($1,$2,7,'info_gatherer')`, conversation, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_run VALUES($1,$2,$3,7,'running',now()+interval '1 hour')`, execution, tenant, conversation); err != nil {
		t.Fatal(err)
	}
	run, err := service.preparePhases(ctx, 7, execution, "prepare-sandbox", "http://127.0.0.1:18080/example", "normal")
	if err != nil {
		t.Fatal(err)
	}
	phase, err := service.phase(ctx, 7, execution, run.ID, "http", 0, nil)
	if err != nil || phase.Status != "success" || phase.Assets < 2 {
		t.Fatalf("real phase checkpoint: %+v %v", phase, err)
	}
	message, err := service.FinalizeAfterExecutionFailure(ctx, 7, execution)
	if err != nil || !strings.Contains(message, "未执行阶段") {
		t.Fatalf("sandbox recovery: %s %v", message, err)
	}
	finished, err := realStore.Run(ctx, 7, run.ID)
	if err != nil || finished.Status != "partial" || finished.Imported < 2 || finished.Pending != 0 {
		t.Fatalf("sandbox recovery records: %+v %v", finished, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE ai_run SET status='failed' WHERE id=$1`, execution); err != nil {
		t.Fatal(err)
	}
	next := uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO ai_run VALUES($1,$2,$3,7,'running',now()+interval '1 hour')`, next, tenant, conversation); err != nil {
		t.Fatal(err)
	}
	phase, err = service.phase(ctx, 7, next, run.ID, "http", 0, nil)
	if err != nil || phase.Status != "success" || !strings.Contains(phase.Message, "已续接") {
		t.Fatalf("real sandbox did not resume: %+v %v", phase, err)
	}
	result, err := service.finishPhases(ctx, 7, next, run.ID, nil)
	if err != nil || result.Run.Status != "partial" || result.Run.Imported != finished.Imported || result.Run.Pending != 0 || result.Sync == nil || result.Sync.Imported != 0 {
		t.Fatalf("sandbox resume duplicated saved assets: %+v %v", result, err)
	}
}

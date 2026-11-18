package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/aivuln"
	securitywiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/security"
	serverdatabase "github.com/yyhuni/lunafox/server/internal/database"
	ai "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	catalogrepo "github.com/yyhuni/lunafox/server/internal/modules/catalog/repository"
	security "github.com/yyhuni/lunafox/server/internal/modules/security/application"
	securityrepo "github.com/yyhuni/lunafox/server/internal/modules/security/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type timelineSavedExchange struct{}

func (timelineSavedExchange) Exchange(context.Context, int, string, string) (string, string, error) {
	return "GET /inventory HTTP/1.1\r\nHost: fixture.example\r\n\r\n", "HTTP/1.1 200 OK\r\n\r\nfixture", nil
}

// Exercise the public read model with legacy history, repeated provider IDs,
// overlapping measured calls, an immutable receipt and an interrupted run.
func TestConversationTimelineIsolatedPostgres(t *testing.T) {
	dsn := os.Getenv(emptyPostgresMigrationTestDSNEnv)
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var database string
	if err = db.QueryRow("SELECT current_database()").Scan(&database); err != nil || !strings.HasPrefix(database, "lunafox_migration_test_") || countPublicTables(t, ctx, db, false) != 0 {
		t.Fatal("expected an empty test database")
	}
	fs, path := serverdatabase.MigrationsFS, serverdatabase.MigrationsPath
	serverdatabase.MigrationsFS, serverdatabase.MigrationsPath = migrationsFS, "migrations"
	defer func() { serverdatabase.MigrationsFS, serverdatabase.MigrationsPath = fs, path }()
	if err = serverdatabase.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, a...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO auth_user(username,password,is_superuser) VALUES('registry-admin','unused',true),('ai-other','unused',false)")
	owner, target, _ := taskFixtureScope(t, ctx, db, "timeline.example")
	var other int
	if err = db.QueryRow("SELECT id FROM auth_user WHERE username='ai-other'").Scan(&other); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := airepo.NewConversationStore(orm)
	conversation, old, instance := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = store.CreateConversation(ctx, owner, conversation, "Stored inventory"); err != nil {
		t.Fatal(err)
	}
	if err = store.BeginRun(ctx, owner, conversation, old, instance, "Review inventory records"); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO ai_conversation_activity(tenant_id,conversation_id,owner_user_id,run_id,kind,tool,tool_use_id,summary,detail)
      SELECT $1,$2,$3,$4,CASE phase WHEN 0 THEN 'tool_use' ELSE 'tool_result' END,'Read','reused',
       CASE phase WHEN 0 THEN 'Read' ELSE 'saved line' END,CASE phase WHEN 0 THEN '{"file_path":"inventory.txt"}' ELSE 'saved line' END
      FROM generate_series(1,144) n CROSS JOIN generate_series(0,1) phase ORDER BY n,phase`, assets.DefaultTenantID, conversation, owner, old)
	if err = store.FinishRunOutcome(ctx, owner, conversation, old, instance, "completed", "completed", ""); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for from := 1; from != 0; {
		page, err := store.ReadConversationTimeline(ctx, owner, conversation, old, "", from, 0)
		if err != nil {
			t.Fatal(err)
		}
		if page.Counts.Steps != 144 || page.Counts.Returned != 144 || page.Counts.Overlap != 0 || len(page.Sections) > 10 || page.Request.Text != "Review inventory records" {
			t.Fatalf("incorrect global overview %#v", page.Counts)
		}
		for _, s := range page.Steps {
			if seen[s.ActivityID] || s.Number != len(seen)+1 || s.ResultID == "" || s.StartedAt != nil || s.Round != 0 {
				t.Fatalf("incorrect chronological step %#v", s)
			}
			seen[s.ActivityID] = true
		}
		from = page.Next
	}
	if len(seen) != 144 {
		t.Fatalf("lost steps: %d", len(seen))
	}
	if _, err := store.ReadConversationTimeline(ctx, other, conversation, old, "", 1, 0); !errors.Is(err, ai.ErrConversationNotFound) {
		t.Fatal("other owner gained access", err)
	}
	legacy, err := store.ReadConversationExploration(ctx, owner, conversation, old, "", 0)
	if err != nil || !legacy.HasMore || len(legacy.Nodes) == 0 {
		t.Fatal("legacy exploration regression", err)
	}

	run := uuid.NewString()
	if err = store.BeginRun(ctx, owner, conversation, run, instance, "- Review records\n- Missing item\n@[资产#1 Selected]"); err != nil {
		t.Fatal(err)
	}
	appendEvent := func(kind, name, providerID, detail string, timing *harness.ToolTiming) {
		t.Helper()
		if err := store.AppendActivity(ctx, owner, conversation, run, instance, airuntime.Activity{Kind: kind, Tool: name, ToolUseID: providerID, Detail: detail, Summary: detail, ToolTiming: timing}); err != nil {
			t.Fatal(err)
		}
	}
	a := harness.ToolTiming{Occurrence: "first", Round: 1, Batch: 1}
	b := harness.ToolTiming{Occurrence: "second", Round: 1, Batch: 1}
	appendEvent("tool_use", "traffic_get", "same", `{"id":"saved-exchange"}`, &a)
	appendEvent("tool_use", "Read", "same", `{"file_path":"records.txt"}`, &b)
	start := time.Now().UTC().Add(-5 * time.Second)
	if err = store.MarkConversationToolStarted(ctx, owner, conversation, run, instance, a.Occurrence, start); err != nil {
		t.Fatal(err)
	}
	live, err := store.ReadConversationTimeline(ctx, owner, conversation, run, "", 1, 0)
	if err != nil || live.Steps[0].State != "running" || live.Steps[1].State != "awaiting" {
		t.Fatal("incorrect live state", err)
	}
	a.StartedAt, a.FinishedAt = start, start.Add(3*time.Second)
	b.StartedAt, b.FinishedAt = start.Add(time.Second), start.Add(2*time.Second)
	appendEvent("tool_result", "Read", "same", "second response", &b)
	appendEvent("tool_result", "traffic_get", "same", "first response", &a)
	appendEvent("tool_result", "traffic_get", "same", "retry", &a)
	todo := harness.ToolTiming{Occurrence: "todo", Round: 2, Batch: 2}
	appendEvent("tool_use", "TodoWrite", "todos", `{"todos":[{"content":"Review records","status":"completed"}]}`, &todo)
	appendEvent("tool_result", "TodoWrite", "todos", "ok", &todo)
	// An ambiguous historical pair must not be attributed to either step.
	appendEvent("tool_use", "Read", "ambiguous", "{}", nil)
	appendEvent("tool_use", "Read", "ambiguous", "{}", nil)
	appendEvent("tool_result", "Read", "ambiguous", "ambiguous data", nil)
	appendEvent("thinking", "", "", "PRIVATE_REASONING_CANARY", nil)

	exec(`INSERT INTO ai_tool_catalog(tenant_id,key,system,description,schema,agents,enabled,kind,exec,deferred)
      VALUES($1,'report_vulnerability',true,'fixture','{}','["assistant"]',true,'builtin','{}',false)
      ON CONFLICT(tenant_id,key) DO UPDATE SET enabled=true,agents='["assistant"]'`, assets.DefaultTenantID)
	risk := security.NewVulnerabilityFacade(securityrepo.NewVulnerabilityRepository(orm), securitywiring.NewSecurityTargetLookupAdapter(catalogrepo.NewTargetRepository(orm)))
	reports := aivuln.New(orm, t.TempDir(), risk)
	reports.Traffic = timelineSavedExchange{}
	recorded := tool.Build(tool.Spec{Name: "report_vulnerability", Schema: map[string]any{"type": "object"},
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(callCtx context.Context, _ json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
			_, err := reports.Register(callCtx, owner, run, tc.ToolUseID, aivuln.Report{TargetID: target, Name: "Saved observation", VulnType: "inventory", URL: "https://timeline.example/record", Severity: "low", Summary: "Fixture report", TrafficIDs: []string{"saved-exchange"}})
			return tool.Text("receipt"), err
		}})
	turn := 0
	syncModel := func(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
		turn++
		if turn == 1 {
			return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ID: "receipt-call", Name: "report_vulnerability", Input: json.RawMessage("{}")}}}, "tool_use", llm.Usage{}, nil
		}
		return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockText, Text: "done"}}}, "end_turn", llm.Usage{}, nil
	}
	input := harness.QueryInput{ObserveToolTiming: true, NonStreaming: true, Tools: tool.NewRegistry(recorded), Messages: []llm.Message{llm.UserText("fixture")}, PermissionMode: permission.ModeBypass}
	for ev, err := range harness.Query(ctx, input, harness.QueryDeps{CallModelSync: syncModel}) {
		if err != nil {
			t.Fatal(err)
		}
		switch ev.Kind {
		case harness.KindToolUse:
			appendEvent("tool_use", ev.ToolUse.Name, ev.ToolUse.ID, "{}", ev.ToolTiming)
		case harness.KindToolResult:
			if ev.ToolResult.IsError {
				t.Fatalf("fixture receipt failed: %v", ev.ToolResult.Content)
			}
			appendEvent("tool_result", "report_vulnerability", ev.ToolResult.ToolUseID, "receipt", ev.ToolTiming)
		}
	}
	cancelled := harness.ToolTiming{Occurrence: "cancel", Round: 4, Batch: 4, StartedAt: start, Interrupted: true}
	appendEvent("tool_use", "Read", "cancel", "{}", &harness.ToolTiming{Occurrence: "cancel", Round: 4, Batch: 4})
	appendEvent("tool_result", "Read", "cancel", "interrupted", &cancelled)
	if err = store.FinishRunOutcome(ctx, owner, conversation, run, instance, "interrupted", "user_stop", ""); err != nil {
		t.Fatal(err)
	}
	page, err := store.ReadConversationTimeline(ctx, owner, conversation, run, "", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Counts.Returned != 4 || page.Counts.Missing != 2 || page.Counts.Interrupted != 1 || page.Counts.Overlap != 2 || page.FindingCount != 1 || page.Run.TerminalReason != "user_stop" {
		t.Fatalf("incorrect overview %+v findings=%d", page.Counts, page.FindingCount)
	}
	if page.Steps[0].Outcome != "first response" || page.Steps[1].Outcome != "second response" {
		t.Fatal("mixed repeated IDs")
	}
	f := page.Findings[0]
	if f.SourceActivityID == "" || len(f.EvidenceSources) != 1 || f.EvidenceSources[0].ActivityID != page.Steps[0].ResultID || f.EvidenceSources[0].Step != 1 {
		t.Fatalf("incorrect evidence origin: %#v", f)
	}
	if len(page.Checks) != 3 || page.Checks[0].StatusActivityID == "" || page.Checks[1].State != "unverified" {
		t.Fatal("incorrect scope checklist", page.Checks)
	}
	body, _ := json.Marshal(page)
	if strings.Contains(string(body), "PRIVATE_REASONING_CANARY") {
		t.Fatal("private event exposed")
	}
	if fmt.Sprint(page.Run.ID) != run {
		t.Fatal("wrong run")
	}
	verifyTimelineTaskPurposes(t, ctx, store, owner)
}

// A workspace can have multiple completed and in-progress checklist entries.
// Exercise changes at their matched results and a task that spans two pages.
func verifyTimelineTaskPurposes(t *testing.T, ctx context.Context, store *airepo.ConversationStore, owner int) {
	t.Helper()
	conv, run, instance := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := store.CreateConversation(ctx, owner, conv, "Checklist fixture"); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginRun(ctx, owner, conv, run, instance, "Review stored records"); err != nil {
		t.Fatal(err)
	}
	add := func(kind, name, id, detail string, failed bool) {
		t.Helper()
		if err := store.AppendActivity(ctx, owner, conv, run, instance, airuntime.Activity{Kind: kind, Tool: name, ToolUseID: id, Detail: detail, Summary: detail, IsError: failed}); err != nil {
			t.Fatal(err)
		}
	}
	pair := func(name, id, detail string, failed bool) {
		add("tool_use", name, id, detail, false)
		add("tool_result", name, id, "stored", failed)
	}
	pair("TodoWrite", "a", `{"todos":[{"status":"in_progress","content":"核对组织资产台账"}]}`, false)
	for i := 2; i <= 30; i++ {
		pair("Read", fmt.Sprint(i), `{"file_path":"inventory.csv"}`, false)
	}
	pair("TodoWrite", "discarded", `{"todos":[{"status":"in_progress","content":"应忽略的失败更新"}]}`, true)
	pair("Read", "32", "{}", false)
	pair("TodoWrite", "b", `{"todos":[{"status":"completed","content":"核对组织资产台账"},{"status":"in_progress","content":"复核导出数据的差异"}]}`, false)
	pair("Read", "34", "{}", false)
	add("text", "", "", "当前任务：整理核查结论", false)
	add("thinking", "", "", "当前任务：不可展示的内部记录", false)
	pair("Read", "35", "{}", false)
	read := func(from int) *ai.ConversationTimeline {
		t.Helper()
		v, err := store.ReadConversationTimeline(ctx, owner, conv, run, "", from, 0)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	first := read(1)
	if len(first.Sections) != 3 || first.Sections[0].First != 1 || first.Sections[0].Last != 32 || first.Sections[0].Purpose.Text != "核对组织资产台账" || first.Next != 25 {
		t.Fatalf("incorrect first task window: %+v", first.Sections)
	}
	if first.Sections[1].Purpose.Text != "复核导出数据的差异" || first.Sections[2].Purpose.Source != "statement" || first.Sections[2].Purpose.Text != "整理核查结论" {
		t.Fatal("incorrect task transition", first.Sections)
	}
	if v := read(25); v.Previous != 1 || v.Next != 33 || len(v.Steps) != 8 {
		t.Fatal("wrong intra-task page", v.Previous, v.Next)
	}
	if v := read(33); v.Previous != 25 || v.Next != 35 || v.Sections[1].Purpose.Basis[0].ActivityID != v.Steps[0].ActivityID {
		t.Fatal("task source or cross-task paging is incorrect")
	}
	if v := read(35); v.Previous != 33 || v.Next != 0 || v.Counts.Steps != 35 {
		t.Fatal("incomplete run")
	}
}

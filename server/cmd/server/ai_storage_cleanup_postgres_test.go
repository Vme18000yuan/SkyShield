package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/aicleanup"
	"github.com/yyhuni/lunafox/server/internal/aitraffic"
	serverdatabase "github.com/yyhuni/lunafox/server/internal/database"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestAIStorageCleanupOnIsolatedPostgres(t *testing.T) {
	dsn := os.Getenv("SKYSHIELD_CLEANUP_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated cleanup database not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var name string
	if err = db.QueryRow(`SELECT current_database()`).Scan(&name); err != nil || !strings.HasPrefix(name, "skyshield_cleanup_test_") {
		t.Fatal("refusing non-test database", name, err)
	}
	if countPublicTables(t, context.Background(), db, false) != 0 {
		t.Fatal("test database must be empty")
	}
	previousFS, previousPath := serverdatabase.MigrationsFS, serverdatabase.MigrationsPath
	serverdatabase.MigrationsFS, serverdatabase.MigrationsPath = migrationsFS, "migrations"
	defer func() { serverdatabase.MigrationsFS, serverdatabase.MigrationsPath = previousFS, previousPath }()
	if err = serverdatabase.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var owner, other int
	for _, item := range []struct {
		name string
		id   *int
	}{{"cleanup-owner", &owner}, {"cleanup-other", &other}} {
		if err = db.QueryRow(`INSERT INTO auth_user(username,password,is_active,is_superuser) VALUES($1,'unused',TRUE,TRUE) RETURNING id`, item.name).Scan(item.id); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO tenant_membership(tenant_id,user_id,role) VALUES($1,$2,'admin') ON CONFLICT(tenant_id,user_id) DO UPDATE SET role='admin'`, assets.DefaultTenantID, *item.id); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	service := aicleanup.New(orm, root, nil)
	ownerRoot := filepath.Join(root, assets.DefaultTenantID, fmt.Sprintf("user-%d", owner))
	write := func(path string) {
		t.Helper()
		full := filepath.Join(ownerRoot, path)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("private fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	exists := func(path string, want bool) {
		t.Helper()
		_, err := os.Stat(filepath.Join(ownerRoot, path))
		if (err == nil) != want {
			t.Fatalf("path %s exists=%v want %v", path, err == nil, want)
		}
	}
	conversations := airepo.NewConversationStore(orm)
	conversation, run, instance, gather := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = conversations.CreateConversation(ctx, owner, conversation, "cleanup"); err != nil {
		t.Fatal(err)
	}
	if err = conversations.BeginRun(ctx, owner, conversation, run, instance, "fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.DeleteScope(ctx, other, "conversation", conversation); !errors.Is(err, aicleanup.ErrNotFound) {
		t.Fatalf("other owner: %v", err)
	}
	if _, err = service.DeleteScope(ctx, owner, "conversation", conversation); !errors.Is(err, aicleanup.ErrBusy) {
		t.Fatalf("running deletion: %v", err)
	}
	if err = conversations.FinishRun(ctx, owner, conversation, run, instance, "completed"); err != nil {
		t.Fatal(err)
	}
	var firstCall, secondCall int64
	for _, pair := range []struct {
		kind string
		id   *int64
	}{{"tool_use", &firstCall}, {"tool_result", nil}, {"tool_use", &secondCall}, {"tool_result", nil}} {
		var id int64
		if err = db.QueryRow(`INSERT INTO ai_conversation_activity(tenant_id,owner_user_id,conversation_id,run_id,kind,tool,tool_use_id,detail) VALUES($1,$2,$3,$4,$5,'fixture','reused-id','large fixture output') RETURNING id`, assets.DefaultTenantID, owner, conversation, run, pair.kind).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if pair.id != nil {
			*pair.id = id
		}
	}
	if _, err = service.DeleteExecutions(ctx, other, "conversation", []string{fmt.Sprint(firstCall)}); !errors.Is(err, aicleanup.ErrNotFound) {
		t.Fatal("foreign execution deletion", err)
	}
	if _, err = service.DeleteExecutions(ctx, owner, "conversation", []string{fmt.Sprint(firstCall)}); err != nil {
		t.Fatal(err)
	}
	var paired int
	if err = db.QueryRow(`SELECT count(*) FROM ai_conversation_activity WHERE conversation_id=$1 AND tool_use_id='reused-id'`, conversation).Scan(&paired); err != nil || paired != 2 {
		t.Fatal("wrong repeated tool pair removed", paired, err)
	}
	var tracedCall int64
	for _, item := range []struct{ kind, trace, detail string }{{"tool_use", "trace-a", "input-a"}, {"tool_use", "trace-b", "input-b"}, {"tool_result", "trace-a", "output-a"}, {"tool_result", "trace-b", "output-b"}} {
		var id int64
		if err = db.QueryRow(`INSERT INTO ai_conversation_activity(tenant_id,owner_user_id,conversation_id,run_id,kind,tool,tool_use_id,trace_call_id,detail) VALUES($1,$2,$3,$4,$5,'fixture','overlapping-id',$6,$7) RETURNING id`, assets.DefaultTenantID, owner, conversation, run, item.kind, item.trace, item.detail).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if item.kind == "tool_use" && item.trace == "trace-a" {
			tracedCall = id
		}
	}
	view, err := conversations.ListToolExecutions(ctx, owner, "", tracedCall, 1, true)
	if err != nil || len(view) != 1 || view[0].Output != "output-a" {
		t.Fatal("overlapping execution attribution", view, err)
	}
	if _, err = service.DeleteExecutions(ctx, owner, "conversation", []string{fmt.Sprint(tracedCall)}); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM ai_conversation_activity WHERE run_id=$1 AND trace_call_id='trace-b'`, run).Scan(&paired); err != nil || paired != 2 {
		t.Fatal("other traced execution removed", paired, err)
	}
	if _, err = db.Exec(`INSERT INTO ai_info_gather_run(id,tenant_id,owner_user_id,execution_id,tool_use_id,target,target_kind,mode,status) VALUES($1,$2,$3,$4,'fixture','example.test','HOST','normal','partial')`, gather, assets.DefaultTenantID, owner, run); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO ai_llm_record(id,tenant_id,owner_user_id,conversation_id,status,sealed_payload) VALUES($1,$2,$3,$4,'ok','fixture')`, uuid.NewString(), assets.DefaultTenantID, owner, conversation); err != nil {
		t.Fatal(err)
	}
	paths := []string{"transcripts/" + conversation + ".jsonl", "transcripts/" + conversation + "/subagents/agent-child.jsonl", "work/sessions/" + conversation + "/.task-output/out", "work/noa/" + conversation + "/archive", "work/info-gather/" + gather + "/report", "traffic/conversation-" + conversation + "/body"}
	for _, path := range paths {
		write(path)
	}
	write("transcripts/unrelated.jsonl")
	out, err := service.DeleteScope(ctx, owner, "conversation", conversation)
	if err != nil || out.CleanupPending {
		t.Fatalf("conversation cleanup %+v %v", out, err)
	}
	for _, path := range paths {
		exists(path, false)
	}
	exists("transcripts/unrelated.jsonl", true)
	for _, query := range []string{`SELECT count(*) FROM ai_run WHERE conversation_id=$1`, `SELECT count(*) FROM ai_llm_record WHERE conversation_id=$1`, `SELECT count(*) FROM ai_conversation_activity WHERE conversation_id=$1`} {
		var n int
		if err = db.QueryRow(query, conversation).Scan(&n); err != nil || n != 0 {
			t.Fatal(query, n, err)
		}
	}
	var n int
	if err = db.QueryRow(`SELECT count(*) FROM ai_info_gather_run WHERE id=$1`, gather).Scan(&n); err != nil || n != 0 {
		t.Fatal("gather retained", n, err)
	}

	tasks := aiapp.NewTaskFacade(airepo.NewTaskStore(orm), nil, nil)
	task, err := tasks.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "cleanup", Goal: "fixture", WorkerCount: 1, TimeoutSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	var target int
	if err = db.QueryRow(`INSERT INTO target(name,type) VALUES('keep.example.test','domain') RETURNING id`).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO subdomain(target_id,dns_name) VALUES($1,'app.keep.example.test')`, target); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO vulnerability(target_id,url,vuln_type,severity,source) VALUES($1,'https://app.keep.example.test/','fixture','info','ai')`, target); err != nil {
		t.Fatal(err)
	}
	var assetsBefore int
	if err = db.QueryRow(`SELECT count(*) FROM asset_entity`).Scan(&assetsBefore); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`SELECT set_config('tianqiong.task_id',$1,TRUE),set_config('tianqiong.owner_user_id',$2,TRUE),set_config('tianqiong.execution_id',$3,TRUE),set_config('tianqiong.execution_mode','setup',TRUE)`, fmt.Sprint(task.ID), fmt.Sprint(owner), task.ActiveRun); err != nil {
		t.Fatal(err)
	}
	var intent, findingNode, finding, toolCall int64
	if err = tx.QueryRow(`INSERT INTO tianqiong_ai.exploration_nodes(exploration_id,kind,state) VALUES($1,'intent','done') RETURNING id`, task.ExplorationID).Scan(&intent); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(`INSERT INTO tianqiong_ai.exploration_nodes(exploration_id,kind,state) VALUES($1,'finding','confirmed') RETURNING id`, task.ExplorationID).Scan(&findingNode); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO tianqiong_ai.exploration_edges(exploration_id,src_id,dst_id,rel) VALUES($1,$2,$3,'yields')`, task.ExplorationID, intent, findingNode); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO tianqiong_ai.task_constraints(exploration_id,kind,text) VALUES($1,'deny','fixture constraint')`, task.ExplorationID); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(`INSERT INTO tianqiong_ai.findings(task_id,node_id,name) VALUES($1,$2,'registered fixture') RETURNING id`, task.ID, findingNode).Scan(&finding); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO tianqiong_ai.exploration_anchors(node_id,asset_id) SELECT $1,id FROM asset_entity WHERE tenant_id=$2 AND asset_type='subdomain' LIMIT 1`, findingNode, assets.DefaultTenantID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO ai_finding_risk_link(finding_id,task_id,vulnerability_id) SELECT $1,$2,id FROM vulnerability WHERE target_id=$3`, finding, task.ID, target); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(`INSERT INTO tianqiong_ai.activity(exploration_id,node_id,worker,kind,tool,tool_use_id,detail) VALUES($1,$2,'worker','tool_use','fixture','task-call','input') RETURNING id`, task.ExplorationID, intent).Scan(&toolCall); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO tianqiong_ai.activity(exploration_id,node_id,worker,kind,tool,tool_use_id,detail) VALUES($1,$2,'worker','tool_result','fixture','task-call','output')`, task.ExplorationID, intent); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// A guessed cleanup job ID must not grant another actor graph write access.
	guard, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	guardID := uuid.NewString()
	if _, err = guard.Exec(`INSERT INTO ai_storage_cleanup(id,tenant_id,owner_user_id,kind,scope_id,exploration_id,payload) VALUES($1,$2,$3,'task_tools',$4,$5,jsonb_build_object('activity_ids',jsonb_build_array($6::bigint)))`, guardID, assets.DefaultTenantID, owner, fmt.Sprint(task.ID), task.ExplorationID, toolCall); err != nil {
		t.Fatal(err)
	}
	if _, err = guard.Exec(`SELECT set_config('tianqiong.execution_mode','storage_cleanup',TRUE),set_config('tianqiong.storage_cleanup_id',$1,TRUE),set_config('tianqiong.owner_user_id',$2,TRUE)`, guardID, fmt.Sprint(other)); err != nil {
		t.Fatal(err)
	}
	if _, err = guard.Exec(`DELETE FROM tianqiong_ai.activity WHERE id=$1`, toolCall); err == nil {
		t.Fatal("foreign cleanup capability accepted")
	}
	_ = guard.Rollback()
	if _, err = service.DeleteExecutions(ctx, owner, "task", []string{fmt.Sprint(toolCall)}); err != nil {
		t.Fatal("task execution cleanup", err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM tianqiong_ai.activity WHERE exploration_id=$1 AND tool_use_id='task-call'`, task.ExplorationID).Scan(&paired); err != nil || paired != 0 {
		t.Fatal("task output retained", paired, err)
	}
	taskPaths := []string{fmt.Sprintf("work/tasks/%d/cmd-output/out", task.ID), fmt.Sprintf("transcripts/exp%d-main.jsonl", task.ExplorationID), fmt.Sprintf("transcripts/exp%d-worker-i9/subagents/a.jsonl", task.ExplorationID), fmt.Sprintf("work/noa/exp%d-planner/a", task.ExplorationID), fmt.Sprintf("traffic/task-%d/body", task.ID)}
	for _, path := range taskPaths {
		write(path)
	}
	otherPath := fmt.Sprintf("transcripts/exp%d-main.jsonl", task.ExplorationID*10)
	write(otherPath)
	var waitingJob int64
	if err = db.QueryRow(`INSERT INTO ai_agent_trigger_job(tenant_id,agent_key,owner_user_id,source_task_id,event_key,title,message) VALUES($1,'reporter',$2,$3,$4,'waiting','fixture') RETURNING id`, assets.DefaultTenantID, owner, task.ID, uuid.NewString()).Scan(&waitingJob); err != nil {
		t.Fatal(err)
	}
	autoChat := uuid.NewString()
	if _, err = conversations.CreateConversation(ctx, owner, autoChat, "automatic task report"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO ai_agent_trigger_job(tenant_id,agent_key,owner_user_id,source_task_id,event_key,title,message,conversation_id,status) VALUES($1,'reporter',$2,$3,$4,'completed','fixture',$5,'completed')`, assets.DefaultTenantID, owner, task.ID, uuid.NewString(), autoChat); err != nil {
		t.Fatal(err)
	}
	write("transcripts/" + autoChat + ".jsonl")
	write("work/sessions/" + autoChat + "/output")
	if _, err = service.DeleteScope(ctx, other, "task", fmt.Sprint(task.ID)); !errors.Is(err, aicleanup.ErrNotFound) {
		t.Fatal("foreign task deletion", err)
	}
	service.SetActiveTaskCheck(func(id int64) bool { return id == task.ID })
	if _, err = service.DeleteScope(ctx, owner, "task", fmt.Sprint(task.ID)); !errors.Is(err, aicleanup.ErrBusy) {
		t.Fatal("draining executor was deleted", err)
	}
	service.SetActiveTaskCheck(nil)
	out, err = service.DeleteScope(ctx, owner, "task", fmt.Sprint(task.ID))
	if err != nil || out.CleanupPending {
		t.Fatalf("task cleanup %+v %v", out, err)
	}
	for _, path := range taskPaths {
		exists(path, false)
	}
	exists(otherPath, true)
	exists("transcripts/"+autoChat+".jsonl", false)
	exists("work/sessions/"+autoChat+"/output", false)
	var jobState string
	if err = db.QueryRow(`SELECT status FROM ai_agent_trigger_job WHERE id=$1`, waitingJob).Scan(&jobState); err != nil || jobState != "cancelled" {
		t.Fatal("queued job survived source deletion", jobState, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM asset_entity`).Scan(&n); err != nil || n != assetsBefore {
		t.Fatal("assets changed", n, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM vulnerability WHERE target_id=$1`, target).Scan(&n); err != nil || n != 1 {
		t.Fatal("vulnerability removed", n, err)
	}

	// A registered vulnerability keeps its report/evidence after its chat is gone.
	protectedChat, protectedRun := uuid.NewString(), uuid.NewString()
	if _, err = conversations.CreateConversation(ctx, owner, protectedChat, "protected report"); err != nil {
		t.Fatal(err)
	}
	if err = conversations.BeginRun(ctx, owner, protectedChat, protectedRun, instance, "fixture"); err != nil {
		t.Fatal(err)
	}
	if err = conversations.FinishRun(ctx, owner, protectedChat, protectedRun, instance, "completed"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO ai_conversation_vulnerability_report(tenant_id,owner_user_id,conversation_id,run_id,tool_use_id,input_hash,vulnerability_id,report,evidence) SELECT $1,$2,$3,$4,'fixture',$5,id,'{"summary":"keep report"}','[{"text":"saved evidence"}]' FROM vulnerability WHERE target_id=$6`, assets.DefaultTenantID, owner, protectedChat, protectedRun, strings.Repeat("a", 64), target); err != nil {
		t.Fatal(err)
	}
	if _, err = service.DeleteScope(ctx, owner, "conversation", protectedChat); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM ai_conversation_vulnerability_report WHERE conversation_id IS NULL AND run_id IS NULL AND evidence->0->>'text'='saved evidence'`).Scan(&n); err != nil || n != 1 {
		t.Fatal("registered report lost", n, err)
	}

	// A filesystem failure is durable and resumes in a new service instance.
	retryChat := uuid.NewString()
	if _, err = conversations.CreateConversation(ctx, other, retryChat, "retry"); err != nil {
		t.Fatal(err)
	}
	otherRoot := filepath.Join(root, assets.DefaultTenantID, fmt.Sprintf("user-%d", other))
	if err = os.MkdirAll(otherRoot, 0700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err = os.WriteFile(filepath.Join(outside, "keep"), []byte("protected"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(otherRoot, "work")); err != nil {
		t.Fatal(err)
	}
	out, err = service.DeleteScope(ctx, other, "conversation", retryChat)
	if err != nil || !out.CleanupPending {
		t.Fatal("missing deferred cleanup", out, err)
	}
	if _, err = os.Stat(filepath.Join(outside, "keep")); err != nil {
		t.Fatal("symlink escaped", err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM ai_storage_cleanup WHERE owner_user_id=$1 AND attempts>0`, other).Scan(&n); err != nil || n != 1 {
		t.Fatal("cleanup not persisted", n, err)
	}
	if err = os.Remove(filepath.Join(otherRoot, "work")); err != nil {
		t.Fatal(err)
	}
	resumedPath := filepath.Join(otherRoot, "work", "sessions", retryChat)
	if err = os.MkdirAll(resumedPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(resumedPath, "output"), []byte("remove"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE ai_storage_cleanup SET next_attempt_at=clock_timestamp() WHERE owner_user_id=$1`, other); err != nil {
		t.Fatal(err)
	}
	restarted := aicleanup.New(orm, root, nil)
	cleanupContext, stopCleanup := context.WithCancel(ctx)
	restarted.Start(cleanupContext)
	defer func() { stopCleanup(); <-restarted.Done() }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err = db.QueryRow(`SELECT count(*) FROM ai_storage_cleanup WHERE owner_user_id=$1`, other).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cleanup retry did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err = os.Stat(resumedPath); !os.IsNotExist(err) {
		t.Fatal("retry retained files", err)
	}

	// Exercise the same recorder used by the UI, including ownership and clear.
	trafficChat := uuid.NewString()
	if _, err = conversations.CreateConversation(ctx, owner, trafficChat, "traffic"); err != nil {
		t.Fatal(err)
	}
	manager, err := aitraffic.NewManager(root, func(*http.Request) (*url.URL, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	lease, err := manager.Acquire(ctx, aitraffic.Scope{TenantID: assets.DefaultTenantID, UserID: owner, ConversationID: trafficChat})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"keep", "remove"} {
		if _, err = lease.Recorder().DB().Exec(`INSERT INTO exchanges(id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path) VALUES(?,1,'fixture.test','GET','/','http://fixture.test/',200,'text/plain',0,3,'')`, id); err != nil {
			t.Fatal(err)
		}
		if _, err = lease.Recorder().DB().Exec(`INSERT INTO exchange_bodies(id,resp_body) VALUES(?,?)`, id, id); err != nil {
			t.Fatal(err)
		}
	}
	lease.Close()
	trafficService := aicleanup.New(orm, root, manager)
	if _, err = trafficService.DeleteTraffic(ctx, other, "conversation", trafficChat, []string{"remove"}, false); !errors.Is(err, aicleanup.ErrNotFound) {
		t.Fatal("foreign traffic delete", err)
	}
	out, err = trafficService.DeleteTraffic(ctx, owner, "conversation", trafficChat, []string{"remove"}, false)
	if err != nil || out.Deleted != 1 {
		t.Fatal("traffic row deletion", out, err)
	}
	out, err = trafficService.DeleteTraffic(ctx, owner, "conversation", trafficChat, nil, true)
	if err != nil || out.Deleted != 1 {
		t.Fatal("traffic clear", out, err)
	}
	if _, err = conversations.ReadConversationHistory(ctx, owner, trafficChat, 0, 10); err != nil {
		t.Fatal("traffic deletion removed conversation", err)
	}
	out, err = trafficService.DeleteScope(ctx, owner, "conversation", trafficChat)
	if err != nil || out.CleanupPending {
		t.Fatal("traffic directory cleanup", out, err)
	}
	exists("traffic/conversation-"+trafficChat, false)
}

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	serverdatabase "github.com/yyhuni/lunafox/server/internal/database"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	notificationapp "github.com/yyhuni/lunafox/server/internal/modules/notification/application"
	notificationrepo "github.com/yyhuni/lunafox/server/internal/modules/notification/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestARTEXFindingNotificationsIsolatedPostgres(t *testing.T) {
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
	owner, target, asset := taskFixtureScope(t, ctx, db, "finding-notification.example")
	var other int
	if err = db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE auth_user SET locale='zh' WHERE id=$1`, owner); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := airepo.NewTaskStore(orm)
	facade := aiapp.NewTaskFacade(store, nil, nil)
	task, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: "通知测试任务", Goal: "验证通知", TargetID: target, AssetIDs: []int64{asset}})
	if err != nil {
		t.Fatal(err)
	}
	run := uuid.NewString()
	if _, err = store.ClaimMainChat(ctx, owner, task.ID, run); err != nil {
		t.Fatal(err)
	}
	connector, err := airuntime.NewTaskGraphConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := connector.OpenForExecution(ctx, airuntime.TaskExecution{ID: task.ID, ExplorationID: task.ExplorationID, TenantID: task.TenantID, UserID: owner, RunID: run, TargetID: target, ExecutionMode: "main_chat"})
	if err != nil {
		t.Fatal(err)
	}
	graph := (&artexdb.DB{DB: connection}).Exploration(task.ExplorationID)
	intent, err := graph.AddIntent(map[string]any{"summary": "检查"}, 5, []int64{asset}, "mainagent")
	if err != nil {
		t.Fatal(err)
	}
	input := artexdb.RecordFindingInput{TaskID: task.ID, IntentID: intent, VulnClass: "fixture", Name: "测试发现", Severity: "high", Summary: "summary-secret", Evidence: "evidence-secret", Worker: "mainagent", AssetIDs: []int64{asset}}
	finding, err := graph.RecordFinding(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	var created int
	var payload string
	if err = db.QueryRowContext(ctx, `SELECT count(*),COALESCE(max(payload::text),'') FROM notification_outbox WHERE kind='ai-finding-created' AND payload->>'findingId'=$1`, fmt.Sprint(finding.FindingID)).Scan(&created, &payload); err != nil || created != 1 || strings.Contains(payload, "secret") {
		t.Fatalf("creation notification count=%d payload=%s err=%v", created, payload, err)
	}
	var outbound int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_notification_events WHERE finding_id=$1`, finding.FindingID).Scan(&outbound); err != nil || outbound != 0 {
		t.Fatalf("outbound snapshot created without an enabled channel: %d %v", outbound, err)
	}
	// The original ARTEX writer and the host trigger must not enqueue twice.
	if _, err = db.ExecContext(ctx, `ALTER TABLE notification_outbox ADD CONSTRAINT fixture_block_ai_creation CHECK(kind<>'ai-finding-created') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	input.Name, input.VulnClass = "通知故障时仍保存", "failure-fixture"
	if _, err = graph.RecordFinding(ctx, input); err != nil {
		t.Fatalf("notification failure rolled back finding: %v", err)
	}
	if _, err = db.ExecContext(ctx, `ALTER TABLE notification_outbox DROP CONSTRAINT fixture_block_ai_creation`); err != nil {
		t.Fatal(err)
	}
	connection.Close()
	if err = store.ReleaseMainChat(ctx, owner, task.ID, run); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"pending", "confirmed", "confirmed", "fixed"} {
		if err = facade.SetFindingStatus(ctx, owner, task.ID, finding.FindingID, state); err != nil {
			t.Fatal(err)
		}
	}
	var changes int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM notification_outbox WHERE kind='ai-finding-status-changed' AND payload->>'findingId'=$1`, fmt.Sprint(finding.FindingID)).Scan(&changes); err != nil || changes != 2 {
		t.Fatalf("status notification count=%d err=%v", changes, err)
	}
	hub := notificationapp.NewRealtimeHub()
	ownerHints, unsubscribeOwner := hub.Subscribe(owner)
	defer unsubscribeOwner()
	otherHints, unsubscribeOther := hub.Subscribe(other)
	defer unsubscribeOther()
	outbox := notificationrepo.NewOutboxRepository(orm)
	inbox := notificationrepo.NewInboxRepository(orm)
	materializer := notificationapp.NewMaterializer(notificationrepo.NewTransactionCoordinator(orm), notificationrepo.NewFactRepository(orm), notificationrepo.NewAudienceRepository(orm), notificationrepo.NewDestinationRepository(orm), notificationapp.NewTemplates(), hub)
	worker := notificationapp.NewOutboxWorker(outbox, materializer, notificationapp.OutboxWorkerOptions{Owner: "ai-finding-fixture"})
	if err = worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ownerHints:
	case <-time.After(time.Second):
		t.Fatal("owner did not receive notification refresh")
	}
	select {
	case <-otherHints:
		t.Fatal("other user received AI finding refresh")
	default:
	}
	page, err := inbox.List(ctx, owner, 25, "", time.Now())
	if err != nil || page.TotalSize != 3 || len(page.Results) != 3 {
		t.Fatalf("owner AI inbox: %+v %v", page, err)
	}
	otherPage, err := inbox.List(ctx, other, 25, "", time.Now())
	if err != nil || otherPage.TotalSize != 0 {
		t.Fatalf("foreign AI inbox: %+v %v", otherPage, err)
	}
	var deliveries int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM notification_delivery d JOIN notification_fact f ON f.id=d.fact_id WHERE f.kind IN ('ai-finding-created','ai-finding-status-changed')`).Scan(&deliveries); err != nil || deliveries != 0 {
		t.Fatalf("AI events sent to installation destinations: %d %v", deliveries, err)
	}
	if !strings.Contains(page.Results[0].Message, "通知测试任务") || strings.Contains(page.Results[0].Message, "secret") {
		t.Fatalf("AI notification snapshot: %q", page.Results[0].Message)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_task_archive(task_id,state) VALUES($1,'archiving')`, task.ID); err != nil {
		t.Fatal(err)
	}
	if page, err = inbox.List(ctx, owner, 25, "", time.Now()); err != nil || page.TotalSize != 0 {
		t.Fatalf("archived task inbox visible: %+v %v", page, err)
	}
	if err = inbox.MarkAllRead(ctx, owner, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM ai_task_archive WHERE task_id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if count, e := inbox.UnreadCount(ctx, owner, time.Now()); e != nil || count != 3 {
		t.Fatalf("hidden notifications were marked read: %d %v", count, e)
	}
	if err = facade.SetFindingStatus(ctx, owner, task.ID, finding.FindingID, "ignored"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE target SET deleted_at=now() WHERE id=$1`, target); err != nil {
		t.Fatal(err)
	}
	if err = worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if page, err = inbox.List(ctx, owner, 25, "", time.Now()); err != nil || page.TotalSize != 0 {
		t.Fatalf("deleted target inbox visible: %+v %v", page, err)
	}
	if count, e := inbox.UnreadCount(ctx, owner, time.Now()); e != nil || count != 0 {
		t.Fatalf("deleted target unread count=%d err=%v", count, e)
	}
	if _, err = inbox.Get(ctx, owner, pageFactID(t, db, finding.FindingID), time.Now()); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("deleted target notification detail visible: %v", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE target SET deleted_at=NULL WHERE id=$1`, target); err != nil {
		t.Fatal(err)
	}
	if page, err = inbox.List(ctx, owner, 25, "", time.Now()); err != nil || page.TotalSize != 3 {
		t.Fatalf("notification created after target removal gained an audience: %+v %v", page, err)
	}
}

func pageFactID(t *testing.T, db *sql.DB, findingID int64) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`SELECT id FROM notification_fact WHERE kind='ai-finding-created' AND payload->>'findingId'=$1`, fmt.Sprint(findingID)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

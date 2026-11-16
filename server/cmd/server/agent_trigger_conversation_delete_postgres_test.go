package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/auth"
	serverdatabase "github.com/yyhuni/lunafox/server/internal/database"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	settingsrepo "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	aihandler "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/handler"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	airouter "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/router"
	registry "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestAgentTriggerConversationDeletionIsolatedPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv(emptyPostgresMigrationTestDSNEnv))
	if dsn == "" {
		t.Skip("isolated PostgreSQL DSN is not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
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
	var owner, other int
	if err = db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='admin'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `INSERT INTO auth_user(username,password,is_superuser) VALUES('trigger-delete-other','unused',FALSE) RETURNING id`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = settingsrepo.NewAgentCatalogStore(orm).SeedDefaultAgents(ctx); err != nil {
		t.Fatal(err)
	}
	jobs := airepo.NewAgentTriggerStore(orm)
	conversations := airepo.NewConversationStore(orm)
	jwt := auth.NewJWTManager("trigger-delete-fixture", time.Minute, time.Hour)
	ownerToken, _, err := jwt.GenerateAccessToken(owner, "admin", 0)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, _, err := jwt.GenerateAccessToken(other, "trigger-delete-other", 0)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/v1")
	group.Use(middleware.AuthMiddleware(jwt, fixtureTokenVersions{}))
	airouter.RegisterAIWorkbenchRoutes(group, aihandler.NewConversationHandler(aiapp.NewConversationFacade(conversations, nil, nil, nil)))
	deleteHTTP := func(id, token string) []aiapp.ConversationDeleteItem {
		t.Helper()
		request := httptest.NewRequest(http.MethodDelete, "/v1/aiConversations/"+id, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("delete HTTP status=%d body=%s", response.Code, response.Body.String())
		}
		var result struct {
			Items []aiapp.ConversationDeleteItem `json:"items"`
		}
		if e := json.Unmarshal(response.Body.Bytes(), &result); e != nil {
			t.Fatal(e)
		}
		return result.Items
	}
	deleteBatchHTTP := func(ids []string, token string) []aiapp.ConversationDeleteItem {
		t.Helper()
		body, e := json.Marshal(map[string][]string{"ids": ids})
		if e != nil {
			t.Fatal(e)
		}
		request := httptest.NewRequest(http.MethodPost, "/v1/aiConversationDeletions", bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("batch delete HTTP status=%d body=%s", response.Code, response.Body.String())
		}
		var result struct {
			Items []aiapp.ConversationDeleteItem `json:"items"`
		}
		if e = json.Unmarshal(response.Body.Bytes(), &result); e != nil {
			t.Fatal(e)
		}
		return result.Items
	}
	insertJob := func(user int, key, status string, conversation *string) int64 {
		t.Helper()
		var id int64
		if e := db.QueryRowContext(ctx, `INSERT INTO ai_agent_trigger_job(tenant_id,agent_key,owner_user_id,event_key,title,message,status,conversation_id) VALUES($1,'reporter',$2,$3,'fixture','fixture',$4,$5) RETURNING id`, registry.DefaultTenantID, user, key, status, conversation).Scan(&id); e != nil {
			t.Fatal(e)
		}
		return id
	}
	jobID := insertJob(owner, "fixture/event/waiting", "queued", nil)
	claimed, err := jobs.ClaimAgentJob(ctx)
	if err != nil || claimed == nil || claimed.ID != jobID {
		t.Fatalf("claim waiting job: %+v %v", claimed, err)
	}
	if err = jobs.ReleaseAgentJob(ctx, jobID, true, "waiting for model"); err != nil {
		t.Fatal(err)
	}
	if items := deleteHTTP(claimed.ConversationID, otherToken); len(items) != 1 || items[0].OK {
		t.Fatalf("other user deleted conversation: %+v", items)
	}
	var state string
	if err = db.QueryRowContext(ctx, `SELECT status FROM ai_agent_trigger_job WHERE id=$1`, jobID).Scan(&state); err != nil || state != "queued" {
		t.Fatalf("other user changed job: %q %v", state, err)
	}
	if items := deleteHTTP(claimed.ConversationID, ownerToken); len(items) != 1 || !items[0].OK {
		t.Fatalf("delete waiting conversation: %+v", items)
	}
	var linked sql.NullString
	if err = db.QueryRowContext(ctx, `SELECT status,conversation_id::text FROM ai_agent_trigger_job WHERE id=$1`, jobID).Scan(&state, &linked); err != nil || state != "cancelled" || linked.Valid {
		t.Fatalf("waiting job not cancelled: state=%q linked=%v err=%v", state, linked.Valid, err)
	}
	if next, e := jobs.ClaimAgentJob(ctx); e != nil || next != nil {
		t.Fatalf("deleted conversation was queued again: %+v %v", next, e)
	}
	var remaining int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_conversation WHERE id=$1`, claimed.ConversationID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("deleted conversation reappeared: %d %v", remaining, err)
	}

	var ids []string
	jobIDs := map[string]int64{}
	var runningID string
	for _, status := range []string{"queued", "starting", "running", "completed"} {
		id := uuid.NewString()
		if _, e := conversations.CreateAgentConversation(ctx, owner, id, "fixture", "reporter"); e != nil {
			t.Fatal(e)
		}
		ids = append(ids, id)
		jobIDs[status] = insertJob(owner, "fixture/"+status, status, &id)
		if status == "running" {
			runningID = uuid.NewString()
			if e := conversations.BeginRun(ctx, owner, id, runningID, uuid.NewString(), "fixture"); e != nil {
				t.Fatal(e)
			}
			if _, e := db.ExecContext(ctx, `UPDATE ai_agent_trigger_job SET run_id=$1 WHERE id=$2`, runningID, jobIDs[status]); e != nil {
				t.Fatal(e)
			}
		}
	}
	otherID := uuid.NewString()
	if _, err = conversations.CreateAgentConversation(ctx, other, otherID, "other", "reporter"); err != nil {
		t.Fatal(err)
	}
	foreignJob := insertJob(other, "fixture/other", "queued", &otherID)
	if items := deleteBatchHTTP(append(ids, otherID), ownerToken); len(items) != len(ids)+1 || items[len(items)-1].OK {
		t.Fatalf("batch ownership: %+v", items)
	} else {
		for _, item := range items[:len(ids)] {
			if !item.OK {
				t.Fatalf("batch failed to delete owned conversation: %+v", item)
			}
		}
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_run WHERE id=$1`, runningID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("deleted running conversation retained a run: %d %v", remaining, err)
	}
	for original, id := range jobIDs {
		want := "cancelled"
		if original == "completed" {
			want = "completed"
		}
		if err = db.QueryRowContext(ctx, `SELECT status,conversation_id::text FROM ai_agent_trigger_job WHERE id=$1`, id).Scan(&state, &linked); err != nil || state != want || linked.Valid {
			t.Fatalf("%s job: state=%q linked=%v err=%v", original, state, linked.Valid, err)
		}
	}
	if err = db.QueryRowContext(ctx, `SELECT status,conversation_id::text FROM ai_agent_trigger_job WHERE id=$1`, foreignJob).Scan(&state, &linked); err != nil || state != "queued" || !linked.Valid || linked.String != otherID {
		t.Fatalf("foreign job changed: state=%q linked=%v err=%v", state, linked.Valid, err)
	}

	// Deletion waits for the same dispatcher lock, preventing a claim from
	// inserting a replacement conversation during the deletion transaction.
	lock, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lock.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1,$2)`, 0x4147454e, 0x4a4f4253); err != nil {
		_ = lock.Rollback()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, e := conversations.DeleteOwnedConversations(ctx, other, []string{otherID})
		done <- e
	}()
	select {
	case e := <-done:
		_ = lock.Rollback()
		t.Fatalf("deletion bypassed dispatch lock: %v", e)
	case <-time.After(100 * time.Millisecond):
	}
	if err = lock.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(fmt.Errorf("deletion did not resume: %w", ctx.Err()))
	}
}

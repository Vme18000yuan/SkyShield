package main

import (
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

func TestAgentTriggerBackgroundConversationsIsolatedPostgres(t *testing.T) {
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
	if err = db.QueryRowContext(ctx, `INSERT INTO auth_user(username,password,is_superuser) VALUES('background-other','unused',FALSE) RETURNING id`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = settingsrepo.NewAgentCatalogStore(orm).SeedDefaultAgents(ctx); err != nil {
		t.Fatal(err)
	}
	conversations := airepo.NewConversationStore(orm)
	jobs := airepo.NewAgentTriggerStore(orm)
	createConversation := func(user int, agent string) string {
		t.Helper()
		id := uuid.NewString()
		if _, e := conversations.CreateAgentConversation(ctx, user, id, "fixture", agent); e != nil {
			t.Fatal(e)
		}
		return id
	}
	insertJob := func(user int, agent, key, status string, conversation *string) int64 {
		t.Helper()
		var id int64
		if e := db.QueryRowContext(ctx, `INSERT INTO ai_agent_trigger_job(tenant_id,agent_key,owner_user_id,event_key,title,message,status,conversation_id) VALUES($1,$2,$3,$4,'fixture','fixture',$5,$6) RETURNING id`, registry.DefaultTenantID, agent, user, key, status, conversation).Scan(&id); e != nil {
			t.Fatal(e)
		}
		return id
	}
	manual := createConversation(owner, "reporter")
	backgroundJob := insertJob(owner, "reporter", "501/event/1", "queued", nil)
	claimed, err := jobs.ClaimAgentJob(ctx)
	if err != nil || claimed == nil || claimed.ID != backgroundJob {
		t.Fatalf("claim automatic Reporter: %+v %v", claimed, err)
	}
	assertList := func(wantPresent, wantAbsent []string) {
		t.Helper()
		items, e := conversations.ListConversations(ctx, owner)
		if e != nil {
			t.Fatal(e)
		}
		seen := make(map[string]bool, len(items))
		for _, item := range items {
			seen[item.ID] = true
		}
		for _, id := range wantPresent {
			if !seen[id] {
				t.Fatalf("interactive conversation missing: %s", id)
			}
		}
		for _, id := range wantAbsent {
			if seen[id] {
				t.Fatalf("background or foreign conversation appeared: %s", id)
			}
		}
	}
	assertList([]string{manual}, []string{claimed.ConversationID})
	if agent, e := conversations.ConversationAgent(ctx, owner, claimed.ConversationID); e != nil || agent != "reporter" {
		t.Fatalf("background execution lost internal context: %q %v", agent, e)
	}
	run, instance := uuid.NewString(), uuid.NewString()
	if err = conversations.BeginRun(aiapp.WithAgentTriggerJob(ctx, claimed.ID), owner, claimed.ConversationID, run, instance, "fixture background report"); err != nil {
		t.Fatal(err)
	}
	assertList([]string{manual}, []string{claimed.ConversationID})
	if err = conversations.FinishRun(ctx, owner, claimed.ConversationID, run, instance, "completed"); err != nil {
		t.Fatal(err)
	}
	if err = jobs.ReconcileAgentJobs(ctx); err != nil {
		t.Fatal(err)
	}
	if page, e := conversations.ReadConversationHistory(ctx, owner, claimed.ConversationID, 0, 20); e != nil || len(page.Items) == 0 {
		t.Fatalf("background execution history lost: %+v %v", page, e)
	}
	assertList([]string{manual}, []string{claimed.ConversationID})

	review := createConversation(owner, "reporter")
	insertJob(owner, "reporter", "finding-case-review:"+review, "queued", &review)
	retest := createConversation(owner, "retester")
	insertJob(owner, "retester", "retest:100", "queued", &retest)
	otherAgent := createConversation(owner, "info_gatherer")
	insertJob(owner, "info_gatherer", "501/event/2", "queued", &otherAgent)
	foreign := createConversation(other, "reporter")
	interval := createConversation(owner, "reporter")
	insertJob(owner, "reporter", "501/interval/123456789", "queued", &interval)

	// Deleting a trigger nulls trigger_id on its jobs. Their event keys continue
	// to distinguish background work from an explicit user conversation.
	var triggerID int64
	if err = db.QueryRowContext(ctx, `INSERT INTO ai_agent_trigger(tenant_id,agent_key,owner_user_id,enabled,config) VALUES($1,'reporter',$2,FALSE,'{}') RETURNING id`, registry.DefaultTenantID, owner).Scan(&triggerID); err != nil {
		t.Fatal(err)
	}
	legacy := createConversation(owner, "reporter")
	var legacyJob int64
	if err = db.QueryRowContext(ctx, `INSERT INTO ai_agent_trigger_job(tenant_id,trigger_id,agent_key,owner_user_id,event_key,title,message,status,conversation_id) VALUES($1,$2,'reporter',$3,$4,'fixture','fixture','completed',$5) RETURNING id`, registry.DefaultTenantID, triggerID, owner, fmt.Sprintf("%d/event/9", triggerID), legacy).Scan(&legacyJob); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM ai_agent_trigger WHERE id=$1`, triggerID); err != nil {
		t.Fatal(err)
	}
	var trigger sql.NullInt64
	if err = db.QueryRowContext(ctx, `SELECT trigger_id FROM ai_agent_trigger_job WHERE id=$1`, legacyJob).Scan(&trigger); err != nil || trigger.Valid {
		t.Fatalf("legacy trigger cleanup: %+v %v", trigger, err)
	}
	assertList([]string{manual, review, retest, otherAgent}, []string{claimed.ConversationID, interval, legacy, foreign})

	// Background rows must be filtered before the chat list's LIMIT 200.
	if _, err = db.ExecContext(ctx, `UPDATE ai_conversation SET updated_at=CURRENT_TIMESTAMP-INTERVAL '1 year' WHERE id=ANY($1::uuid[])`, fmt.Sprintf("{%s,%s,%s,%s}", manual, review, retest, otherAgent)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_conversation(id,tenant_id,owner_user_id,title,agent_key) SELECT md5('background-list-'||i)::uuid,$1,$2,'background fixture','reporter' FROM generate_series(1,210) i`, registry.DefaultTenantID, owner); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_agent_trigger_job(tenant_id,agent_key,owner_user_id,event_key,title,message,status,conversation_id) SELECT $1,'reporter',$2,'700/event/'||i,'fixture','fixture','completed',md5('background-list-'||i)::uuid FROM generate_series(1,210) i`, registry.DefaultTenantID, owner); err != nil {
		t.Fatal(err)
	}
	assertList([]string{manual, review, retest, otherAgent}, []string{claimed.ConversationID, interval, legacy, foreign})

	jwt := auth.NewJWTManager("background-list-fixture", time.Minute, time.Hour)
	token, _, err := jwt.GenerateAccessToken(owner, "admin", 0)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/v1")
	group.Use(middleware.AuthMiddleware(jwt, fixtureTokenVersions{}))
	airouter.RegisterAIWorkbenchRoutes(group, aihandler.NewConversationHandler(aiapp.NewConversationFacade(conversations, nil, nil, nil)))
	request := httptest.NewRequest(http.MethodGet, "/v1/aiConversations", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list HTTP status=%d body=%s", response.Code, response.Body.String())
	}
	var result struct {
		Conversations []struct {
			ID string `json:"id"`
		} `json:"conversations"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, item := range result.Conversations {
		seen[item.ID] = true
	}
	if !seen[manual] || !seen[review] || !seen[retest] || !seen[otherAgent] || seen[claimed.ConversationID] || seen[interval] || seen[legacy] {
		t.Fatal("HTTP conversation list did not preserve the interactive/background boundary")
	}

	// If a user had already continued an automatically created conversation,
	// the interactive history remains discoverable rather than being hidden.
	manualRun, manualInstance := uuid.NewString(), uuid.NewString()
	if err = conversations.BeginRun(ctx, owner, claimed.ConversationID, manualRun, manualInstance, "manual follow-up"); err != nil {
		t.Fatal(err)
	}
	if err = conversations.FinishRun(ctx, owner, claimed.ConversationID, manualRun, manualInstance, "completed"); err != nil {
		t.Fatal(err)
	}
	assertList([]string{manual, review, retest, otherAgent, claimed.ConversationID}, []string{interval, legacy, foreign})
}

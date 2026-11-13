package main

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	serverdatabase "github.com/yyhuni/lunafox/server/internal/database"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestARTEXCredentialQueueIdleIsolatedPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv(emptyPostgresMigrationTestDSNEnv))
	if dsn == "" {
		t.Skip(emptyPostgresMigrationTestDSNEnv + " is not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var database string
	if err = db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&database); err != nil || !strings.HasPrefix(database, "lunafox_migration_test_") {
		t.Fatalf("refusing non-test database %q: %v", database, err)
	}
	if countPublicTables(t, ctx, db, false) != 0 {
		t.Fatal("test database is not empty")
	}
	previousFS, previousPath := serverdatabase.MigrationsFS, serverdatabase.MigrationsPath
	serverdatabase.MigrationsFS, serverdatabase.MigrationsPath = migrationsFS, "migrations"
	defer func() { serverdatabase.MigrationsFS, serverdatabase.MigrationsPath = previousFS, previousPath }()
	if err = serverdatabase.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	var userID int
	if err = db.QueryRowContext(ctx, `INSERT INTO auth_user(username,password,is_superuser) VALUES('credential-idle-admin','unused',TRUE) RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_custom_agent(tenant_id,agent_key,display_name,enabled,created_by,updated_by) VALUES('00000000-0000-0000-0000-000000000001','credential_processor','fixture',FALSE,$1,$1)`, userID); err != nil {
		t.Fatal(err)
	}
	// A statement trigger detects queue updates even when they match zero rows.
	// It exists only in the disposable test database.
	if _, err = db.ExecContext(ctx, `CREATE FUNCTION credential_queue_probe() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'credential queue statement reached'; END $$;
		CREATE TRIGGER credential_queue_probe BEFORE INSERT OR UPDATE ON ai_credential_job FOR EACH STATEMENT EXECUTE FUNCTION credential_queue_probe()`); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	jobs := airepo.NewCredentialJobStore(orm)
	claimIdle := func(stage string) {
		t.Helper()
		job, claimErr := jobs.Claim(ctx)
		if claimErr != nil || job != nil {
			t.Fatalf("%s: idle queue returned %+v, %v", stage, job, claimErr)
		}
	}
	claimIdle("no settings")
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_runtime_setting(tenant_id,options) VALUES('00000000-0000-0000-0000-000000000001','{"task_intelligence_enabled":true}')`); err != nil {
		t.Fatal(err)
	}
	claimIdle("only intelligence enabled")
	if _, err = db.ExecContext(ctx, `UPDATE ai_runtime_setting SET options='{"task_intelligence_enabled":true,"credential_agent_enabled":true}'`); err != nil {
		t.Fatal(err)
	}
	claimIdle("agent disabled")
	if _, err = db.ExecContext(ctx, `UPDATE ai_custom_agent SET enabled=TRUE WHERE agent_key='credential_processor'`); err != nil {
		t.Fatal(err)
	}
	if _, err = jobs.Claim(ctx); err == nil || !strings.Contains(err.Error(), "credential queue statement reached") {
		t.Fatalf("enabled processor did not reach queue: %v", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_runtime_setting SET options='{"task_intelligence_enabled":false,"credential_agent_enabled":true}'`); err != nil {
		t.Fatal(err)
	}
	claimIdle("intelligence disabled again")
	if _, err = db.ExecContext(ctx, `DROP TRIGGER credential_queue_probe ON ai_credential_job; DROP FUNCTION credential_queue_probe();
		UPDATE ai_runtime_setting SET options='{"task_intelligence_enabled":true,"credential_agent_enabled":true}'`); err != nil {
		t.Fatal(err)
	}
	claimIdle("enabled empty queue")
	canceled, stop := context.WithCancel(ctx)
	stop()
	if _, err = jobs.Claim(canceled); err == nil {
		t.Fatal("canceled settings check returned no error")
	}
}

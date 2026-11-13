package main

import (
	"context"
	"database/sql"
	"errors"
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
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	assetrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRootDomainCoverageIsolatedPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv(emptyPostgresMigrationTestDSNEnv))
	if dsn == "" {
		t.Skip("isolated PostgreSQL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var name string
	if err = db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&name); err != nil || !strings.HasPrefix(name, "lunafox_migration_test_") {
		t.Fatal("refusing non-test database")
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
	if _, err = db.ExecContext(ctx, `INSERT INTO auth_user(username,password,is_superuser) VALUES('registry-admin','unused',TRUE),('ai-other','unused',FALSE)`); err != nil {
		t.Fatal(err)
	}
	verifyRootDomainCoveragePostgres(t, ctx, db, dsn)
}

func verifyRootDomainCoveragePostgres(t *testing.T, ctx context.Context, db *sql.DB, dsn string) {
	t.Helper()
	owner, target, asset := taskFixtureScope(t, ctx, db, "coverage-root.example")
	_, targetB, assetB := taskFixtureScope(t, ctx, db, "b.coverage-root.example")
	var orgA, orgB, other int
	for _, x := range []struct {
		name string
		id   *int
	}{{"coverage-org-a", &orgA}, {"coverage-org-b", &orgB}} {
		if err := db.QueryRowContext(ctx, `INSERT INTO organization(name) VALUES($1) RETURNING id`, x.name).Scan(x.id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO organization_target(organization_id,target_id) VALUES($1,$3),($2,$4)`, orgA, orgB, target, targetB); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO subdomain(target_id,dns_name) SELECT $1,'node-'||n||'.coverage-root.example' FROM generate_series(1,60) n`, target); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO host_port_mapping(target_id,host,ip,port) VALUES($1,'api.coverage-root.example','192.0.2.42',22)`, target); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	registry := assetrepo.NewAssetRegistryStore(orm)
	for i := 0; i < 1000; i++ {
		worked, e := registry.ProcessRootDomainBatch(ctx, 100)
		if e != nil {
			t.Fatal(e)
		}
		if !worked {
			break
		}
	}
	var rootID int64
	if err = db.QueryRowContext(ctx, `SELECT id FROM asset_entity WHERE asset_type='root_domain' AND label='coverage-root.example'`).Scan(&rootID); err != nil {
		t.Fatal(err)
	}
	assets := assetapp.NewFacade(registry)
	tasks := airepo.NewTaskStore(orm)
	task, err := aiapp.NewTaskFacade(tasks, nil, nil).Create(ctx, aiapp.CreateTaskInput{UserID: owner, TargetID: target, AssetIDs: []int64{asset}, Goal: "coverage fixture", Name: "Coverage source"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tasks.ControlTask(ctx, owner, task.ID, "start", false); err != nil {
		t.Fatal(err)
	}
	var run *aiapp.Task
	for i := 0; i < 100; i++ {
		run, err = tasks.ClaimTask(ctx, uuid.NewString())
		if err != nil || run == nil {
			t.Fatalf("claim: %v", err)
		}
		if run.ID == task.ID {
			break
		}
		if err = tasks.FinishTask(ctx, run, "paused", "fixture cleanup"); err != nil {
			t.Fatal(err)
		}
	}
	if run == nil || run.ID != task.ID {
		t.Fatal("task not claimed")
	}
	run, err = tasks.StampTaskStart(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	connector, err := airuntime.NewTaskGraphConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := connector.OpenForExecution(ctx, taskExecution(run))
	if err != nil {
		t.Fatal(err)
	}
	graph := (&artexdb.DB{DB: conn}).Exploration(task.ExplorationID)
	if _, err = graph.AddNode("fact", map[string]any{"summary": "Root coverage confirmed evidence"}, 0, "confirmed", "worker", []int64{asset}); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	conn.Close()
	if err = tasks.FinishTask(ctx, run, "done", ""); err != nil {
		t.Fatal(err)
	}
	result, err := assets.RootDomainCoverage(ctx, owner, rootID, orgA)
	if err != nil {
		t.Fatal(err)
	}
	present, tested, ports := map[int64]bool{}, map[int64]bool{}, 0
	keys := map[string]bool{}
	for _, node := range result.Nodes {
		keys[node.Key] = true
		present[node.AssetID] = true
		tested[node.AssetID] = node.Tested
		if node.Port == 22 && node.AssetID == 0 {
			ports++
		}
	}
	if result.Truncated || len(result.Nodes) < 63 || !present[rootID] || !tested[asset] || present[assetB] || ports != 2 {
		t.Fatalf("wrong root scope: nodes=%d tested=%v foreign=%v ports=%d", len(result.Nodes), tested[asset], present[assetB], ports)
	}
	for _, edge := range result.Edges {
		if !keys[edge.Src] || !keys[edge.Dst] {
			t.Fatal("dangling edge")
		}
	}
	refs, err := assets.RootDomainCoverageRefs(ctx, owner, rootID, orgA, asset)
	if err != nil || len(refs.Facts) != 1 || refs.Facts[0].SourceTaskID != task.ID {
		t.Fatalf("missing evidence: %+v %v", refs, err)
	}
	if _, err = assets.RootDomainCoverageRefs(ctx, owner, rootID, orgA, assetB); !errors.Is(err, assetapp.ErrRootDomainNotFound) {
		t.Fatalf("cross organization refs: %v", err)
	}
	hidden, err := assets.RootDomainCoverageRefs(ctx, other, rootID, orgA, asset)
	if err != nil || len(hidden.Facts) != 0 {
		t.Fatalf("other owner evidence exposed: %+v %v", hidden, err)
	}
	if _, err = assets.RootDomainCoverage(ctx, 999999, rootID, orgA); !errors.Is(err, assetapp.ErrForbidden) {
		t.Fatalf("membership bypass: %v", err)
	}
	if _, err = assets.RootDomainCoverage(ctx, owner, asset, orgA); !errors.Is(err, assetapp.ErrRootDomainNotFound) {
		t.Fatalf("non-root accepted: %v", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_task SET deleted_at=clock_timestamp() WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	hidden, err = assets.RootDomainCoverageRefs(ctx, owner, rootID, orgA, asset)
	if err != nil || len(hidden.Facts) != 0 {
		t.Fatalf("deleted task evidence exposed: %+v %v", hidden, err)
	}
	result, err = assets.RootDomainCoverage(ctx, owner, rootID, orgB)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range result.Nodes {
		if node.AssetID == asset {
			t.Fatal("organization switch retained old asset")
		}
	}
}

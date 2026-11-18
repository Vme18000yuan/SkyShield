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
	// An edge-only port must disappear from the list, graph and overview under
	// the same switch. The named business domain and the other organization stay intact.
	if _, err = db.ExecContext(ctx, `INSERT INTO host_port_mapping(target_id,host,ip,port) VALUES($1,'cdn.coverage-root.example','104.16.0.9',19001)`, target); err != nil {
		t.Fatal(err)
	}
	for _, include := range []bool{false, true} {
		hide := !include
		overview, err := assets.OrganizationOverview(ctx, owner, orgA, hide)
		if err != nil {
			t.Fatal(err)
		}
		for kind, count := range map[string]int64{"root_domain": overview.RootDomains, "subdomain": overview.Subdomains, "ip": overview.IPs} {
			page, err := assets.List(ctx, assetapp.ListInput{UserID: owner, OrganizationID: orgA, AssetType: kind, HideCDN: hide, Page: 1, PageSize: 1})
			if err != nil || page.TotalSize != count {
				t.Fatalf("%s count mismatch: overview=%d page=%+v err=%v", kind, count, page, err)
			}
		}
		graph, err := assets.RootDomainCoverageView(ctx, owner, rootID, orgA, hide)
		if err != nil {
			t.Fatal(err)
		}
		foundIP, foundPort := false, false
		for _, n := range graph.Nodes {
			foundIP = foundIP || n.Kind == "ip" && n.Label == "104.16.0.9"
			foundPort = foundPort || n.Port == 19001
		}
		if foundIP != include || foundPort != include {
			t.Fatalf("CDN graph include=%t got IP=%t port=%t", include, foundIP, foundPort)
		}
		ports, err := assets.ListPorts(ctx, assetapp.PortListInput{UserID: owner, OrganizationID: orgA, RootDomainID: rootID, HideCDN: hide, Query: "19001"})
		if err != nil || (ports.TotalSize > 0) != include {
			t.Fatalf("CDN ports include=%t got %+v err=%v", include, ports, err)
		}
	}
	// Imported CDN evidence also covers IPs outside the public prefix snapshot,
	// but it cannot suppress the same IP for an unrelated organization.
	if _, err = db.ExecContext(ctx, `WITH g AS (
 INSERT INTO ai_info_gather_run(id,tenant_id,owner_user_id,execution_id,tool_use_id,target,target_kind,mode,status)
 VALUES($1,$2,$3,$4,'asset-cdn','coverage-root.example','HOST','fast','completed') RETURNING id)
 INSERT INTO ai_info_gather_record(run_id,kind,record_id,payload,status,target_id)
 SELECT id,'asset',repeat('b',64),'{"type":"ip","value":"2001:db8::21","cdn":true}','imported',$5 FROM g`, uuid.NewString(), assetapp.DefaultTenantID, owner, uuid.NewString(), target); err != nil {
		t.Fatal(err)
	}
	for org, want := range map[int]bool{orgA: true, orgB: false} {
		var marked bool
		err = db.QueryRowContext(ctx, `SELECT public.tq_inventory_cdn_asset('service','[2001:db8::21]:443',$1,$2,0)`, assetapp.DefaultTenantID, org).Scan(&marked)
		if err != nil || marked != want {
			t.Fatalf("organization %d CDN evidence: %t %v", org, marked, err)
		}
	}
	if _, err = assets.OrganizationOverview(ctx, owner, 2147483647, true); !errors.Is(err, assetapp.ErrOrganizationNotFound) {
		t.Fatalf("unknown organization: %v", err)
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

package main

import (
	"context"
	"database/sql"
	"testing"

	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	assetrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// verifyAssetRegistryPostgresContract runs only against the disposable database
// guarded by TestInitialSchemaBootstrapsEmptyPostgres.
func verifyAssetRegistryPostgresContract(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var targetA, targetB int
	for _, item := range []struct {
		name string
		id   *int
	}{
		{"registry-a.example", &targetA},
		{"registry-b.example", &targetB},
	} {
		if err := db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES($1,'domain') RETURNING id`, item.name).Scan(item.id); err != nil {
			t.Fatal(err)
		}
	}
	const rawURL = "https://registry-a.example/Case%2FPath?b=2&a=1"
	var websiteA, websiteB, websiteID, secondID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO website(target_id,url) VALUES($1,$2) RETURNING id`, targetA, rawURL).Scan(&websiteA); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO website(target_id,url) VALUES($1,$2) RETURNING id`, targetB, rawURL).Scan(&websiteB); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT asset_id FROM asset_website_binding WHERE website_id=$1`, websiteA).Scan(&websiteID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT asset_id FROM asset_website_binding WHERE website_id=$1`, websiteB).Scan(&secondID); err != nil {
		t.Fatal(err)
	}
	if websiteID != secondID {
		t.Fatalf("same URL in two targets produced asset IDs %d and %d", websiteID, secondID)
	}
	var label string
	if err := db.QueryRowContext(ctx, `SELECT label FROM asset_entity WHERE id=$1`, websiteID).Scan(&label); err != nil || label != rawURL {
		t.Fatalf("raw URL changed: got %q, err %v", label, err)
	}
	var endpointID int64
	var method sql.NullString
	if err := db.QueryRowContext(ctx, `INSERT INTO endpoint(target_id,url) VALUES($1,$2) RETURNING id`, targetA, rawURL).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT e.method FROM asset_endpoint_binding b JOIN asset_entity e ON e.id=b.asset_id WHERE b.endpoint_id=$1`, endpointID).Scan(&method); err != nil || method.Valid {
		t.Fatalf("legacy endpoint must have unknown method: %+v, err %v", method, err)
	}
	var hostPortID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO host_port_mapping(target_id,host,ip,port) VALUES($1,'registry-a.example','192.0.2.11',443) RETURNING id`, targetA).Scan(&hostPortID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT e.label FROM asset_host_port_binding b JOIN asset_entity e ON e.id=b.asset_id WHERE b.host_port_id=$1 AND e.asset_type='ip'`, hostPortID).Scan(&label); err != nil || label != "192.0.2.11" {
		t.Fatalf("host port IP identity mismatch: %q, err %v", label, err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO website(target_id,url) VALUES($1,'https://registry-rollback.example/')`, targetA); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_entity WHERE label='https://registry-rollback.example/'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("asset registration escaped transaction: count %d, err %v", n, err)
	}
	var userID int
	if err := db.QueryRowContext(ctx, `INSERT INTO auth_user(username,password,is_superuser) VALUES('registry-admin','unused',TRUE) RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	var role string
	if err := db.QueryRowContext(ctx, `SELECT role FROM tenant_membership WHERE user_id=$1 AND tenant_id='00000000-0000-0000-0000-000000000001'`, userID).Scan(&role); err != nil || role != "admin" {
		t.Fatalf("default tenant membership: role %q, err %v", role, err)
	}

	// Simulate a row predating migration 000010 and exercise resumable backfill.
	if _, err := db.ExecContext(ctx, `ALTER TABLE website DISABLE TRIGGER tq_website_asset_binding`); err != nil {
		t.Fatal(err)
	}
	var historicalID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO website(target_id,url) VALUES($1,'https://registry-historical.example/') RETURNING id`, targetA).Scan(&historicalID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE website ENABLE TRIGGER tq_website_asset_binding`); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := assetrepo.NewAssetRegistryStore(orm)
	complete := false
	for i := 0; i < 100 && !complete; i++ {
		complete, err = store.BackfillBatch(ctx, 2)
		if err != nil {
			t.Fatalf("backfill batch: %v", err)
		}
	}
	if !complete {
		t.Fatal("backfill did not finish")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_website_binding WHERE website_id=$1`, historicalID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("historical website binding: count %d, err %v", n, err)
	}
	member, err := store.IsActiveTenantMember(ctx, assetapp.DefaultTenantID, userID)
	if err != nil || !member {
		t.Fatalf("tenant access: member %v, err %v", member, err)
	}
	items, err := store.ListAssets(ctx, assetapp.AssetQuery{TenantID: assetapp.DefaultTenantID, AssetType: "service", Query: "Case%2FPath", Limit: 10})
	if err != nil || len(items) != 1 || items[0].ID != websiteID || items[0].SourceCount != 2 {
		t.Fatalf("unified asset query: items %+v, err %v", items, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE target SET deleted_at=CURRENT_TIMESTAMP WHERE id=$1`, targetA); err != nil {
		t.Fatal(err)
	}
	items, err = store.ListAssets(ctx, assetapp.AssetQuery{TenantID: assetapp.DefaultTenantID, AssetType: "service", Query: "Case%2FPath", Limit: 10})
	if err != nil || len(items) != 1 || items[0].SourceCount != 1 {
		t.Fatalf("shared asset after Target deletion: items %+v, err %v", items, err)
	}
}

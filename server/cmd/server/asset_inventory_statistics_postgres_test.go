package main

import (
	"context"
	"database/sql"
	"testing"
	"time"

	assetrepo "github.com/yyhuni/lunafox/server/internal/modules/asset/repository"
	monitorapp "github.com/yyhuni/lunafox/server/internal/modules/assetmonitor/application"
	monitorrepo "github.com/yyhuni/lunafox/server/internal/modules/assetmonitor/repository"
	registryapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	registryrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyAssetInventoryStatisticsPostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	for _, tc := range []struct{ input, expected string }{
		{"HTTP://Example.COM/a?x=1", "example.com:80"},
		{"https://example.com:0443/path", "example.com:443"},
		{"https://user:password@EXAMPLE.COM/a", "example.com:443"},
		{"example.com:22", "example.com:22"},
		{"http://[2001:db8::1]/", "[2001:db8::1]:80"},
		{"[2001:db8::1]:8443", "[2001:db8::1]:8443"},
		{"http://example.com:99999", ""},
		{"https://example.com:abc/", ""},
	} {
		var result sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT tq_inventory_host_port($1)`, tc.input).Scan(&result); err != nil || result.String != tc.expected {
			t.Fatalf("host-port normalization %q = %q, want %q: %v", tc.input, result.String, tc.expected, err)
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for range 8 {
		var valid int
		if err = tx.QueryRowContext(ctx, `SELECT count(tq_inventory_host_port('http://host'||n||'.example/a')) FROM generate_series(1,4000) n`).Scan(&valid); err != nil || valid != 4000 {
			_ = tx.Rollback()
			t.Fatalf("repeated host-port parsing = %d: %v", valid, err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	stats := assetrepo.NewAssetStatisticsRepository(orm)
	before, err := stats.GetCurrentAssetStatistics(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var user, org, target, hidden int
	if err = db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `INSERT INTO organization(name) VALUES('inventory-statistics-org') RETURNING id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES('inventory-statistics-fixture.example','domain') RETURNING id`).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES('hidden-inventory-fixture.example','domain') RETURNING id`).Scan(&hidden); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO organization_target(organization_id,target_id) VALUES($1,$2)`, org, target); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO subdomain(target_id,dns_name) VALUES($1,'inventory-statistics-fixture.example')`, target); err != nil {
		t.Fatal(err)
	}
	for _, port := range []int{80, 443, 22, 8080, 8443, 3306} {
		if _, err = db.ExecContext(ctx, `INSERT INTO host_port_mapping(target_id,host,ip,port) VALUES($1,'inventory-statistics-fixture.example','203.0.113.249',$2)`, target, port); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO host_port_mapping(target_id,host,ip,port) VALUES($1,'second.inventory-statistics-fixture.example','203.0.113.249',80),($2,'hidden-inventory-fixture.example','203.0.113.249',9090)`, target, hidden); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO website(target_id,url,host) VALUES($1,'http://inventory-statistics-fixture.example/','inventory-statistics-fixture.example'),($1,'https://inventory-statistics-fixture.example/login','inventory-statistics-fixture.example')`, target); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO endpoint(target_id,url,host) VALUES($1,'http://inventory-statistics-fixture.example/a','inventory-statistics-fixture.example'),($1,'http://inventory-statistics-fixture.example:80/b','inventory-statistics-fixture.example')`, target); err != nil {
		t.Fatal(err)
	}
	after, err := stats.GetCurrentAssetStatistics(ctx, time.Now().Add(-24*time.Hour))
	if err != nil || after.TotalAssets-before.TotalAssets != 17 || after.TotalServices-before.TotalServices != 15 {
		t.Fatalf("global inventory before/after = %+v / %+v, err %v", before, after, err)
	}
	history, err := stats.ListAssetStatisticsHistory(ctx, time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -29), 30)
	if err != nil || len(history) != 30 || history[29].TotalAssets != after.TotalAssets {
		t.Fatalf("30-day inventory history: %+v %v", history, err)
	}
	snapshot, err := monitorapp.NewFacade(monitorrepo.NewStore(orm), nil).Read(ctx, user, []int{org})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Summary.TotalAssets != 15 || snapshot.Summary.IPAssets != 1 || len(snapshot.RecentAssets) != 15 {
		t.Fatalf("scoped inventory = %+v", snapshot)
	}
	counts := map[string]int64{}
	for _, item := range snapshot.InventoryCategories {
		counts[item.Key] = item.Count
	}
	if counts["subdomain"] != 1 || counts["ip"] != 1 || counts["service"] != 13 {
		t.Fatalf("inventory categories = %+v", counts)
	}
	expectedPorts := []struct {
		key   string
		count int64
	}{{"80", 3}, {"22", 2}, {"443", 2}, {"3306", 2}, {"8080", 2}, {"8443", 2}}
	if len(snapshot.Ports) != len(expectedPorts) {
		t.Fatalf("top ports = %+v", snapshot.Ports)
	}
	for i, expected := range expectedPorts {
		if snapshot.Ports[i].Key != expected.key || snapshot.Ports[i].Count != expected.count {
			t.Fatalf("top ports = %+v", snapshot.Ports)
		}
	}
	if _, err = db.ExecContext(ctx, `UPDATE target SET deleted_at=CURRENT_TIMESTAMP WHERE id IN ($1,$2)`, target, hidden); err != nil {
		t.Fatal(err)
	}
	remaining, err := stats.GetCurrentAssetStatistics(ctx, time.Now().Add(-24*time.Hour))
	if err != nil || remaining.TotalAssets != before.TotalAssets {
		t.Fatalf("deleted target remained in global inventory: before=%+v after=%+v err=%v", before, remaining, err)
	}
	registration := registryapp.NewRegistrationFacade(registryrepo.NewAssetRegistrationStore(orm, registryapp.LegacyAssetCreators{}), nil)
	created, err := registration.Register(ctx, registryapp.BatchRegisterInput{UserID: user, Origin: "manual", RequestID: "inventory-direct-org-service", Assets: []registryapp.AssetInput{{Type: "service", OrganizationID: org, Domain: "direct-inventory.example", ServiceName: "SSH", Port: 22, Proto: "tcp"}}})
	if err != nil || len(created.Results) != 1 || len(created.Errors) != 0 {
		t.Fatalf("direct organization asset: %+v %v", created, err)
	}
	directSnapshot, err := monitorapp.NewFacade(monitorrepo.NewStore(orm), nil).Read(ctx, user, []int{org})
	if err != nil || directSnapshot.Summary.TotalAssets != 1 || len(directSnapshot.Ports) != 1 || directSnapshot.Ports[0].Key != "22" {
		t.Fatalf("direct organization inventory: %+v %v", directSnapshot, err)
	}
	remaining, err = stats.GetCurrentAssetStatistics(ctx, time.Now().Add(-24*time.Hour))
	if err != nil || remaining.TotalAssets != before.TotalAssets+1 {
		t.Fatalf("direct asset missing from global inventory: %+v %v", remaining, err)
	}
}

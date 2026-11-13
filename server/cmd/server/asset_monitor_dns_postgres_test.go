package main

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/yyhuni/lunafox/server/internal/geolocation"
	monitorrepo "github.com/yyhuni/lunafox/server/internal/modules/assetmonitor/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyAssetMonitorDNSPostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var user, org, target int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO organization(name) VALUES('dns-monitor-org') RETURNING id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES('dns-monitor-fixture.example.com','domain') RETURNING id`).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO organization_target(organization_id,target_id) VALUES($1,$2)`, org, target); err != nil {
		t.Fatal(err)
	}
	host := "asset.dns-monitor-fixture.example.com"
	if _, err := db.ExecContext(ctx, `INSERT INTO subdomain(target_id,dns_name) VALUES($1,$2)`, target, host); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO website(target_id,url,host) VALUES($1,'https://asset.dns-monitor-fixture.example.com/',$2)`, target, host); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO host_port_mapping(target_id,host,ip,port) VALUES($1,$2,'198.18.1.2',80)`, target, host); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := monitorrepo.NewStore(orm)
	initial, err := store.Read(ctx, user, []int{org})
	if err != nil {
		t.Fatal(err)
	}
	if initial.Summary.TotalAssets != 5 || initial.Summary.LocatedAssets != 0 || len(initial.PendingHosts) != 1 || initial.PendingHosts[0] != host {
		t.Fatalf("initial DNS candidates: %+v", initial)
	}
	claimed, err := store.ClaimHost(ctx, host)
	if err != nil || !claimed {
		t.Fatalf("claim DNS lookup: %v %v", claimed, err)
	}
	claimed, err = store.ClaimHost(ctx, host)
	if err != nil || claimed {
		t.Fatalf("duplicate DNS claim: %v %v", claimed, err)
	}
	if err = store.SaveHostResolution(ctx, host, "198.18.1.2", time.Now()); err == nil {
		t.Fatal("accepted fake IP as DNS evidence")
	}
	now := time.Now().UTC()
	if err = store.SaveHostResolution(ctx, host, "8.8.8.8", now); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveLocation(ctx, geolocation.Location{ObservedIP: "8.8.8.8", Latitude: 37.4, Longitude: -122.1, ProviderKey: geolocation.FreeIPAPIProviderKey, ResolvedAt: now}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Read(ctx, user, []int{org})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Summary.TotalAssets != 5 || snapshot.Summary.LocatedAssets != 3 || snapshot.Summary.UnlocatedAssets != 2 || len(snapshot.Locations) != 1 || snapshot.Locations[0].Precision != "dns" || snapshot.Locations[0].Count != 3 {
		t.Fatalf("DNS inferred only domain assets: %+v", snapshot)
	}
	if _, err = db.ExecContext(ctx, `UPDATE asset_monitor_dns_resolution SET resolved_at=CURRENT_TIMESTAMP-INTERVAL '2 hours',next_attempt_at=CURRENT_TIMESTAMP-INTERVAL '1 minute' WHERE host=$1`, host); err != nil {
		t.Fatal(err)
	}
	stale, err := store.Read(ctx, user, []int{org})
	if err != nil || len(stale.Locations) != 1 || !stale.Locations[0].Expired || len(stale.PendingHosts) != 1 {
		t.Fatalf("stale DNS attribution: %+v %v", stale, err)
	}
}

package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	serverdatabase "github.com/yyhuni/lunafox/server/internal/database"
	"github.com/yyhuni/lunafox/server/internal/geolocation"
	monitorrepo "github.com/yyhuni/lunafox/server/internal/modules/assetmonitor/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestAssetMonitorCitiesAndRecent50IsolatedPostgres(t *testing.T) {
	dsn := os.Getenv(emptyPostgresMigrationTestDSNEnv)
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
	var name string
	if err = db.QueryRowContext(ctx, "SELECT current_database()").Scan(&name); err != nil || !strings.HasPrefix(name, "lunafox_migration_test_") {
		t.Fatalf("refusing non-test database %q", name)
	}
	if countPublicTables(t, ctx, db, false) != 0 {
		t.Fatal("test database must start empty")
	}
	previousFS, previousPath := serverdatabase.MigrationsFS, serverdatabase.MigrationsPath
	serverdatabase.MigrationsFS, serverdatabase.MigrationsPath = migrationsFS, "migrations"
	defer func() { serverdatabase.MigrationsFS, serverdatabase.MigrationsPath = previousFS, previousPath }()
	if err = serverdatabase.MigrateToVersion(db, 76); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO asset_monitor_ip_location(ip,latitude,longitude,provider_key,resolved_at,next_attempt_at) VALUES('8.8.8.8',37.4211,-122.0811,'freeipapi',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP+INTERVAL '7 days')`); err != nil {
		t.Fatal(err)
	}
	if err = serverdatabase.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	var preserved bool
	if err = db.QueryRowContext(ctx, `SELECT latitude=37.4211 AND longitude=-122.0811 AND city_name='' AND next_attempt_at<=CURRENT_TIMESTAMP FROM asset_monitor_ip_location WHERE ip='8.8.8.8'`).Scan(&preserved); err != nil || !preserved {
		t.Fatalf("migration did not preserve and schedule old geography: %v", err)
	}
	var user, org, target, hidden int
	if err = db.QueryRowContext(ctx, `INSERT INTO auth_user(username,password,is_superuser) VALUES('monitor-city-admin','unused',TRUE) RETURNING id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `INSERT INTO organization(name) VALUES('monitor-city-scope') RETURNING id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES('city.example','domain') RETURNING id`).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES('hidden-city.example','domain') RETURNING id`).Scan(&hidden); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO organization_target(organization_id,target_id) VALUES($1,$2)`, org, target); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO subdomain(target_id,dns_name,created_at) SELECT $1,'a'||lpad(i::text,2,'0')||'.city.example',CURRENT_TIMESTAMP-i*INTERVAL '1 minute' FROM generate_series(0,54) i`, target); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO subdomain(target_id,dns_name,created_at) VALUES($1,'leak.hidden-city.example',CURRENT_TIMESTAMP+INTERVAL '1 day')`, hidden); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO host_port_mapping(target_id,host,ip,port) VALUES($1,'a00.city.example','8.8.8.8',443),($1,'a01.city.example','1.1.1.1',443)`, target); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO asset_observation(tenant_id,asset_id,target_id,original_target_id,organization_id,created_by,origin,source_key,request_fingerprint,payload)
 SELECT '00000000-0000-0000-0000-000000000001',b.asset_id,$1,$1,$2,$3,'import','city-country-fixture','fixture','{"extra":{"country":"CN","city":"Beijing"}}'::jsonb FROM subdomain d JOIN asset_subdomain_binding b ON b.subdomain_id=d.id WHERE d.target_id=$1 AND d.dns_name='a03.city.example'`, target, org, user); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := monitorrepo.NewStore(orm)
	now := time.Now().UTC()
	for _, location := range []geolocation.Location{
		{ObservedIP: "8.8.8.8", Latitude: 37.4211, Longitude: -122.0811, CountryCode: "US", RegionName: "California", CityName: "Alpha", ProviderKey: geolocation.FreeIPAPIProviderKey, ResolvedAt: now},
		{ObservedIP: "1.1.1.1", Latitude: 37.4222, Longitude: -122.0822, CountryCode: "US", RegionName: "California", CityName: "Beta", ProviderKey: geolocation.FreeIPAPIProviderKey, ResolvedAt: now},
	} {
		if err = store.SaveLocation(ctx, location); err != nil {
			t.Fatal(err)
		}
	}
	if err = store.SaveHostResolution(ctx, "a02.city.example", "1.1.1.1", now); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Read(ctx, user, []int{org})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.RecentAssets) != 50 {
		t.Fatalf("recent asset count=%d", len(snapshot.RecentAssets))
	}
	seen := map[string]bool{}
	for index, asset := range snapshot.RecentAssets {
		if seen[asset.ID] || strings.Contains(asset.Label, "hidden-city") {
			t.Fatal("recent assets duplicated or escaped scope")
		}
		seen[asset.ID] = true
		if index > 0 && asset.FirstSeenAt.After(snapshot.RecentAssets[index-1].FirstSeenAt) {
			t.Fatal("recent assets are not newest first")
		}
	}
	cityKinds := map[string]bool{}
	for _, location := range snapshot.Locations {
		if location.Precision == "country" {
			if location.CityName != "" || location.Latitude != nil {
				t.Fatal("country-only evidence invented a city coordinate")
			}
			continue
		}
		cityKinds[location.Precision+":"+location.CityName] = true
		if location.CountryCode != "US" || location.RegionName != "California" {
			t.Fatalf("city provenance lost: %+v", location)
		}
		if location.CityName == "Alpha" && (location.Latitude == nil || *location.Latitude != 37.4211) {
			t.Fatal("coordinates were rounded to a coarse city cell")
		}
	}
	countryQueued := false
	for _, host := range snapshot.PendingHosts {
		if host == "a03.city.example" {
			countryQueued = true
		}
	}
	if !countryQueued {
		t.Fatal("country-only geography blocked city enrichment")
	}
	for _, key := range []string{"ip:Alpha", "ip:Beta", "dns:Beta"} {
		if !cityKinds[key] {
			t.Fatalf("missing city projection %s: %+v", key, snapshot.Locations)
		}
	}
	verifyAssetMonitorTop40(t, ctx, db, store, user, org, target, hidden)
}

func verifyAssetMonitorTop40(t *testing.T, ctx context.Context, db *sql.DB, store *monitorrepo.Store, user, org, target, hidden int) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `INSERT INTO host_port_mapping(target_id,host,ip,port)
	 SELECT $1::integer,'a00.city.example','8.8.8.8'::inet,10000+i FROM generate_series(0,44) i
	 UNION ALL SELECT $1::integer,'a01.city.example','1.1.1.1'::inet,10044
	 UNION ALL SELECT $2::integer,'hidden'||i||'.hidden-city.example','1.1.1.1'::inet,30000 FROM generate_series(1,6) i`, target, hidden); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO website(target_id,url,host,tech) VALUES
 ($1,'https://a00.city.example/','a00.city.example',ARRAY(SELECT 'technology-'||lpad(i::text,2,'0') FROM generate_series(1,45) i)||ARRAY['technology-45']),
 ($1,'https://a01.city.example/','a01.city.example',ARRAY['technology-45']),
 ($2,'https://hidden-city.example/','hidden-city.example',ARRAY['hidden-technology'])`, target, hidden); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Read(ctx, user, []int{org})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Ports) != 40 || len(snapshot.Technologies) != 40 {
		t.Fatalf("expected 40 ports and technologies, got %d and %d", len(snapshot.Ports), len(snapshot.Technologies))
	}
	if snapshot.Ports[0].Key != "443" || snapshot.Ports[0].Count != 4 || snapshot.Ports[1].Key != "10044" || snapshot.Ports[1].Count != 4 {
		t.Fatalf("ports were not ranked by deduplicated host:port count: %+v", snapshot.Ports[:2])
	}
	for i, item := range snapshot.Ports[2:] {
		if item.Key != fmt.Sprint(10000+i) || item.Count != 2 {
			t.Fatalf("port rank %d: %+v", i+3, item)
		}
	}
	if snapshot.Technologies[0].Key != "technology-45" || snapshot.Technologies[0].Count != 2 {
		t.Fatalf("technologies were not ranked by distinct website count: %+v", snapshot.Technologies[0])
	}
	for i, item := range snapshot.Technologies[1:] {
		if item.Key != fmt.Sprintf("technology-%02d", i+1) || item.Count != 1 {
			t.Fatalf("technology rank %d: %+v", i+2, item)
		}
	}
}

package main

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// This fixture uses only an explicitly named disposable database.
func TestInfoGatherRiskMigrationPreservesEvidenceAndBackfillsExactAssets(t *testing.T) {
	dsn := os.Getenv("TIANQIONG_RISK_MIGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL DSN not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	var name string
	if err := db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&name); err != nil || !strings.HasPrefix(name, "tianqiong_risk_migration_test_") {
		t.Fatalf("refusing non-test database: %q %v", name, err)
	}
	for _, query := range []string{
		`CREATE TABLE asset_entity(id bigint primary key)`,
		`CREATE TABLE vulnerability(id int primary key,target_id int not null,url text not null,vuln_type text not null,severity text not null,source text not null)`,
		`CREATE TABLE endpoint(id int primary key,target_id int not null,url text not null)`,
		`CREATE TABLE asset_endpoint_binding(endpoint_id int primary key,asset_id bigint not null)`,
		`CREATE TABLE website(id int primary key,target_id int not null,url text not null)`,
		`CREATE TABLE asset_website_binding(website_id int primary key,asset_id bigint not null)`,
		`CREATE TABLE ai_info_gather_record(run_id uuid not null,kind text not null,record_id text not null,status text not null,target_id int,reason text not null default '',payload jsonb not null,updated_at timestamptz not null default now(),primary key(run_id,kind,record_id))`,
		`INSERT INTO asset_entity VALUES(101),(102),(103),(104)`,
		`INSERT INTO vulnerability VALUES(1,7,'https://app.example.test/api','匿名数据响应（待复核）','info','info-gath'),(2,7,'https://web.example.test/','test','info','info-gath'),(3,7,'https://ambiguous.example.test/','test','info','info-gath')`,
		`INSERT INTO endpoint VALUES(1,7,'https://app.example.test/api'),(2,7,'https://ambiguous.example.test/'),(3,7,'https://ambiguous.example.test/')`,
		`INSERT INTO asset_endpoint_binding VALUES(1,101),(2,103),(3,104)`,
		`INSERT INTO website VALUES(1,7,'https://web.example.test/')`,
		`INSERT INTO asset_website_binding VALUES(1,102)`,
		`INSERT INTO ai_info_gather_record(run_id,kind,record_id,status,target_id,payload) VALUES
		 ('11111111-1111-4111-8111-111111111111','vulnerability','a','imported',7,'{"type":"ANONYMOUS_DATA","url":"https://app.example.test/api"}'),
		 ('11111111-1111-4111-8111-111111111111','observation','a','pending',NULL,'{"category":"ANONYMOUS_DATA","value":"https://app.example.test/api"}'),
		 ('11111111-1111-4111-8111-111111111111','vulnerability','b','imported',7,'{"type":"ANONYMOUS_DATA","url":"https://missing.example.test/"}'),
		 ('11111111-1111-4111-8111-111111111111','observation','b','pending',NULL,'{"category":"ANONYMOUS_DATA","value":"https://missing.example.test/"}'),
		 ('11111111-1111-4111-8111-111111111111','vulnerability','c','imported',7,'{"type":"ANONYMOUS_DATA","url":"https://app.example.test/api"}'),
		 ('11111111-1111-4111-8111-111111111111','observation','c','pending',NULL,'{"category":"ANONYMOUS_DATA","value":"https://different.example.test/"}')`,
	} {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	up, err := os.ReadFile("migrations/000075_info_gather_risk_assets.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, string(up)); err != nil {
		t.Fatal(err)
	}
	var first, second sql.NullInt64
	var third sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT (SELECT asset_id FROM vulnerability WHERE id=1),(SELECT asset_id FROM vulnerability WHERE id=2),(SELECT asset_id FROM vulnerability WHERE id=3)`).Scan(&first, &second, &third); err != nil || !first.Valid || first.Int64 != 101 || !second.Valid || second.Int64 != 102 || third.Valid {
		t.Fatalf("incorrect exact or ambiguous associations: %v %v %v %v", first, second, third, err)
	}
	var status, reason string
	if err := db.QueryRowContext(ctx, `SELECT status,reason FROM ai_info_gather_record WHERE kind='observation' AND record_id='a'`).Scan(&status, &reason); err != nil || status != "ignored" || reason == "" {
		t.Fatalf("duplicate not resolved: %q %q %v", status, reason, err)
	}
	var pending int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM ai_info_gather_record WHERE kind='observation' AND record_id IN ('b','c') AND status='pending'`).Scan(&pending); err != nil || pending != 2 {
		t.Fatalf("unmatched evidence changed: %d %v", pending, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM asset_entity WHERE id=101`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT asset_id FROM vulnerability WHERE id=1`).Scan(&first); err != nil || first.Valid {
		t.Fatalf("deleted asset left stale association: %v %v", first, err)
	}
}

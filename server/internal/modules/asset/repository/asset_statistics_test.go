package repository

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestAssetStatisticsRepositoryCurrentProjection(t *testing.T) {
	db := newAssetRepositoryDB(t)
	seedAssetStatisticsSupportTables(t, db)
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	seedAssetStatisticsCurrentRows(t, db, now)

	statistics, err := NewAssetStatisticsRepository(db).GetCurrentAssetStatistics(context.Background(), now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("get current statistics: %v", err)
	}
	if statistics.TotalTargets != 1 || statistics.TotalSubdomains != 2 || statistics.TotalIPs != 2 || statistics.TotalEndpoints != 1 || statistics.TotalWebsites != 1 || statistics.TotalVulns != 2 || statistics.RunningScans != 1 {
		t.Fatalf("unexpected totals: %+v", statistics)
	}
	if statistics.TotalServices != 4 || statistics.TotalAssets != 8 {
		t.Fatalf("total services/assets = %d/%d, want 4/8", statistics.TotalServices, statistics.TotalAssets)
	}
	if statistics.ChangeTargets != 0 || statistics.ChangeSubdomains != 1 || statistics.ChangeIPs != 1 || statistics.ChangeServices != 2 || statistics.ChangeEndpoints != 1 || statistics.ChangeWebsites != 0 || statistics.ChangeVulns != 1 || statistics.ChangeAssets != 4 {
		t.Fatalf("unexpected 24h changes: %+v", statistics)
	}
	if statistics.VulnsBySeverity.Critical != 1 || statistics.VulnsBySeverity.Info != 0 {
		t.Fatalf("unexpected severity counts: %+v", statistics.VulnsBySeverity)
	}
}

func TestAssetStatisticsRepositoryHistoryUsesFirstSeenDistinctIP(t *testing.T) {
	db := newAssetRepositoryDB(t)
	seedAssetStatisticsSupportTables(t, db)
	start := time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)
	if err := db.Exec("UPDATE target SET created_at = ? WHERE id = 1", start.Add(-24*time.Hour)).Error; err != nil {
		t.Fatalf("set target creation time: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO subdomain (id, target_id, dns_name, created_at) VALUES
			(101, 1, 'one.example.test', ?),
			(102, 1, 'two.example.test', ?);
		INSERT INTO host_port_mapping (id, target_id, host, ip, port, created_at) VALUES
			(101, 1, 'one.example.test', '192.0.2.1', 443, ?),
			(102, 1, 'two.example.test', '192.0.2.1', 443, ?),
			(103, 1, 'two.example.test', '192.0.2.2', 443, ?);
		INSERT INTO endpoint (id, target_id, url, created_at) VALUES (101, 1, 'https://two.example.test/api', ?);
		INSERT INTO website (id, target_id, url, created_at) VALUES (101, 1, 'https://one.example.test', ?);
		INSERT INTO vulnerability (id, created_at) VALUES (101, ?);
	`,
		start.Add(10*time.Hour), start.AddDate(0, 0, 1).Add(10*time.Hour),
		start.Add(10*time.Hour), start.AddDate(0, 0, 1).Add(8*time.Hour), start.AddDate(0, 0, 1).Add(8*time.Hour),
		start.AddDate(0, 0, 1).Add(10*time.Hour), start.Add(10*time.Hour), start.AddDate(0, 0, 1).Add(10*time.Hour)).Error; err != nil {
		t.Fatalf("seed history rows: %v", err)
	}

	history, err := NewAssetStatisticsRepository(db).ListAssetStatisticsHistory(context.Background(), start, 2)
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("history length = %d, want 2", len(history))
	}
	first, second := history[0], history[1]
	if !first.Date.Equal(start) || first.TotalTargets != 1 || first.TotalSubdomains != 1 || first.TotalIPs != 1 || first.TotalServices != 2 || first.TotalEndpoints != 0 || first.TotalWebsites != 1 || first.TotalVulns != 0 || first.TotalAssets != 4 {
		t.Fatalf("unexpected first history item: %+v", first)
	}
	if second.TotalTargets != 1 || second.TotalSubdomains != 2 || second.TotalIPs != 2 || second.TotalServices != 4 || second.TotalEndpoints != 1 || second.TotalWebsites != 1 || second.TotalVulns != 1 || second.TotalAssets != 8 {
		t.Fatalf("unexpected second history item: %+v", second)
	}
}

func seedAssetStatisticsSupportTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, statement := range []string{
		`CREATE TABLE vulnerability (id INTEGER PRIMARY KEY, severity TEXT, created_at DATETIME)`,
		`CREATE TABLE scan (id INTEGER PRIMARY KEY, status TEXT, deleted_at DATETIME)`,
		`CREATE VIEW asset_inventory_visible_source AS
		 SELECT '00000000-0000-0000-0000-000000000001' tenant_id,'subdomain' category,lower(s.dns_name) label,s.created_at first_seen_at FROM subdomain s JOIN target t ON t.id=s.target_id AND t.deleted_at IS NULL
		 UNION ALL SELECT '00000000-0000-0000-0000-000000000001','ip',h.ip,h.created_at FROM host_port_mapping h JOIN target t ON t.id=h.target_id AND t.deleted_at IS NULL
		 UNION ALL SELECT '00000000-0000-0000-0000-000000000001','service',lower(h.host)||':'||h.port,h.created_at FROM host_port_mapping h JOIN target t ON t.id=h.target_id AND t.deleted_at IS NULL
		 UNION ALL SELECT '00000000-0000-0000-0000-000000000001','service',h.ip||':'||h.port,h.created_at FROM host_port_mapping h JOIN target t ON t.id=h.target_id AND t.deleted_at IS NULL`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create support table: %v", err)
		}
	}
}

func seedAssetStatisticsCurrentRows(t *testing.T, db *gorm.DB, now time.Time) {
	t.Helper()
	if err := db.Exec("UPDATE target SET created_at = ? WHERE id = 1", now.Add(-48*time.Hour)).Error; err != nil {
		t.Fatalf("set target creation time: %v", err)
	}
	result := db.Exec(`
		INSERT INTO subdomain (id, target_id, dns_name, created_at) VALUES
			(101, 1, 'old.example.test', ?),
			(102, 1, 'new.example.test', ?);
		INSERT INTO host_port_mapping (id, target_id, host, ip, port, created_at) VALUES
			(101, 1, 'old.example.test', '192.0.2.1', 443, ?),
			(102, 1, 'new.example.test', '192.0.2.1', 443, ?),
			(103, 1, 'new.example.test', '192.0.2.2', 443, ?);
		INSERT INTO endpoint (id, target_id, url, created_at) VALUES (101, 1, 'https://new.example.test/api', ?);
		INSERT INTO website (id, target_id, url, created_at) VALUES (101, 1, 'https://old.example.test', ?);
		INSERT INTO vulnerability (id, severity, created_at) VALUES
			(101, 'critical', ?),
			(102, 'unknown', ?);
		INSERT INTO scan (id, status, deleted_at) VALUES
			(101, 'running', NULL),
			(102, 'running', ?);
	`,
		now.Add(-48*time.Hour), now.Add(-2*time.Hour),
		now.Add(-48*time.Hour), now.Add(-2*time.Hour), now.Add(-3*time.Hour),
		now.Add(-time.Hour), now.Add(-26*time.Hour), now.Add(-time.Hour), now.Add(-26*time.Hour), now)
	if err := result.Error; err != nil {
		t.Fatalf("seed current rows: %v", err)
	}
}

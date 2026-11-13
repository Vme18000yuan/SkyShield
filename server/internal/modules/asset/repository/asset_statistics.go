package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	assetdomain "github.com/yyhuni/lunafox/server/internal/modules/asset/domain"
	"gorm.io/gorm"
)

// AssetStatisticsRepository reads current-state aggregates without exposing other modules' persistence models.
type AssetStatisticsRepository struct {
	db *gorm.DB
}

func NewAssetStatisticsRepository(db *gorm.DB) *AssetStatisticsRepository {
	return &AssetStatisticsRepository{db: db}
}

type assetStatisticsRow struct {
	TotalTargets    int64
	TotalSubdomains int64
	TotalIPs        int64
	TotalServices   int64
	TotalEndpoints  int64
	TotalWebsites   int64
	TotalVulns      int64
	RunningScans    int64

	ChangeTargets    int64
	ChangeSubdomains int64
	ChangeIPs        int64
	ChangeServices   int64
	ChangeEndpoints  int64
	ChangeWebsites   int64
	ChangeVulns      int64
}

type vulnerabilitySeverityRow struct {
	Severity string
	Count    int64
}

func (r *AssetStatisticsRepository) GetCurrentAssetStatistics(ctx context.Context, changeSince time.Time) (assetdomain.AssetStatistics, error) {
	var statistics assetdomain.AssetStatistics
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, err := queryCurrentAssetStatistics(tx, changeSince.UTC())
		if err != nil {
			return err
		}
		statistics = assetStatisticsFromRow(row)

		var severityRows []vulnerabilitySeverityRow
		if err := tx.Raw(`SELECT severity, COUNT(*) AS count FROM vulnerability GROUP BY severity`).Scan(&severityRows).Error; err != nil {
			return err
		}
		statistics.VulnsBySeverity = mapVulnerabilitySeverityCounts(severityRows)
		return nil
	})
	return statistics, err
}

func (r *AssetStatisticsRepository) ListAssetStatisticsHistory(ctx context.Context, start time.Time, days int) ([]assetdomain.AssetStatisticsHistoryItem, error) {
	history := make([]assetdomain.AssetStatisticsHistoryItem, 0, days)
	if days == 0 {
		return history, nil
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Build the normalized inventory once for the entire history window.
		// Re-reading it for every day multiplies database work as inventories grow.
		selects := make([]string, 0, days*7)
		args := make([]any, 0, days*7)
		destinations := make([]any, 0, days*7)
		for offset := 0; offset < days; offset++ {
			day := start.UTC().AddDate(0, 0, offset)
			history = append(history, assetdomain.AssetStatisticsHistoryItem{Date: day})
			item := &history[offset]
			selects = append(selects,
				"(SELECT COUNT(*) FROM target WHERE deleted_at IS NULL AND created_at < ?)",
				"(SELECT COUNT(*) FROM inventory WHERE category='subdomain' AND first_seen_at < ?)",
				"(SELECT COUNT(*) FROM inventory WHERE category='ip' AND first_seen_at < ?)",
				"(SELECT COUNT(*) FROM inventory WHERE category='service' AND first_seen_at < ?)",
				"(SELECT COUNT(*) FROM endpoint WHERE created_at < ?)",
				"(SELECT COUNT(*) FROM website WHERE created_at < ?)",
				"(SELECT COUNT(*) FROM vulnerability WHERE created_at < ?)",
			)
			for range 7 {
				args = append(args, day.AddDate(0, 0, 1))
			}
			destinations = append(destinations, &item.TotalTargets, &item.TotalSubdomains, &item.TotalIPs, &item.TotalServices, &item.TotalEndpoints, &item.TotalWebsites, &item.TotalVulns)
		}
		query := fmt.Sprintf(`WITH inventory AS (SELECT category,label,MIN(first_seen_at) AS first_seen_at FROM asset_inventory_visible_source WHERE tenant_id='00000000-0000-0000-0000-000000000001' GROUP BY category,label) SELECT %s`, strings.Join(selects, ","))
		if err := tx.Raw(query, args...).Row().Scan(destinations...); err != nil {
			return err
		}
		for i := range history {
			history[i].TotalAssets = history[i].TotalSubdomains + history[i].TotalIPs + history[i].TotalServices
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return history, nil
}

func queryCurrentAssetStatistics(tx *gorm.DB, changeSince time.Time) (assetStatisticsRow, error) {
	var row assetStatisticsRow
	err := tx.Raw(`
		WITH inventory AS (SELECT category,label,MIN(first_seen_at) AS first_seen_at FROM asset_inventory_visible_source WHERE tenant_id='00000000-0000-0000-0000-000000000001' GROUP BY category,label)
		SELECT
			(SELECT COUNT(*) FROM target WHERE deleted_at IS NULL) AS total_targets,
			(SELECT COUNT(*) FROM inventory WHERE category='subdomain') AS total_subdomains,
			(SELECT COUNT(*) FROM inventory WHERE category='ip') AS total_ips,
			(SELECT COUNT(*) FROM inventory WHERE category='service') AS total_services,
			(SELECT COUNT(*) FROM endpoint) AS total_endpoints,
			(SELECT COUNT(*) FROM website) AS total_websites,
			(SELECT COUNT(*) FROM vulnerability) AS total_vulns,
			(SELECT COUNT(*) FROM scan WHERE deleted_at IS NULL AND status = 'running') AS running_scans,
			(SELECT COUNT(*) FROM target WHERE deleted_at IS NULL AND created_at >= ?) AS change_targets,
			(SELECT COUNT(*) FROM inventory WHERE category='subdomain' AND first_seen_at >= ?) AS change_subdomains,
			(SELECT COUNT(*) FROM inventory WHERE category='ip' AND first_seen_at >= ?) AS change_ips,
			(SELECT COUNT(*) FROM inventory WHERE category='service' AND first_seen_at >= ?) AS change_services,
			(SELECT COUNT(*) FROM endpoint WHERE created_at >= ?) AS change_endpoints,
			(SELECT COUNT(*) FROM website WHERE created_at >= ?) AS change_websites,
			(SELECT COUNT(*) FROM vulnerability WHERE created_at >= ?) AS change_vulns
	`, changeSince, changeSince, changeSince, changeSince, changeSince, changeSince, changeSince).Scan(&row).Error
	return row, err
}

func assetStatisticsFromRow(row assetStatisticsRow) assetdomain.AssetStatistics {
	return assetdomain.AssetStatistics{
		TotalTargets:     row.TotalTargets,
		TotalSubdomains:  row.TotalSubdomains,
		TotalIPs:         row.TotalIPs,
		TotalServices:    row.TotalServices,
		TotalEndpoints:   row.TotalEndpoints,
		TotalWebsites:    row.TotalWebsites,
		TotalVulns:       row.TotalVulns,
		TotalAssets:      row.TotalSubdomains + row.TotalIPs + row.TotalServices,
		RunningScans:     row.RunningScans,
		ChangeTargets:    row.ChangeTargets,
		ChangeSubdomains: row.ChangeSubdomains,
		ChangeIPs:        row.ChangeIPs,
		ChangeServices:   row.ChangeServices,
		ChangeEndpoints:  row.ChangeEndpoints,
		ChangeWebsites:   row.ChangeWebsites,
		ChangeVulns:      row.ChangeVulns,
		ChangeAssets:     row.ChangeSubdomains + row.ChangeIPs + row.ChangeServices,
	}
}

func mapVulnerabilitySeverityCounts(rows []vulnerabilitySeverityRow) assetdomain.VulnerabilitySeverityCounts {
	var counts assetdomain.VulnerabilitySeverityCounts
	for _, row := range rows {
		switch strings.ToLower(strings.TrimSpace(row.Severity)) {
		case "critical":
			counts.Critical = row.Count
		case "high":
			counts.High = row.Count
		case "medium":
			counts.Medium = row.Count
		case "low":
			counts.Low = row.Count
		case "info":
			counts.Info = row.Count
		}
	}
	return counts
}

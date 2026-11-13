package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	resultwiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/resultingest"
	resultapp "github.com/yyhuni/lunafox/server/internal/modules/resultingest/application"
	snapshotrepo "github.com/yyhuni/lunafox/server/internal/modules/snapshot/repository"
	"github.com/yyhuni/lunafox/server/internal/pkg/dbtx"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"testing"
)

func verifyLiveScanResultsPostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	target := targetCleanupGateCreateTarget(t, orm, "live-results-fixture.example")
	scan, task, scope := targetCleanupGateSeedResultExecution(t, orm, target, "live-results-fixture")
	coordinator := resultwiring.NewResultIngestMaterializationCoordinator(orm)
	url := "https://live-results-fixture.example/live"
	if err := coordinator.Materialize(ctx, scope, func(txCtx context.Context) error {
		tx := dbtx.Resolve(txCtx, orm).WithContext(txCtx)
		if err := tx.Exec(`INSERT INTO website_snapshot(scan_id,url,host,status_code) VALUES(?,?,?,200)`, scan, url, "live-results-fixture.example").Error; err != nil {
			return err
		}
		return tx.Exec(`INSERT INTO website(target_id,url,host,status_code) VALUES(?,?,?,200)`, target, url, "live-results-fixture.example").Error
	}); err != nil {
		t.Fatal(err)
	}
	var persisted int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM website_snapshot w JOIN scan_task st ON st.scan_id=w.scan_id WHERE st.id=$1 AND st.status='running' AND w.url=$2`, task, url).Scan(&persisted); err != nil || persisted != 1 {
		t.Fatalf("running task result not visible: %d %v", persisted, err)
	}
	for i, status := range []int{302, 403, 502, 400} {
		if _, err := db.ExecContext(ctx, `INSERT INTO website_snapshot(scan_id,url,host,status_code) VALUES($1,$2,'live-results-fixture.example',$3)`, scan, fmt.Sprintf("https://live-results-fixture.example/%d", i), status); err != nil {
			t.Fatal(err)
		}
	}
	var responsive []string
	if err := snapshotrepo.NewWebsiteSnapshotRepository(orm).ForEachResponsiveWebsiteURLByScanID(ctx, scan, func(url string) error { responsive = append(responsive, url); return nil }); err != nil || len(responsive) != 2 {
		t.Fatalf("responsive seed filter: %v %v", responsive, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE scan_task SET status='cancelled',completed_at=now() WHERE id=$1`, task); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE scan SET status='cancelled',stopped_at=now() WHERE id=$1`, scan); err != nil {
		t.Fatal(err)
	}
	lateWrite := false
	err = coordinator.Materialize(ctx, scope, func(context.Context) error { lateWrite = true; return nil })
	if !errors.Is(err, resultapp.ErrResultExecutionFenceRejected) || lateWrite {
		t.Fatalf("cancelled task accepted late batch: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM website w JOIN website_snapshot s ON s.url=w.url WHERE w.target_id=$1 AND s.scan_id=$2 AND w.url=$3`, target, scan, url).Scan(&persisted); err != nil || persisted != 1 {
		t.Fatalf("acknowledged live result lost after cancellation: %d %v", persisted, err)
	}
}

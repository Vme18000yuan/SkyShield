package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/auth"
	"github.com/yyhuni/lunafox/server/internal/geolocation"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	monitorapp "github.com/yyhuni/lunafox/server/internal/modules/assetmonitor/application"
	monitorrepo "github.com/yyhuni/lunafox/server/internal/modules/assetmonitor/repository"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	assethandler "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/handler"
	assetrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
	assetrouter "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/router"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyRootDomainPostgresContract(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var user, orgA, orgB, targetA, targetB, targetUK int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		name string
		id   *int
	}{{"root-org-a", &orgA}, {"root-org-b", &orgB}} {
		if err := db.QueryRowContext(ctx, `INSERT INTO organization(name) VALUES($1) RETURNING id`, entry.name).Scan(entry.id); err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range []struct {
		name string
		id   *int
	}{{"skyshield.example", &targetA}, {"team.skyshield.example", &targetB}, {"portal.example.co.uk", &targetUK}} {
		if err := db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES($1,'domain') RETURNING id`, entry.name).Scan(entry.id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO organization_target(organization_id,target_id) VALUES($1,$3),($2,$4)`, orgA, orgB, targetA, targetB); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO subdomain(target_id,dns_name) VALUES($1,'z.skyshield.example'),($2,'device.team.skyshield.example')`, targetA, targetB); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO website(target_id,url,host) VALUES($1,'https://z.skyshield.example/login','z.skyshield.example')`, targetA); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO host_port_mapping(target_id,host,ip,port) VALUES($1,'z.skyshield.example','8.8.4.4',443)`, targetA); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := assetrepo.NewAssetRegistryStore(orm)
	drain := func() {
		t.Helper()
		for i := 0; i < 1000; i++ {
			worked, err := store.ProcessRootDomainBatch(ctx, 40)
			if err != nil {
				t.Fatalf("root projection: %v", err)
			}
			if !worked {
				return
			}
		}
		t.Fatal("root projection did not converge")
	}
	// Exercise the same managed worker used by the server, including shutdown.
	jobCtx, stopJob := context.WithCancel(ctx)
	controller := assetapp.NewRootDomainController(store)
	controller.Start(jobCtx)
	deadline := time.Now().Add(5 * time.Second)
	var produced bool
	for time.Now().Before(deadline) {
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM asset_entity WHERE asset_type='root_domain' AND label='skyshield.example')`).Scan(&produced); err != nil {
			stopJob()
			t.Fatal(err)
		}
		if produced {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	stopJob()
	select {
	case <-controller.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("root projection worker did not stop")
	}
	if !produced {
		t.Fatal("root projection worker did not create the historical target root")
	}
	drain()
	// An ingestion transaction must not wait on an older event locked by the
	// projector. Its new append-only event is processed after that lock releases.
	if _, err := db.ExecContext(ctx, `UPDATE target SET name=name WHERE id=$1`, targetA); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var eventID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM asset_root_projection_queue WHERE source_kind='target' AND source_id=$1 ORDER BY id LIMIT 1 FOR UPDATE`, targetA).Scan(&eventID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	writeCtx, stopWrite := context.WithTimeout(ctx, 2*time.Second)
	_, writeErr := db.ExecContext(writeCtx, `UPDATE target SET name=name WHERE id=$1`, targetA)
	stopWrite()
	_ = tx.Rollback()
	if writeErr != nil {
		t.Fatalf("new source update waited for a locked projection event: %v", writeErr)
	}
	drain()
	// Only one server projects at a time; another instance can retry without
	// overwriting a fresher association.
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1380929364,1146047809)`); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	worked, err := store.ProcessRootDomainBatch(ctx, 40)
	_ = tx.Rollback()
	if err != nil || worked {
		t.Fatalf("projection did not honor single-writer fencing: %v %v", worked, err)
	}
	lookup := func(query string, target int) []assetapp.AssetEntity {
		t.Helper()
		items, err := store.ListAssets(ctx, assetapp.AssetQuery{TenantID: assetapp.DefaultTenantID, AssetType: "root_domain", Query: query, TargetID: target, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		return items
	}
	items := lookup("skyshield.example", 0)
	if len(items) != 1 || items[0].Label != "skyshield.example" || !items[0].Derived || items[0].SourceCount < 4 {
		t.Fatalf("root from target and subdomains: %+v", items)
	}
	rootID := items[0].ID
	observedLater := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	if _, err := db.ExecContext(ctx, `UPDATE asset_entity SET last_seen_at=$1 WHERE asset_type='subdomain' AND label='z.skyshield.example'`, observedLater); err != nil {
		t.Fatal(err)
	}
	items = lookup("skyshield.example", 0)
	if len(items) != 1 || !items[0].LastSeenAt.Equal(observedLater) {
		t.Fatalf("root did not follow a repeated child observation: %+v", items)
	}
	if items = lookup("skyshield.example", targetA); len(items) != 1 || items[0].ID != rootID {
		t.Fatalf("root target scope: %+v", items)
	}
	if items = lookup("skyshield.example", targetB); len(items) != 0 {
		t.Fatalf("inferred parent widened subdomain target scope: %+v", items)
	}
	tasks := aiapp.NewTaskFacade(airepo.NewTaskStore(orm), nil, nil)
	if _, err := tasks.Create(ctx, aiapp.CreateTaskInput{UserID: user, Goal: "验证根域", TargetID: targetA, AssetIDs: []int64{rootID}}); err != nil {
		t.Fatalf("exact root target rejected its derived asset: %v", err)
	}
	if _, err := tasks.Create(ctx, aiapp.CreateTaskInput{UserID: user, Goal: "不得扩大范围", TargetID: targetB, AssetIDs: []int64{rootID}}); !errors.Is(err, assetapp.ErrAssetOutsideTarget) {
		t.Fatalf("child target accepted parent root: %v", err)
	}
	if items = lookup("example.co.uk", 0); len(items) != 1 || items[0].Label != "example.co.uk" {
		t.Fatalf("multi-label public suffix: %+v", items)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM target WHERE name='example.co.uk' AND deleted_at IS NULL`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("root projection created a scan target: %d %v", count, err)
	}
	registration := assetapp.NewRegistrationFacade(assetrepo.NewAssetRegistrationStore(orm, assetapp.LegacyAssetCreators{}), nil)
	manualChild, err := registration.Register(ctx, assetapp.BatchRegisterInput{UserID: user, Origin: "manual", RequestID: "standalone-subdomain-root", Assets: []assetapp.AssetInput{{Type: "subdomain", Domain: "one.manualscope.example", OrganizationID: orgB}}})
	if err != nil || len(manualChild.Results) != 1 || len(manualChild.Errors) != 0 {
		t.Fatalf("standalone subdomain: %+v %v", manualChild, err)
	}
	drain()
	if items = lookup("manualscope.example", 0); len(items) != 1 || !items[0].Derived {
		t.Fatalf("manual subdomain root not projected: %+v", items)
	}
	monitorStore := monitorrepo.NewStore(orm)
	if err := monitorStore.SaveLocation(ctx, geolocation.Location{ObservedIP: "8.8.4.4", Latitude: 39.9, Longitude: 116.4, ProviderKey: geolocation.FreeIPAPIProviderKey, ResolvedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	monitor := monitorapp.NewFacade(monitorStore, nil)
	snapshot, err := monitor.Read(ctx, user, []int{orgA})
	if err != nil {
		t.Fatal(err)
	}
	var roots int64
	for _, item := range snapshot.AssetTypes {
		if item.Key == "root_domain" {
			roots = item.Count
		}
	}
	if roots != 1 {
		t.Fatalf("organization monitor missing derived root: %+v", snapshot.AssetTypes)
	}
	if snapshot.Summary.TotalAssets != 4 || snapshot.Summary.LocatedAssets != 4 {
		t.Fatalf("root did not inherit selected source geography: %+v", snapshot.Summary)
	}
	manager := auth.NewJWTManager("fixture-jwt-secret", time.Minute, time.Hour)
	token, _, err := manager.GenerateAccessToken(user, "registry-admin", 0)
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	group := engine.Group("/v1")
	group.Use(middleware.AuthMiddleware(manager, fixtureTokenVersions{}))
	assetrouter.RegisterAssetRegistryRoutes(group, assethandler.NewAssetEntityHandler(assetapp.NewFacade(store)), nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/assetEntities?assetType=root_domain&q=skyshield.example", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	out := httptest.NewRecorder()
	engine.ServeHTTP(out, req)
	if out.Code != http.StatusOK || !strings.Contains(out.Body.String(), `"label":"skyshield.example"`) || !strings.Contains(out.Body.String(), `"derived":true`) {
		t.Fatalf("root domain API: %d %s", out.Code, out.Body.String())
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO subdomain(target_id,dns_name) VALUES($1,'fresh.skyshield.example')`, targetA); err != nil {
		t.Fatal(err)
	}
	drain()
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM asset_root_domain_source r JOIN asset_entity e ON e.id=r.source_asset_id WHERE e.label='fresh.skyshield.example' AND r.root_asset_id=$1`, rootID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("new subdomain not projected: %d %v", count, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE target SET name='service.changed.co.uk' WHERE id=$1`, targetUK); err != nil {
		t.Fatal(err)
	}
	drain()
	if items = lookup("changed.co.uk", 0); len(items) != 1 || items[0].Label != "changed.co.uk" {
		t.Fatalf("updated target not reprojected: %+v", items)
	}
	if items = lookup("example.co.uk", 0); len(items) != 0 {
		t.Fatalf("renamed target retained orphan root: %+v", items)
	}
	if _, err := db.ExecContext(ctx, `UPDATE target SET deleted_at=CURRENT_TIMESTAMP WHERE id=$1`, targetA); err != nil {
		t.Fatal(err)
	}
	if items = lookup("skyshield.example", 0); len(items) != 1 {
		t.Fatalf("shared root vanished when one target was deleted: %+v", items)
	}
	if _, err := db.ExecContext(ctx, `UPDATE target SET deleted_at=CURRENT_TIMESTAMP WHERE id=$1`, targetB); err != nil {
		t.Fatal(err)
	}
	if items = lookup("skyshield.example", 0); len(items) != 0 {
		t.Fatalf("deleted-only root stayed visible: %+v", items)
	}
	snapshot, err = monitor.Read(ctx, user, []int{orgB})
	if err != nil {
		t.Fatal(err)
	}
	roots = 0
	for _, item := range snapshot.AssetTypes {
		if item.Key == "root_domain" {
			roots = item.Count
		}
	}
	if roots != 1 {
		t.Fatalf("standalone organization root lost with deleted target: %+v", snapshot.AssetTypes)
	}
	registered, err := registration.Register(ctx, assetapp.BatchRegisterInput{UserID: user, Origin: "manual", RequestID: "explicit-root-reuse", Assets: []assetapp.AssetInput{{Type: "root_domain", Domain: "skyshield.example"}}})
	if err != nil || len(registered.Results) != 1 || len(registered.Errors) != 0 {
		t.Fatalf("explicit root registration: %+v %v", registered, err)
	}
	items = lookup("skyshield.example", 0)
	if len(items) != 1 || items[0].ID != rootID || items[0].Derived {
		t.Fatalf("explicit root duplicated or stayed marked derived: %+v", items)
	}
	encoded, _ := json.Marshal(registered)
	if !strings.Contains(string(encoded), registered.Results[0].ID) {
		t.Fatalf("unexpected registration response: %s", encoded)
	}
}

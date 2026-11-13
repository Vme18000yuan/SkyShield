package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/auth"
	"github.com/yyhuni/lunafox/server/internal/geolocation"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	monitor "github.com/yyhuni/lunafox/server/internal/modules/assetmonitor/application"
	monitorhandler "github.com/yyhuni/lunafox/server/internal/modules/assetmonitor/handler"
	monitorrepo "github.com/yyhuni/lunafox/server/internal/modules/assetmonitor/repository"
	monitorrouter "github.com/yyhuni/lunafox/server/internal/modules/assetmonitor/router"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyAssetMonitorPostgresContract(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var user, orgA, orgB, target, hidden int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO organization(name) VALUES('monitor-a') RETURNING id`).Scan(&orgA); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO organization(name) VALUES('monitor-b') RETURNING id`).Scan(&orgB); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES('monitor.example','domain') RETURNING id`).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES('hidden-monitor.example','domain') RETURNING id`).Scan(&hidden); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO organization_target(organization_id,target_id) VALUES($1,$3),($2,$3)`, orgA, orgB, target); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO website(target_id,url,host,tech) VALUES($1,'https://monitor.example/','monitor.example',ARRAY['Nginx']),($2,'https://hidden-monitor.example/','hidden-monitor.example',ARRAY['HiddenTech'])`, target, hidden); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO subdomain(target_id,dns_name) VALUES($1,'unlocated.monitor.example')`, target); err != nil {
		t.Fatal(err)
	}
	var privateHost int
	if _, err := db.ExecContext(ctx, `INSERT INTO host_port_mapping(target_id,host,ip,port) VALUES($1,'monitor.example','8.8.8.8',443)`, target); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO host_port_mapping(target_id,host,ip,port) VALUES($1,'private.monitor.example','10.0.0.1',8080) RETURNING id`, target).Scan(&privateHost); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO vulnerability(target_id,url,vuln_type,severity) VALUES($1,'https://monitor.example/','monitor-fixture','critical'),($2,'https://hidden-monitor.example/','hidden-fixture','high')`, target, hidden); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO asset_observation(tenant_id,asset_id,target_id,original_target_id,organization_id,created_by,origin,source_key,request_fingerprint,payload)
 SELECT '00000000-0000-0000-0000-000000000001',b.asset_id,$1,$1,$2,$3,'import','monitor-country-fixture','fixture','{"extra":{"country":"CN","countryName":"China"}}'::jsonb FROM asset_host_port_binding b WHERE b.host_port_id=$4`, target, orgA, user, privateHost); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := monitorrepo.NewStore(orm)
	facade := monitor.NewFacade(store, nil)
	now := time.Now().UTC()
	if err := store.SaveLocation(ctx, geolocation.Location{ObservedIP: "8.8.8.8", Latitude: 37.4, Longitude: -122.1, ProviderKey: geolocation.FreeIPAPIProviderKey, ResolvedAt: now}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := facade.Read(ctx, user, []int{orgA, orgB})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Summary.TotalAssets != 7 || snapshot.Summary.Targets != 1 || snapshot.Summary.Risks != 1 || snapshot.Summary.LocatedAssets != 6 || snapshot.Summary.UnlocatedAssets != 1 {
		t.Fatalf("scoped snapshot: %+v", snapshot)
	}
	if len(snapshot.Organizations) != 2 || snapshot.Organizations[0].AssetCount != 7 || snapshot.Organizations[1].AssetCount != 7 {
		t.Fatalf("shared-target organization totals: %+v", snapshot.Organizations)
	}
	if len(snapshot.Technologies) != 1 || snapshot.Technologies[0].Key != "Nginx" {
		t.Fatalf("unselected organization leaked: %+v", snapshot.Technologies)
	}
	if len(snapshot.Trend) != 7 || len(snapshot.RecentAssets) != 7 {
		t.Fatalf("snapshot history/recent: %+v", snapshot)
	}
	manager := auth.NewJWTManager("monitor-fixture-secret", time.Minute, time.Hour)
	token, _, err := manager.GenerateAccessToken(user, "registry-admin", 0)
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	group := engine.Group("/v1")
	group.Use(middleware.AuthMiddleware(manager, fixtureTokenVersions{}))
	monitorrouter.RegisterRoutes(group, monitorhandler.NewHandler(facade))
	request := func(query, credential string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/v1/assetMonitors/current"+query, nil)
		if credential != "" {
			req.Header.Set("Authorization", "Bearer "+credential)
		}
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	if response := request(fmt.Sprintf("?organizationIds=%d,%d", orgA, orgB), token); response.Code != http.StatusOK || strings.Contains(response.Body.String(), "HiddenTech") || !strings.Contains(response.Body.String(), `"totalAssets":7`) {
		t.Fatalf("monitor endpoint: %d %s", response.Code, response.Body.String())
	}
	if response := request(fmt.Sprintf("?organizationIds=%d", orgA), ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous monitor: %d", response.Code)
	}
	if response := request("", token); response.Code != http.StatusBadRequest {
		t.Fatalf("missing monitor scope: %d", response.Code)
	}
	if response := request(fmt.Sprintf("?organizationIds=%d,%d", orgA, orgA), token); response.Code != http.StatusBadRequest {
		t.Fatalf("duplicate monitor scope: %d", response.Code)
	}
	if response := request("?organizationIds=2147483647", token); response.Code != http.StatusNotFound {
		t.Fatalf("missing organization: %d", response.Code)
	}
	if _, err := facade.Read(ctx, user, []int{orgA, orgA}); !errors.Is(err, monitor.ErrInvalidSelection) {
		t.Fatalf("duplicate selection accepted: %v", err)
	}
	if _, err := facade.Read(ctx, user, []int{999999999}); !errors.Is(err, monitor.ErrOrganizationNotFound) {
		t.Fatalf("missing selection expanded globally: %v", err)
	}
	if _, err := facade.Read(ctx, 999999999, []int{orgA}); !errors.Is(err, monitor.ErrForbidden) {
		t.Fatalf("nonmember accessed snapshot: %v", err)
	}
	claimed, err := store.ClaimIP(ctx, "1.1.1.1")
	if err != nil || !claimed {
		t.Fatalf("first geo admission: %v %v", claimed, err)
	}
	claimed, err = store.ClaimIP(ctx, "1.1.1.1")
	if err != nil || claimed {
		t.Fatalf("duplicate geo admission: %v %v", claimed, err)
	}
	claimed, err = store.ClaimIP(ctx, "10.0.0.1")
	if err != nil || claimed {
		t.Fatalf("private IP geo admission: %v %v", claimed, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE target SET deleted_at=CURRENT_TIMESTAMP WHERE id=$1`, target); err != nil {
		t.Fatal(err)
	}
	snapshot, err = facade.Read(ctx, user, []int{orgA, orgB})
	if err != nil || snapshot.Summary.TotalAssets != 0 || snapshot.Summary.Risks != 0 {
		t.Fatalf("deleted target remains on monitor: %+v %v", snapshot, err)
	}
}

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/auth"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	assethandler "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/handler"
	assetrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
	assetrouter "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/router"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyAssetInventoryUIPostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var user, orgA, orgB, targetA, targetB int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO organization(name) VALUES('inventory-ui-org-a') RETURNING id`).Scan(&orgA); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO organization(name) VALUES('inventory-ui-org-b') RETURNING id`).Scan(&orgB); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES('inventoryscope-one.com','domain') RETURNING id`).Scan(&targetA); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES('inventoryscope-two.com','domain') RETURNING id`).Scan(&targetB); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO organization_target(organization_id,target_id) VALUES($1,$3),($2,$4)`, orgA, orgB, targetA, targetB); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO subdomain(target_id,dns_name) SELECT $1,'node'||n||'.inventoryscope-one.com' FROM generate_series(1,55)n`, targetA); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO subdomain(target_id,dns_name) VALUES($1,'www.inventoryscope-one.com'),($2,'other.inventoryscope-two.com')`, targetA, targetB); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO website(target_id,url,host) VALUES($1,'http://www.inventoryscope-one.com/','www.inventoryscope-one.com')`, targetA); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO endpoint(target_id,url,host) VALUES($1,'http://www.inventoryscope-one.com/api','www.inventoryscope-one.com')`, targetA); err != nil {
		t.Fatal(err)
	}
	for _, port := range []int{21, 22, 1883, 3306} {
		if _, err := db.ExecContext(ctx, `INSERT INTO host_port_mapping(target_id,host,ip,port) VALUES($1,'www.inventoryscope-one.com','8.8.8.8',$2)`, targetA, port); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO host_port_mapping(target_id,host,ip,port) VALUES($1,'other.inventoryscope-two.com','8.8.8.8',3306)`, targetB); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := assetrepo.NewAssetRegistryStore(orm)
	registration := assetapp.NewRegistrationFacade(assetrepo.NewAssetRegistrationStore(orm, assetapp.LegacyAssetCreators{}), nil)
	created, err := registration.Register(ctx, assetapp.BatchRegisterInput{UserID: user, Origin: "manual", RequestID: "inventory-ui-mqtt", Assets: []assetapp.AssetInput{{Type: "service", TargetID: targetA, OrganizationID: orgA, Domain: "www.inventoryscope-one.com", ServiceName: "mqtt", Port: 1883, Proto: "tcp"}}})
	if err != nil || len(created.Results) != 1 || len(created.Errors) != 0 {
		t.Fatalf("register observed service: %+v %v", created, err)
	}
	ipAsset, err := registration.Register(ctx, assetapp.BatchRegisterInput{UserID: user, Origin: "manual", RequestID: "inventory-ui-ip-ports", Assets: []assetapp.AssetInput{{Type: "ip", IP: "1.1.1.1", OrganizationID: orgA, BoundDomains: []string{"www.inventoryscope-one.com"}, OpenPorts: []assetapp.PortObservation{{Port: 6379, Service: "redis", Protocol: "tcp"}}}}})
	if err != nil || len(ipAsset.Results) != 1 || len(ipAsset.Errors) != 0 {
		t.Fatalf("register IP port: %+v %v", ipAsset, err)
	}
	appAsset, err := registration.Register(ctx, assetapp.BatchRegisterInput{UserID: user, Origin: "manual", RequestID: "inventory-ui-app", Assets: []assetapp.AssetInput{{Type: "app", AppName: "Inventory App", OrganizationID: orgA, BoundDomains: []string{"www.inventoryscope-one.com"}}}})
	if err != nil || len(appAsset.Results) != 1 || len(appAsset.Errors) != 0 {
		t.Fatalf("register related app: %+v %v", appAsset, err)
	}
	appOther, err := registration.Register(ctx, assetapp.BatchRegisterInput{UserID: user, Origin: "manual", RequestID: "inventory-ui-other-app", Assets: []assetapp.AssetInput{{Type: "app", AppName: "Other Org App", OrganizationID: orgB, BoundDomains: []string{"www.inventoryscope-one.com"}}}})
	if err != nil || len(appOther.Results) != 1 || len(appOther.Errors) != 0 {
		t.Fatalf("register other organization app: %+v %v", appOther, err)
	}
	for i := 0; i < 1000; i++ {
		worked, e := store.ProcessRootDomainBatch(ctx, 100)
		if e != nil {
			t.Fatal(e)
		}
		if !worked {
			break
		}
		if i == 999 {
			t.Fatal("root projection did not finish")
		}
	}
	var rootA, rootB int64
	if err = db.QueryRowContext(ctx, `SELECT id FROM asset_entity WHERE asset_type='root_domain' AND label='inventoryscope-one.com'`).Scan(&rootA); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT id FROM asset_entity WHERE asset_type='root_domain' AND label='inventoryscope-two.com'`).Scan(&rootB); err != nil {
		t.Fatal(err)
	}
	facade := assetapp.NewFacade(store)
	rootFromOther, e := facade.List(ctx, assetapp.ListInput{UserID: user, OrganizationID: orgB, AssetType: "root_domain", AssetID: rootA, Page: 1, PageSize: 10})
	if e != nil || rootFromOther.TotalSize != 1 {
		t.Fatalf("root should be visible through explicitly bound app: %+v %v", rootFromOther, e)
	}
	page, e := facade.List(ctx, assetapp.ListInput{UserID: user, OrganizationID: orgA, RootDomainID: rootA, AssetType: "subdomain", Page: 6, PageSize: 10})
	if e != nil || page.TotalSize != 56 || len(page.Items) != 6 {
		t.Fatalf("page 6: %+v %v", page, e)
	}
	for _, item := range page.Items {
		if len(item.Organizations) != 1 || item.Organizations[0].ID != orgA {
			t.Fatalf("organization attribution: %+v", item)
		}
	}
	wrong, e := facade.List(ctx, assetapp.ListInput{UserID: user, OrganizationID: orgB, RootDomainID: rootA, AssetType: "subdomain", Page: 1, PageSize: 10})
	if e != nil || wrong.TotalSize != 0 {
		t.Fatalf("organization/root scope leak: %+v %v", wrong, e)
	}
	ports, e := facade.ListPorts(ctx, assetapp.PortListInput{UserID: user, OrganizationID: orgA, RootDomainID: rootA, Page: 1, PageSize: 50})
	if e != nil {
		t.Fatal(e)
	}
	if ports.TotalSize != 10 {
		t.Fatalf("expected 10 distinct host:port observations: %+v", ports)
	}
	found := map[string]assetapp.PortAsset{}
	for _, p := range ports.Items {
		found[p.Label] = p
	}
	for label, want := range map[string]string{"www.inventoryscope-one.com:21": "FTP", "www.inventoryscope-one.com:22": "SSH", "www.inventoryscope-one.com:1883": "MQTT", "8.8.8.8:3306": "MYSQL", "1.1.1.1:6379": "REDIS", "www.inventoryscope-one.com:80": "HTTP"} {
		if found[label].Service != want {
			t.Fatalf("service %s = %+v, want %s", label, found[label], want)
		}
	}
	if found["www.inventoryscope-one.com:1883"].ServiceSource != "observed" || found["8.8.8.8:3306"].ServiceSource != "port" {
		t.Fatalf("service evidence: %+v", found)
	}
	combined, e := facade.ListInventory(ctx, assetapp.PortListInput{UserID: user, OrganizationID: orgA, RootDomainID: rootA, Page: 9, PageSize: 7})
	if e != nil || combined.TotalSize != 71 || len(combined.Items) != 7 {
		t.Fatalf("combined inventory page: %+v %v", combined, e)
	}
	assetRows, portRows := 0, 0
	for _, row := range combined.Items {
		if row.Asset != nil {
			assetRows++
		}
		if row.Port != nil {
			portRows++
		}
	}
	if assetRows != 5 || portRows != 2 {
		t.Fatalf("combined page boundary: %d assets and %d ports", assetRows, portRows)
	}
	last, e := facade.ListInventory(ctx, assetapp.PortListInput{UserID: user, OrganizationID: orgA, RootDomainID: rootA, Page: 10, PageSize: 7})
	if e != nil || last.TotalSize != 71 || len(last.Items) != 7 {
		t.Fatalf("combined final page: %+v %v", last, e)
	}
	wrongPorts, e := facade.ListPorts(ctx, assetapp.PortListInput{UserID: user, OrganizationID: orgA, RootDomainID: rootB, Page: 1, PageSize: 20})
	if e != nil || wrongPorts.TotalSize != 0 {
		t.Fatalf("port scope leak: %+v %v", wrongPorts, e)
	}
	manager := auth.NewJWTManager("inventory-ui-fixture-secret", time.Minute, time.Hour)
	token, _, e := manager.GenerateAccessToken(user, "registry-admin", 0)
	if e != nil {
		t.Fatal(e)
	}
	engine := gin.New()
	group := engine.Group("/v1")
	group.Use(middleware.AuthMiddleware(manager, fixtureTokenVersions{}))
	assetrouter.RegisterAssetRegistryRoutes(group, assethandler.NewAssetEntityHandler(facade))
	request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/v1/assetEntities/%d/rootDomainOverview?organizationId=%d", rootA, orgA), nil)
	request.Header.Set("Authorization", "Bearer "+token)
	out := httptest.NewRecorder()
	engine.ServeHTTP(out, request)
	var body struct {
		Domain string           `json:"domain"`
		Counts map[string]int64 `json:"counts"`
	}
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &body) != nil || body.Domain != "inventoryscope-one.com" || body.Counts["subdomains"] != 56 || body.Counts["ips"] != 2 || body.Counts["apps"] != 1 || body.Counts["ports"] != 10 {
		t.Fatalf("root detail: %d %s", out.Code, out.Body.String())
	}
}

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/auth"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	monitorapp "github.com/yyhuni/lunafox/server/internal/modules/assetmonitor/application"
	monitorrepo "github.com/yyhuni/lunafox/server/internal/modules/assetmonitor/repository"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	assethandler "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/handler"
	assetrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
	assetrouter "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/router"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyAssetWebClassificationPostgresContract(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var user, target, organization int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES('web-category.example','domain') RETURNING id`).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO organization(name) VALUES('web-category-org') RETURNING id`).Scan(&organization); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO organization_target(organization_id,target_id) VALUES($1,$2)`, organization, target); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"HTTP://web-category.example/", "https://web-category.example/login"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO website(target_id,url,host) VALUES($1,$2,'web-category.example')`, target, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{"http://web-category.example/", "https://web-category.example/api/v1"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO endpoint(target_id,url,host) VALUES($1,$2,'web-category.example')`, target, value); err != nil {
			t.Fatal(err)
		}
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := assetrepo.NewAssetRegistryStore(orm)
	registration := assetapp.NewRegistrationFacade(assetrepo.NewAssetRegistrationStore(orm, assetapp.LegacyAssetCreators{}), nil)
	created, err := registration.Register(ctx, assetapp.BatchRegisterInput{UserID: user, Origin: "manual", RequestID: "web-category-ssh", Assets: []assetapp.AssetInput{{Type: "service", TargetID: target, Domain: "web-category.example", ServiceName: "SSH", Port: 22, Proto: "tcp"}}})
	if err != nil || len(created.Results) != 1 || len(created.Errors) != 0 {
		t.Fatalf("non-Web service: %+v %v", created, err)
	}
	facade := assetapp.NewFacade(store)
	page, err := facade.List(ctx, assetapp.ListInput{UserID: user, AssetCategory: "web", Query: "web-category.example", PageSize: 2})
	if err != nil || len(page.Items) != 2 || page.NextPageToken == "" {
		t.Fatalf("first Web page: %+v %v", page, err)
	}
	other, err := facade.List(ctx, assetapp.ListInput{UserID: user, AssetCategory: "web", Query: "web-category.example", PageSize: 2, PageToken: page.NextPageToken})
	if err != nil || len(other.Items) != 2 || other.NextPageToken != "" {
		t.Fatalf("second Web page: %+v %v", other, err)
	}
	all := append(page.Items, other.Items...)
	types := map[string]int{}
	ids := map[int64]bool{}
	for _, item := range all {
		if item.DisplayType != "web" || ids[item.ID] {
			t.Fatalf("Web classification or pagination incorrect: %+v", all)
		}
		ids[item.ID] = true
		types[item.AssetType]++
	}
	if types["service"] != 2 || types["endpoint"] != 2 {
		t.Fatalf("canonical site and endpoint identity changed: %+v", types)
	}
	if _, err := facade.List(ctx, assetapp.ListInput{UserID: user, AssetCategory: "non_web_service", Query: "web-category.example", PageSize: 2, PageToken: page.NextPageToken}); !errors.Is(err, assetapp.ErrInvalidQuery) {
		t.Fatalf("category cursor accepted for another filter: %v", err)
	}
	services, err := facade.List(ctx, assetapp.ListInput{UserID: user, AssetCategory: "non_web_service", Query: "web-category.example"})
	if err != nil || len(services.Items) != 1 || services.Items[0].DisplayType != "service" || services.Items[0].Label != "web-category.example:22" {
		t.Fatalf("other service category: %+v %v", services, err)
	}
	canonical, err := facade.List(ctx, assetapp.ListInput{UserID: user, AssetType: "service", Query: "web-category.example"})
	if err != nil || len(canonical.Items) != 3 {
		t.Fatalf("canonical service API changed: %+v %v", canonical, err)
	}
	endpoints, err := facade.List(ctx, assetapp.ListInput{UserID: user, AssetType: "endpoint", Query: "web-category.example"})
	if err != nil || len(endpoints.Items) != 2 {
		t.Fatalf("URL subtype query: %+v %v", endpoints, err)
	}
	manager := auth.NewJWTManager("fixture-jwt-secret", time.Minute, time.Hour)
	token, _, err := manager.GenerateAccessToken(user, "registry-admin", 0)
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	group := engine.Group("/v1")
	group.Use(middleware.AuthMiddleware(manager, fixtureTokenVersions{}))
	assetrouter.RegisterAssetRegistryRoutes(group, assethandler.NewAssetEntityHandler(facade))
	req := httptest.NewRequest(http.MethodGet, "/v1/assetEntities?assetCategory=web&q=web-category.example&pageSize=100", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	out := httptest.NewRecorder()
	engine.ServeHTTP(out, req)
	var body struct {
		Assets []struct {
			AssetType   string `json:"assetType"`
			DisplayType string `json:"displayType"`
		} `json:"assetEntities"`
	}
	if out.Code != http.StatusOK || json.Unmarshal(out.Body.Bytes(), &body) != nil || len(body.Assets) != 4 {
		t.Fatalf("Web API response: %d %s", out.Code, out.Body.String())
	}
	for _, item := range body.Assets {
		if item.DisplayType != "web" {
			t.Fatalf("incorrect API display type: %+v", body.Assets)
		}
	}
	monitor := monitorapp.NewFacade(monitorrepo.NewStore(orm), nil)
	snapshot, err := monitor.Read(ctx, user, []int{organization})
	if err != nil {
		t.Fatal(err)
	}
	categoryCounts := map[string]int64{}
	for _, category := range snapshot.AssetCategories {
		categoryCounts[category.Key] = category.Count
	}
	canonicalCounts := map[string]int64{}
	for _, category := range snapshot.AssetTypes {
		canonicalCounts[category.Key] = category.Count
	}
	if snapshot.Summary.TotalAssets != 3 || categoryCounts["web"] != 4 || categoryCounts["service"] != 1 || canonicalCounts["service"] != 3 || canonicalCounts["endpoint"] != 2 {
		t.Fatalf("monitor categories and canonical types: %+v / %+v / %+v", snapshot.Summary, categoryCounts, canonicalCounts)
	}
	webRecent, serviceRecent := 0, 0
	for _, item := range snapshot.RecentAssets {
		if item.DisplayType == "web" {
			webRecent++
		}
		if item.DisplayType == "service" {
			serviceRecent++
		}
	}
	if webRecent != 0 || serviceRecent != 3 {
		t.Fatalf("monitor recent classifications: %+v", snapshot.RecentAssets)
	}
}

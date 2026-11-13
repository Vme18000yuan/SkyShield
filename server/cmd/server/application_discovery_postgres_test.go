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
	"github.com/yyhuni/lunafox/server/internal/middleware"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	assethandler "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/handler"
	assetrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
	assetrouter "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/router"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyApplicationDiscoveryPostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var user, orgA, orgB, targetA, targetB, targetOther, targetNoSite int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct {
		name string
		id   *int
	}{{"application-discovery-a", &orgA}, {"application-discovery-b", &orgB}} {
		if err := db.QueryRowContext(ctx, `INSERT INTO organization(name) VALUES($1) RETURNING id`, x.name).Scan(x.id); err != nil {
			t.Fatal(err)
		}
	}
	for _, x := range []struct {
		name string
		id   *int
	}{{"app-fixture-example.com", &targetA}, {"dept.app-fixture-example.com", &targetB}, {"different-app-fixture.com", &targetOther}, {"empty.app-fixture-example.com", &targetNoSite}} {
		if err := db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES($1,'domain') RETURNING id`, x.name).Scan(x.id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO organization_target(organization_id,target_id) VALUES($1,$3),($2,$4),($2,$5),($2,$6)`, orgA, orgB, targetA, targetB, targetOther, targetNoSite); err != nil {
		t.Fatal(err)
	}
	var siteA, siteA2, siteB, siteOther, product, errorSite int
	for _, x := range []struct {
		target           int
		url, host, title string
		status           int
		tech             string
		id               *int
	}{
		{targetA, "https://sales.app-fixture-example.com/", "sales.app-fixture-example.com", "Sales Console", 200, "Nginx", &siteA},
		{targetA, "https://admin.app-fixture-example.com/", "admin.app-fixture-example.com", "Ｓａｌｅｓ　Console", 200, "Vue.js", &siteA2},
		{targetB, "https://portal.dept.app-fixture-example.com/", "portal.dept.app-fixture-example.com", "sales console", 200, "Nginx", &siteB},
		{targetOther, "https://sales.different-app-fixture.com/", "sales.different-app-fixture.com", "Sales Console", 200, "Nginx", &siteOther},
		{targetA, "https://metrics.app-fixture-example.com/", "metrics.app-fixture-example.com", "登录", 200, "Grafana:10", &product},
		{targetA, "https://denied.app-fixture-example.com/", "denied.app-fixture-example.com", "Access denied", 403, "Nginx", &errorSite},
	} {
		if err := db.QueryRowContext(ctx, `INSERT INTO website(target_id,url,host,title,status_code,tech) VALUES($1,$2,$3,$4,$5,ARRAY[$6]) RETURNING id`, x.target, x.url, x.host, x.title, x.status, x.tech).Scan(x.id); err != nil {
			t.Fatal(err)
		}
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := assetrepo.NewAssetRegistryStore(orm)
	drain := func(run func(context.Context, int) (bool, error)) {
		t.Helper()
		for i := 0; i < 1000; i++ {
			worked, e := run(ctx, 100)
			if e != nil {
				t.Fatal(e)
			}
			if !worked {
				return
			}
		}
		t.Fatal("projection did not converge")
	}
	drain(store.ProcessRootDomainBatch)
	drain(store.ProcessApplicationBatch)
	appFor := func(site int) int64 {
		t.Helper()
		var id int64
		if e := db.QueryRowContext(ctx, `SELECT application_id FROM asset_application_website WHERE website_id=$1`, site).Scan(&id); e != nil {
			t.Fatal(e)
		}
		return id
	}
	app := appFor(siteA)
	if appFor(siteA2) != app || appFor(siteB) != app {
		t.Fatal("same-name websites under the same root were not grouped")
	}
	if appFor(siteOther) == app || appFor(product) == app {
		t.Fatal("different roots or products were merged")
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM asset_application_website WHERE website_id=$1`, errorSite).Scan(&count); err != nil || count != 0 {
		t.Fatalf("error page produced an application: %d %v", count, err)
	}
	var lateSite int
	if err := db.QueryRowContext(ctx, `INSERT INTO website(target_id,url,host,title,status_code,tech) VALUES($1,'https://late.app-fixture-example.com/','late.app-fixture-example.com','登录',200,ARRAY['Nginx']) RETURNING id`, targetA).Scan(&lateSite); err != nil {
		t.Fatal(err)
	}
	drain(store.ProcessApplicationBatch)
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM asset_application_website WHERE website_id=$1`, lateSite).Scan(&count); err != nil || count != 0 {
		t.Fatalf("generic title created an application: %d %v", count, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE website SET title='New Customer Portal' WHERE id=$1`, lateSite); err != nil {
		t.Fatal(err)
	}
	drain(store.ProcessApplicationBatch)
	if appFor(lateSite) == app {
		t.Fatal("new application evidence was merged with an unrelated name")
	}
	var rootID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM asset_entity WHERE asset_type='root_domain' AND label='app-fixture-example.com'`).Scan(&rootID); err != nil {
		t.Fatal(err)
	}
	facade := assetapp.NewFacade(store)
	listed, e := facade.List(ctx, assetapp.ListInput{UserID: user, AssetType: "app", AssetID: app, OrganizationID: orgA, RootDomainID: rootID, Page: 1, PageSize: 10})
	if e != nil || listed.TotalSize != 1 || len(listed.Items) != 1 || listed.Items[0].ApplicationSources != 3 || len(listed.Items[0].Organizations) != 2 {
		t.Fatalf("application scope: %+v %v", listed, e)
	}
	for _, scope := range []struct{ org, want int }{{orgA, 2}, {orgB, 1}} {
		overview, e := facade.ApplicationOverview(ctx, assetapp.ApplicationOverviewInput{UserID: user, ApplicationID: app, OrganizationID: scope.org, Page: 1, PageSize: 10})
		if e != nil || overview.TotalSize != int64(scope.want) || len(overview.Websites) != scope.want {
			t.Fatalf("scoped application overview: %+v %v", overview, e)
		}
		for _, site := range overview.Websites {
			if site.EvidenceKind != "title" || site.EvidenceValue == "" {
				t.Fatalf("missing title evidence: %+v", site)
			}
		}
	}
	productView, e := facade.ApplicationOverview(ctx, assetapp.ApplicationOverviewInput{UserID: user, ApplicationID: appFor(product), OrganizationID: orgA})
	if e != nil || len(productView.Websites) != 1 || productView.Websites[0].EvidenceKind != "technology" {
		t.Fatalf("product fingerprint evidence: %+v %v", productView, e)
	}
	tasks := aiapp.NewTaskFacade(airepo.NewTaskStore(orm), nil, nil)
	if _, err := tasks.Create(ctx, aiapp.CreateTaskInput{UserID: user, Goal: "验证应用", TargetID: targetA, AssetIDs: []int64{app}}); err != nil {
		t.Fatalf("application source target rejected: %v", err)
	}
	if _, err := tasks.Create(ctx, aiapp.CreateTaskInput{UserID: user, Goal: "不得扩大范围", TargetID: targetNoSite, AssetIDs: []int64{app}}); !errors.Is(err, assetapp.ErrAssetOutsideTarget) {
		t.Fatalf("unrelated target accepted application: %v", err)
	}
	registration := assetapp.NewRegistrationFacade(assetrepo.NewAssetRegistrationStore(orm, assetapp.LegacyAssetCreators{}), nil)
	manual, e := registration.Register(ctx, assetapp.BatchRegisterInput{UserID: user, Origin: "manual", RequestID: "application-bound-domain", Assets: []assetapp.AssetInput{{Type: "app", AppName: "Manual Ops", OrganizationID: orgA, BoundDomains: []string{"SALES.app-fixture-example.com."}}}})
	if e != nil || len(manual.Results) != 1 || len(manual.Errors) > 0 {
		t.Fatalf("manual application: %+v %v", manual, e)
	}
	var manualID int64
	if _, e = fmt.Sscan(manual.Results[0].ID, &manualID); e != nil {
		t.Fatal(e)
	}
	manualView, e := facade.ApplicationOverview(ctx, assetapp.ApplicationOverviewInput{UserID: user, ApplicationID: manualID, OrganizationID: orgA})
	if e != nil || manualView.TotalSize != 1 || manualView.Websites[0].EvidenceKind != "binding" {
		t.Fatalf("manual domain association: %+v %v", manualView, e)
	}
	if _, e = facade.ApplicationOverview(ctx, assetapp.ApplicationOverviewInput{UserID: user, ApplicationID: manualID, OrganizationID: orgB}); !errors.Is(e, assetapp.ErrApplicationNotFound) {
		t.Fatalf("manual application leaked to another org: %v", e)
	}
	matched, e := registration.Register(ctx, assetapp.BatchRegisterInput{UserID: user, Origin: "manual", RequestID: "application-merge", Assets: []assetapp.AssetInput{{Type: "app", AppName: "Sales Console", OrganizationID: orgA, BoundDomains: []string{"sales.app-fixture-example.com"}}}})
	if e != nil || len(matched.Results) != 1 || len(matched.Errors) != 0 {
		t.Fatalf("register matching application: %+v %v", matched, e)
	}
	var matchedID int64
	if _, e = fmt.Sscan(matched.Results[0].ID, &matchedID); e != nil {
		t.Fatal(e)
	}
	drain(store.ProcessApplicationBatch)
	if appFor(siteA) != matchedID || appFor(siteA2) != matchedID || appFor(siteB) != app {
		t.Fatal("explicit domain binding did not reconcile the same-name group within its organization")
	}
	matchedView, e := facade.ApplicationOverview(ctx, assetapp.ApplicationOverviewInput{UserID: user, ApplicationID: matchedID, OrganizationID: orgA})
	if e != nil || matchedView.TotalSize != 2 || matchedView.Websites[0].EvidenceKind != "title" {
		t.Fatalf("merged application evidence: %+v %v", matchedView, e)
	}
	manager := auth.NewJWTManager("application-fixture-secret", time.Minute, time.Hour)
	token, _, e := manager.GenerateAccessToken(user, "registry-admin", 0)
	if e != nil {
		t.Fatal(e)
	}
	engine := gin.New()
	group := engine.Group("/v1")
	group.Use(middleware.AuthMiddleware(manager, fixtureTokenVersions{}))
	assetrouter.RegisterAssetRegistryRoutes(group, assethandler.NewAssetEntityHandler(facade))
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/v1/assetEntities/%d/applicationOverview?organizationId=%d", matchedID, orgA), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	out := httptest.NewRecorder()
	engine.ServeHTTP(out, req)
	if out.Code != 200 || !strings.Contains(out.Body.String(), `"totalSize":2`) || strings.Contains(out.Body.String(), "portal.dept.app-fixture-example.com") {
		t.Fatalf("application endpoint scope: %d %s", out.Code, out.Body.String())
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM organization_target WHERE organization_id=$1 AND target_id=$2`, orgA, targetA); err != nil {
		t.Fatal(err)
	}
	// Visibility must change immediately, before the background queue runs.
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM asset_application_visible_website WHERE application_id=$1 AND website_id IN ($2,$3)`, matchedID, siteA, siteA2).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unlinked organization retained application evidence: %d %v", count, err)
	}
	unlinkedView, e := facade.ApplicationOverview(ctx, assetapp.ApplicationOverviewInput{UserID: user, ApplicationID: matchedID, OrganizationID: orgA})
	if e != nil || unlinkedView.TotalSize != 0 {
		t.Fatalf("unlinked organization retained associated websites: %+v %v", unlinkedView, e)
	}
	if _, err := tasks.Create(ctx, aiapp.CreateTaskInput{UserID: user, Goal: "解绑后不能继承", TargetID: targetA, AssetIDs: []int64{matchedID}}); !errors.Is(err, assetapp.ErrAssetOutsideTarget) {
		t.Fatalf("unlinked application accepted by target: %v", err)
	}
	drain(store.ProcessApplicationBatch)
	if appFor(siteA) != app || appFor(siteA2) != app {
		t.Fatal("unlinked sites were not returned to their identified application")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO organization_target(organization_id,target_id) VALUES($1,$2)`, orgA, targetA); err != nil {
		t.Fatal(err)
	}
	drain(store.ProcessApplicationBatch)
	if appFor(siteA) != matchedID || appFor(siteA2) != matchedID {
		t.Fatal("relinked organization did not reconcile application sites")
	}
	if _, err := tasks.Create(ctx, aiapp.CreateTaskInput{UserID: user, Goal: "重新关联", TargetID: targetA, AssetIDs: []int64{matchedID}}); err != nil {
		t.Fatalf("relinked application rejected by target: %v", err)
	}
	ambiguous, e := registration.Register(ctx, assetapp.BatchRegisterInput{UserID: user, Origin: "manual", RequestID: "application-ambiguous", Assets: []assetapp.AssetInput{{Type: "app", AppName: "Sales Console", BundleID: "com.example.second", OrganizationID: orgA, BoundDomains: []string{"admin.app-fixture-example.com"}}}})
	if e != nil || len(ambiguous.Results) != 1 || len(ambiguous.Errors) != 0 {
		t.Fatalf("register ambiguous match: %+v %v", ambiguous, e)
	}
	drain(store.ProcessApplicationBatch)
	if appFor(siteA) != app || appFor(siteA2) != app {
		t.Fatal("ambiguous manual applications must not be merged automatically")
	}
	if _, err := db.ExecContext(ctx, `UPDATE website SET title='Access denied',status_code=403 WHERE id IN ($1,$2)`, siteA, siteA2); err != nil {
		t.Fatal(err)
	}
	drain(store.ProcessApplicationBatch)
	hidden, e := facade.List(ctx, assetapp.ListInput{UserID: user, AssetType: "app", AssetID: app, OrganizationID: orgA, Page: 1, PageSize: 1})
	if e != nil || hidden.TotalSize != 0 {
		t.Fatalf("removed application evidence remained in org A: %+v %v", hidden, e)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM website WHERE id=$1`, siteB); err != nil {
		t.Fatal(err)
	}
	hidden, e = facade.List(ctx, assetapp.ListInput{UserID: user, AssetType: "app", AssetID: app, Page: 1, PageSize: 1})
	if e != nil || hidden.TotalSize != 0 {
		t.Fatalf("orphaned application still visible: %+v %v", hidden, e)
	}
}

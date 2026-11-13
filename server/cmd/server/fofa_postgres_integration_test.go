package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/auth"
	assetwiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/asset"
	catalogwiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/catalog"
	spacewiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/spacesearch"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/asset/application"
	assetrepo "github.com/yyhuni/lunafox/server/internal/modules/asset/repository"
	registryapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	registryrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
	catalogapp "github.com/yyhuni/lunafox/server/internal/modules/catalog/application"
	catalogrepo "github.com/yyhuni/lunafox/server/internal/modules/catalog/repository"
	identityrepo "github.com/yyhuni/lunafox/server/internal/modules/identity/repository"
	spaceapp "github.com/yyhuni/lunafox/server/internal/modules/spacesearch/application"
	spacehandler "github.com/yyhuni/lunafox/server/internal/modules/spacesearch/handler"
	spaceinfra "github.com/yyhuni/lunafox/server/internal/modules/spacesearch/infrastructure"
	spacerepo "github.com/yyhuni/lunafox/server/internal/modules/spacesearch/repository"
	spacerouter "github.com/yyhuni/lunafox/server/internal/modules/spacesearch/router"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type failingFofaImporter struct{ spaceapp.AssetImporter }

func (f failingFofaImporter) Import(ctx context.Context, input spaceapp.ImportCommand) (spaceapp.ImportRowResult, error) {
	result, err := f.AssetImporter.Import(ctx, input)
	if err != nil {
		return result, err
	}
	return result, errors.New("injected failure after asset writes")
}

func verifyFofaPostgresContract(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	t.Setenv("TIANQIONG_AI_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("f", 32))))
	t.Setenv("TIANQIONG_AI_ENCRYPTION_KEY_FILE", "")
	seedUser := func(name, role string, superuser bool) int {
		var id int
		if err := db.QueryRowContext(ctx, `INSERT INTO auth_user(username,password,is_active,is_superuser,is_staff) VALUES($1,'fixture',TRUE,$2,$2) RETURNING id`, name, superuser).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO tenant_membership(tenant_id,user_id,role) VALUES($1,$2,$3) ON CONFLICT(tenant_id,user_id) DO UPDATE SET role=EXCLUDED.role`, registryapp.DefaultTenantID, id, role); err != nil {
			t.Fatal(err)
		}
		return id
	}
	admin := seedUser("fofa-admin", "admin", true)
	analyst := seedUser("fofa-analyst", "analyst", false)
	viewer := seedUser("fofa-viewer", "viewer", false)
	var organization int
	if err := db.QueryRowContext(ctx, `INSERT INTO organization(name) VALUES('FOFA fixture organization') RETURNING id`).Scan(&organization); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	targetRepo := catalogrepo.NewTargetRepository(orm)
	targets := catalogapp.NewTargetFacade(catalogapp.NewTargetQueryService(catalogwiring.NewCatalogTargetQueryStoreAdapter(targetRepo)), catalogapp.NewTargetCommandService(catalogwiring.NewCatalogTargetCommandStoreAdapter(targetRepo), catalogwiring.NewCatalogOrganizationTargetBindingStoreAdapter(identityrepo.NewOrganizationRepository(orm)), catalogwiring.NewCatalogTransactionCoordinator(orm)))
	lookup := assetwiring.NewAssetTargetLookupAdapter(targetRepo)
	hostPorts := assetapp.NewHostPortFacade(nil, assetapp.NewHostPortCommandService(assetwiring.NewAssetHostPortStoreAdapter(assetrepo.NewHostPortRepository(orm)), lookup))
	registry := registryapp.NewRegistrationFacade(registryrepo.NewAssetRegistrationStore(orm, assetRegistrationCreators(orm)), airuntime.SecretBox{})
	importer := spacewiring.NewSpacesearchAssetImporterAdapter(targets, targetRepo, hostPorts, registry)
	store := spacerepo.NewFofaStore(orm)
	const apiKey = "fofa-fixture-secret"
	const rawURL = "https://api.fofa-fixture.example/Case%2FPath?b=2&a=1"
	var providerCalls atomic.Int64
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != apiKey || r.URL.Query().Get("email") != "ops@fofa-fixture.example" {
			t.Error("saved FOFA credential was not retained")
		}
		if r.URL.Path == "/api/v1/info/my" {
			_ = json.NewEncoder(w).Encode(map[string]any{"error": false, "email": "ops@fofa-fixture.example"})
			return
		}
		providerCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": false, "size": 6, "results": [][]any{
			{rawURL, "192.0.2.80", "443", "https", "fofa-fixture.example", "FOFA 站点", "nginx", "CN", "中国", "上海", "上海", "ICP fixture", "tcp"},
			{"192.0.2.81:22", "192.0.2.81", 22, "ssh", "", "", "OpenSSH", "", "", "", "", "", "tcp"},
			{"https://[2001:db8::42]:8443/", "2001:db8::42", "8443", "https", "", "IPv6 fixture", "", "", "", "", "", "", "tcp"},
			{"invalid", "not-an-ip", 80, "http", "", "", "", "", "", "", "", "", "tcp"},
			{"https://a.new-fofa-fixture.co.uk/", "192.0.2.82", 443, "https", "unrelated.example", "New site A", "nginx", "", "", "", "", "", "tcp"},
			{"https://b.new-fofa-fixture.co.uk/", "192.0.2.83", 443, "https", "", "New site B", "nginx", "", "", "", "", "", "tcp"},
		}})
	}))
	defer fixture.Close()
	provider := spaceinfra.NewFofaClient(fixture.URL, fixture.Client())
	facade := spaceapp.NewFofaFacade(store, provider, airuntime.SecretBox{}, importer)
	if _, err = facade.Save(ctx, analyst, "ops@fofa-fixture.example", apiKey); !errors.Is(err, spaceapp.ErrForbidden) {
		t.Fatalf("analyst changed credential: %v", err)
	}
	status, err := facade.Save(ctx, admin, "ops@fofa-fixture.example", apiKey)
	if err != nil || !status.Configured || !status.CanManage {
		t.Fatalf("FOFA settings: %+v %v", status, err)
	}
	var ciphertext []byte
	if err = db.QueryRowContext(ctx, `SELECT encrypted_api_key FROM fofa_setting WHERE tenant_id=$1`, registryapp.DefaultTenantID).Scan(&ciphertext); err != nil || strings.Contains(string(ciphertext), apiKey) {
		t.Fatalf("FOFA key not encrypted: %v", err)
	}
	if _, err = facade.Save(ctx, admin, "ops@fofa-fixture.example", ""); err != nil {
		t.Fatal(err)
	}
	if err = facade.Test(ctx, admin); err != nil {
		t.Fatal(err)
	}
	status, err = facade.Status(ctx, analyst)
	if err != nil || status.Email != "" || status.CanManage || !status.CanSearch || !status.Configured {
		t.Fatalf("credential scope: %+v %v", status, err)
	}
	if _, err = facade.Search(ctx, viewer, spaceapp.SearchInput{Query: "fixture", PageSize: 4}); !errors.Is(err, spaceapp.ErrForbidden) || providerCalls.Load() != 0 {
		t.Fatalf("viewer search was not denied: %v", err)
	}
	page, err := facade.Search(ctx, analyst, spaceapp.SearchInput{Query: `domain="fofa-fixture.example"`, PageSize: 6})
	if err != nil || len(page.Results) != 6 || !page.Results[0].Importable || page.Results[3].Importable || page.Results[0].URL != rawURL || page.Results[0].Target != "fofa-fixture.example" || page.Results[4].Target != "new-fofa-fixture.co.uk" {
		t.Fatalf("FOFA snapshot: %+v %v", page, err)
	}
	var existingRoot int
	if err = db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES('fofa-fixture.example','domain') RETURNING id`).Scan(&existingRoot); err != nil {
		t.Fatal(err)
	}
	// A search saved by the previous version still contains the old full-host
	// target. Import must apply the current root rule to that snapshot too.
	if _, err = db.ExecContext(ctx, `UPDATE fofa_search SET records=jsonb_set(records,'{0,target}',to_jsonb('api.fofa-fixture.example'::text)) WHERE id=$1`, page.ID); err != nil {
		t.Fatal(err)
	}
	input := spaceapp.ImportInput{UserID: analyst, SearchID: page.ID, Rows: []int{0}, OrganizationID: organization}
	foreign := input
	foreign.UserID = admin
	if _, err = facade.Import(ctx, foreign); !errors.Is(err, spaceapp.ErrSearchNotFound) {
		t.Fatalf("cross-user result import: %v", err)
	}
	result, err := facade.Import(ctx, input)
	if err != nil || result.ImportedRows != 1 || result.FailedRows != 0 || result.AssetCount != 3 {
		t.Fatalf("FOFA import: %+v %v", result, err)
	}
	targetID := result.Results[0].TargetID
	if targetID != existingRoot {
		t.Fatalf("existing root target not reused: got %d want %d", targetID, existingRoot)
	}
	var title, server string
	if err = db.QueryRowContext(ctx, `SELECT title,webserver FROM website WHERE target_id=$1 AND url=$2`, targetID, rawURL).Scan(&title, &server); err != nil || title != "FOFA 站点" || server != "nginx" {
		t.Fatalf("FOFA website projection: %q %q %v", title, server, err)
	}
	assertCount := func(query string, want int, args ...any) {
		t.Helper()
		var got int
		if e := db.QueryRowContext(ctx, query, args...).Scan(&got); e != nil || got != want {
			t.Fatalf("FOFA count got %d want %d: %v", got, want, e)
		}
	}
	assertCount(`SELECT count(*) FROM organization_target WHERE organization_id=$1 AND target_id=$2`, 1, organization, targetID)
	assertCount(`SELECT count(*) FROM target WHERE name='api.fofa-fixture.example' AND deleted_at IS NULL`, 0)
	assertCount(`SELECT count(*) FROM host_port_mapping WHERE target_id=$1 AND ip='192.0.2.80' AND port=443`, 1, targetID)
	assertCount(`SELECT count(*) FROM subdomain WHERE target_id=$1 AND dns_name='api.fofa-fixture.example'`, 1, targetID)
	assertCount(`SELECT count(*) FROM asset_observation WHERE origin='import' AND payload->'extra'->>'searchId'=$1`, 3, page.ID)
	newRoots := input
	newRoots.Rows = []int{4, 5}
	newResult, err := facade.Import(ctx, newRoots)
	if err != nil || newResult.ImportedRows != 2 || newResult.FailedRows != 0 || newResult.Results[0].TargetID != newResult.Results[1].TargetID {
		t.Fatalf("new root grouping: %+v %v", newResult, err)
	}
	newRootID := newResult.Results[0].TargetID
	assertCount(`SELECT count(*) FROM target WHERE id=$1 AND name='new-fofa-fixture.co.uk' AND deleted_at IS NULL`, 1, newRootID)
	assertCount(`SELECT count(*) FROM target WHERE name IN ('a.new-fofa-fixture.co.uk','b.new-fofa-fixture.co.uk','unrelated.example') AND deleted_at IS NULL`, 0)
	assertCount(`SELECT count(*) FROM organization_target WHERE organization_id=$1 AND target_id=$2`, 1, organization, newRootID)
	assertCount(`SELECT count(*) FROM subdomain WHERE target_id=$1 AND dns_name IN ('a.new-fofa-fixture.co.uk','b.new-fofa-fixture.co.uk')`, 2, newRootID)
	var secondOrganization int
	if err := db.QueryRowContext(ctx, `INSERT INTO organization(name) VALUES('FOFA second organization') RETURNING id`).Scan(&secondOrganization); err != nil {
		t.Fatal(err)
	}
	newRoots.OrganizationID = secondOrganization
	newRoots.Rows = []int{4}
	newResult, err = facade.Import(ctx, newRoots)
	if err != nil || newResult.ImportedRows != 1 || newResult.Results[0].TargetID != newRootID {
		t.Fatalf("existing root was not reused for another organization: %+v %v", newResult, err)
	}
	assertCount(`SELECT count(*) FROM organization_target WHERE organization_id=$1 AND target_id=$2`, 1, secondOrganization, newRootID)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, e := facade.Import(ctx, input)
			if e == nil && (value.ImportedRows != 1 || !value.Results[0].Reused) {
				e = errors.New("import was not replayed")
			}
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	assertCount(`SELECT count(*) FROM asset_observation WHERE origin='import' AND payload->'extra'->>'searchId'=$1 AND (payload->'extra'->>'rowId')::int=0`, 3, page.ID)
	explicit := input
	explicit.TargetID = targetID
	result, err = facade.Import(ctx, explicit)
	if err != nil || result.ImportedRows != 1 || result.Results[0].TargetID != targetID {
		t.Fatalf("explicit in-scope target rejected: %+v %v", result, err)
	}
	var outsideID int
	if err = db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES('outside-fofa-scope.example','domain') RETURNING id`).Scan(&outsideID); err != nil {
		t.Fatal(err)
	}
	outOfScope := input
	outOfScope.TargetID = outsideID
	result, err = facade.Import(ctx, outOfScope)
	if err != nil || result.ImportedRows != 0 || result.FailedRows != 1 {
		t.Fatalf("out-of-scope FOFA import: %+v %v", result, err)
	}
	assertCount(`SELECT count(*) FROM organization_target WHERE organization_id=$1 AND target_id=$2`, 0, organization, outsideID)
	assertCount(`SELECT count(*) FROM host_port_mapping WHERE target_id=$1`, 0, outsideID)
	failed := spaceapp.NewFofaFacade(store, provider, airuntime.SecretBox{}, failingFofaImporter{importer})
	rowOne := input
	rowOne.Rows = []int{1}
	result, err = failed.Import(ctx, rowOne)
	if err != nil || result.FailedRows != 1 {
		t.Fatalf("injected failure: %+v %v", result, err)
	}
	assertCount(`SELECT count(*) FROM target WHERE name='192.0.2.81' AND deleted_at IS NULL`, 0)
	assertCount(`SELECT count(*) FROM host_port_mapping WHERE ip='192.0.2.81'`, 0)
	assertCount(`SELECT count(*) FROM fofa_import WHERE search_id=$1 AND row_index=1`, 0, page.ID)
	result, err = facade.Import(ctx, rowOne)
	if err != nil || result.ImportedRows != 1 || result.AssetCount != 2 {
		t.Fatalf("retry after rolled-back import: %+v %v", result, err)
	}
	ipv6 := input
	ipv6.Rows = []int{2}
	result, err = facade.Import(ctx, ipv6)
	if err != nil || result.ImportedRows != 1 || result.Results[0].TargetID != 0 || len(result.Results[0].Warnings) == 0 {
		t.Fatalf("IPv6 FOFA import: %+v %v", result, err)
	}
	assertCount(`SELECT count(*) FROM asset_entity WHERE asset_type='ip' AND label='2001:db8::42'`, 1)
	assertCount(`SELECT count(*) FROM target WHERE name='2001:db8::42'`, 0)
	verifyFofaHTTPContract(t, facade, admin, analyst, viewer, input, apiKey)
	invalid := input
	invalid.Rows = []int{3}
	result, err = facade.Import(ctx, invalid)
	if err != nil || result.FailedRows != 1 {
		t.Fatalf("malformed FOFA row was accepted: %+v %v", result, err)
	}
	expired := *page
	expired.ID = uuid.NewString()
	expired.ExpiresAt = time.Now().Add(-time.Minute)
	if err = store.SaveSearch(ctx, analyst, expired); err != nil {
		t.Fatal(err)
	}
	expiredInput := input
	expiredInput.SearchID = expired.ID
	if _, err = facade.Import(ctx, expiredInput); !errors.Is(err, spaceapp.ErrSearchNotFound) {
		t.Fatalf("expired snapshot accepted: %v", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE tenant_membership SET role='viewer' WHERE tenant_id=$1 AND user_id=$2`, registryapp.DefaultTenantID, analyst); err != nil {
		t.Fatal(err)
	}
	if _, err = facade.Import(ctx, input); !errors.Is(err, spaceapp.ErrForbidden) {
		t.Fatalf("revoked search permission accepted: %v", err)
	}
	if err = facade.Clear(ctx, admin); err != nil {
		t.Fatal(err)
	}
	if _, err = facade.Search(ctx, admin, spaceapp.SearchInput{Query: "fixture"}); !errors.Is(err, spaceapp.ErrNotConfigured) {
		t.Fatalf("cleared credential remained available: %v", err)
	}
}

func verifyFofaHTTPContract(t *testing.T, facade *spaceapp.FofaFacade, admin, analyst, viewer int, input spaceapp.ImportInput, key string) {
	t.Helper()
	manager := auth.NewJWTManager("fofa-fixture-jwt-secret", time.Minute, time.Hour)
	token := func(id int) string {
		value, _, err := manager.GenerateAccessToken(id, "fofa-fixture", 0)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/v1")
	group.Use(middleware.AuthMiddleware(manager, fixtureTokenVersions{}))
	spacerouter.RegisterSpaceSearchRoutes(group, spacehandler.NewFofaHandler(facade))
	request := func(method, path, body, bearer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, req)
		return response
	}
	if r := request(http.MethodGet, "/v1/fofaSettings/current", "", ""); r.Code != http.StatusUnauthorized {
		t.Fatalf("FOFA status without auth: %d", r.Code)
	}
	if r := request(http.MethodGet, "/v1/fofaSettings/current", "", token(admin)); r.Code != http.StatusOK || strings.Contains(r.Body.String(), key) || strings.Contains(r.Body.String(), "encrypted") {
		t.Fatalf("credential leaked in status: %d %s", r.Code, r.Body.String())
	}
	if r := request(http.MethodPut, "/v1/fofaSettings/current", `{"email":"changed@example.test","apiKey":"change"}`, token(analyst)); r.Code != http.StatusForbidden {
		t.Fatalf("analyst changed FOFA settings: %d", r.Code)
	}
	if r := request(http.MethodPost, "/v1/fofaSearches", `{"query":"fixture","pageSize":4}`, token(viewer)); r.Code != http.StatusForbidden {
		t.Fatalf("viewer searched FOFA: %d", r.Code)
	}
	body := `{"rows":[0],"organizationId":` + strconv.Itoa(input.OrganizationID) + `}`
	path := "/v1/fofaSearches/" + input.SearchID + "/import"
	if r := request(http.MethodPost, path, body, token(admin)); r.Code != http.StatusNotFound {
		t.Fatalf("FOFA snapshot crossed users: %d", r.Code)
	}
	if r := request(http.MethodPost, path, body, token(analyst)); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"reused":true`) {
		t.Fatalf("FOFA HTTP import: %d %s", r.Code, r.Body.String())
	}
}

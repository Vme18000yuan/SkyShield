package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	assetwiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/asset"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/asset/application"
	assetrepo "github.com/yyhuni/lunafox/server/internal/modules/asset/repository"
	registryapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	registryrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
	catalogrepo "github.com/yyhuni/lunafox/server/internal/modules/catalog/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type failingAssetCreator struct{ registryapp.LegacyAssetCreator }

func (f failingAssetCreator) BatchCreateContext(ctx context.Context, target int, values []string) (int, error) {
	_, err := f.LegacyAssetCreator.BatchCreateContext(ctx, target, values)
	if err != nil {
		return 0, err
	}
	return 0, errors.New("injected projection failure")
}

func assetRegistrationCreators(db *gorm.DB) registryapp.LegacyAssetCreators {
	lookup := assetwiring.NewAssetTargetLookupAdapter(catalogrepo.NewTargetRepository(db))
	websiteStore := assetwiring.NewAssetWebsiteStoreAdapter(assetrepo.NewWebsiteRepository(db))
	endpointStore := assetwiring.NewAssetEndpointStoreAdapter(assetrepo.NewEndpointRepository(db))
	subdomainStore := assetwiring.NewAssetSubdomainStoreAdapter(assetrepo.NewSubdomainRepository(db))
	return registryapp.LegacyAssetCreators{
		Websites:   assetapp.NewWebsiteFacade(nil, assetapp.NewWebsiteCommandService(websiteStore, lookup)),
		Endpoints:  assetapp.NewEndpointFacade(nil, assetapp.NewEndpointCommandService(endpointStore, lookup)),
		Subdomains: assetapp.NewSubdomainFacade(nil, assetapp.NewSubdomainCommandService(subdomainStore, lookup)),
	}
}

func verifyAssetRegistrationPostgresContract(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	t.Setenv("TIANQIONG_AI_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32))))
	t.Setenv("TIANQIONG_AI_ENCRYPTION_KEY_FILE", "")
	var user, target int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES('write-scope.example','domain') RETURNING id`).Scan(&target); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	creators := assetRegistrationCreators(orm)
	store := registryrepo.NewAssetRegistrationStore(orm, creators)
	facade := registryapp.NewRegistrationFacade(store, airuntime.SecretBox{})
	status := int64(200)
	const rawURL = "https://api.write-scope.example/Case%2FPath?b=2&a=1"
	input := registryapp.BatchRegisterInput{UserID: user, Origin: "ai", RequestID: uuid.NewString(), Assets: []registryapp.AssetInput{
		{Type: "service", URL: rawURL, TargetID: target, PageTitle: "观测标题", StatusCode: &status, Technologies: []string{"nginx"}, Auth: []map[string]any{{"password": "synthetic-asset-secret"}}},
		{Type: "endpoint", URL: rawURL, TargetID: target, Method: "get", PageTitle: "GET only"},
		{Type: "endpoint", URL: rawURL, TargetID: target, Method: "post", PageTitle: "POST only"},
		{Type: "subdomain", Domain: "api.write-scope.example", TargetID: target, RecordType: "A", RecordValue: []string{"192.0.2.30"}},
		{Type: "root_domain", Domain: "example.com"},
		{Type: "ip", IP: "2001:db8::1234"},
		{Type: "app", AppName: "移动应用", BundleID: "com.example.app"},
		{Type: "service", IP: "192.0.2.30", Port: 22, Proto: "tcp", ServiceName: "SSH"},
		{Type: "service", URL: "https://out-of-scope.example/", TargetID: target},
	}}
	result, err := facade.Register(ctx, input)
	if err != nil || len(result.Results) != 8 || len(result.Errors) != 1 || result.Errors[0].Index != 8 {
		t.Fatalf("batch registration: %+v, %v", result, err)
	}
	if result.Results[1].ID == result.Results[2].ID {
		t.Fatal("GET and POST identities were merged")
	}
	var n int
	var title, storedURL string
	if err := db.QueryRowContext(ctx, `SELECT url,title FROM website WHERE target_id=$1 AND url=$2`, target, rawURL).Scan(&storedURL, &title); err != nil || storedURL != rawURL || title != "观测标题" {
		t.Fatalf("original Website projection: %q %q %v", storedURL, title, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM endpoint WHERE target_id=$1 AND url=$2`, target, rawURL).Scan(&n); err != nil || n != 1 {
		t.Fatalf("URL-level endpoint projection: count %d err %v", n, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT title FROM endpoint WHERE target_id=$1 AND url=$2`, target, rawURL).Scan(&title); err != nil || title != "" {
		t.Fatalf("method-specific title polluted URL evidence: %q %v", title, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_endpoint_variant_binding b JOIN endpoint e ON e.id=b.endpoint_id WHERE e.target_id=$1 AND e.url=$2`, target, rawURL).Scan(&n); err != nil || n != 2 {
		t.Fatalf("method variants: count %d err %v", n, err)
	}
	var payload string
	var encrypted []byte
	if err := db.QueryRowContext(ctx, `SELECT payload::text,encrypted_auth FROM asset_observation WHERE asset_id=$1 AND origin='ai'`, result.Results[0].ID).Scan(&payload, &encrypted); err != nil || strings.Contains(payload, "synthetic-asset-secret") || len(encrypted) == 0 {
		t.Fatalf("credential persistence: encrypted=%d err=%v", len(encrypted), err)
	}
	replay, err := facade.Register(ctx, input)
	if err != nil || len(replay.Results) != 8 || replay.Results[0].ID != result.Results[0].ID {
		t.Fatalf("idempotent replay: %+v %v", replay, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_observation WHERE created_by=$1 AND origin='ai' AND source_key LIKE $2`, user, "user-%/"+input.RequestID+"/%").Scan(&n); err != nil || n != 8 {
		t.Fatalf("observation replay count: %d %v", n, err)
	}
	input.Assets[0].PageTitle = "different"
	conflict, err := facade.Register(ctx, input)
	if err != nil || len(conflict.Errors) != 2 || conflict.Errors[0].Index != 0 {
		t.Fatalf("request conflict: %+v %v", conflict, err)
	}
	rollbackCreators := creators
	rollbackCreators.Websites = failingAssetCreator{creators.Websites}
	rollbackFacade := registryapp.NewRegistrationFacade(registryrepo.NewAssetRegistrationStore(orm, rollbackCreators), airuntime.SecretBox{})
	rollback, err := rollbackFacade.Register(ctx, registryapp.BatchRegisterInput{UserID: user, Origin: "manual", RequestID: uuid.NewString(), Assets: []registryapp.AssetInput{{Type: "service", TargetID: target, URL: "https://api.write-scope.example/rollback"}}})
	if err != nil || len(rollback.Results) != 0 || len(rollback.Errors) != 1 {
		t.Fatalf("rollback result: %+v %v", rollback, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM website WHERE url='https://api.write-scope.example/rollback'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("legacy write escaped registry transaction: %d %v", n, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_entity WHERE label='https://api.write-scope.example/rollback'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("entity write escaped registry transaction: %d %v", n, err)
	}
}

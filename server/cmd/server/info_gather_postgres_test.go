package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/norma/tool"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/auth"
	securitywiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/security"
	"github.com/yyhuni/lunafox/server/internal/infogather"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	settingsrepo "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	registryapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	registryrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
	catalogrepo "github.com/yyhuni/lunafox/server/internal/modules/catalog/repository"
	securityapp "github.com/yyhuni/lunafox/server/internal/modules/security/application"
	securityrepo "github.com/yyhuni/lunafox/server/internal/modules/security/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type infoGatherFixture struct{ calls int }

func (f *infoGatherFixture) Execute(_ context.Context, _ int, id string, scope infogather.Scope, mode string, _ func(string)) ([]byte, json.RawMessage, error) {
	f.calls++
	a := func(char string, kind, value, hint string) infogather.Asset {
		return infogather.Asset{ID: strings.Repeat(char, 64), Type: kind, Value: value, Scope: hint, Evidence: []string{"recon.json"}}
	}
	report := infogather.Envelope{SchemaVersion: "1.0", Run: infogather.ReportRun{ID: id, Target: scope.Name, Kind: scope.Kind, Status: "completed", Mode: mode}, Assets: []infogather.Asset{}, Vulnerabilities: []infogather.Vulnerability{}, Observations: []infogather.Observation{}}
	if scope.Name == "new-info.example" {
		report.Assets = append(report.Assets, a("9", "domain", scope.Name, "in_scope"))
	} else {
		ip := a("2", "ip", "203.0.113.5", "associated")
		ip.BoundDomains = []string{"app.auto-info.example"}
		cdn := a("6", "ip", "203.0.113.6", "unverified")
		cdn.BoundDomains = []string{"app.auto-info.example"}
		cdn.CDN = true
		web := a("3", "service", "https://app.auto-info.example/", "in_scope")
		web.PageTitle, web.Technologies = "Fixture console", []string{"nginx", "PHP:8.3"}
		report.Assets = append(report.Assets, a("1", "domain", "auto-info.example", "in_scope"), ip, web, a("4", "endpoint", "https://app.auto-info.example/api", "in_scope"), a("5", "domain", "other-info.example", "in_scope"), cdn)
		report.Vulnerabilities = append(report.Vulnerabilities, infogather.Vulnerability{ID: strings.Repeat("a", 64), Type: "ANONYMOUS_DATA", URL: "https://app.auto-info.example/api", Title: "candidate", Severity: "info", Confidence: "candidate", Status: "needs_review", Scope: "in_scope", Description: "candidate", Evidence: []string{"recon.json"}})
		report.Observations = append(report.Observations,
			infogather.Observation{ID: strings.Repeat("a", 64), Category: "ANONYMOUS_DATA", Value: "https://app.auto-info.example/api", Scope: "in_scope", Status: "needs_review", Evidence: []string{"recon.json"}},
			infogather.Observation{ID: strings.Repeat("b", 64), Category: "PUBLIC_LISTING", Value: "https://bucket.other.test/", Scope: "unverified", Status: "needs_review", Evidence: []string{"recon.json"}},
			infogather.Observation{ID: strings.Repeat("c", 64), Category: "DNS_ZONE_TRANSFER", Value: "auto-info.example", Scope: "in_scope", Status: "needs_review", Evidence: []string{"axfr.json"}},
			infogather.Observation{ID: strings.Repeat("d", 64), Category: "OTHER", Value: "fixture clue", Scope: "unverified", Status: "needs_review", Evidence: []string{"recon.json"}},
		)
	}
	raw, err := json.Marshal(report)
	return raw, json.RawMessage(`{"steps":{"dns":"success"}}`), err
}

func verifyInfoGatherPostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var user, other, target, otherTarget int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES('auto-info.example','domain') RETURNING id`).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES('other-info.example','domain') RETURNING id`).Scan(&otherTarget); err != nil {
		t.Fatal(err)
	}
	tenant := registryapp.DefaultTenantID
	conversation, execution, owner := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO ai_conversation(id,tenant_id,owner_user_id,title,agent_key) VALUES($1,$2,$3,'info fixture','info_gatherer')`, conversation, tenant, user); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO ai_run(id,tenant_id,conversation_id,owner_user_id,status,lease_owner,lease_until) VALUES($1,$2,$3,$4,'running',$5,$6)`, execution, tenant, conversation, user, owner, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO ai_tool_catalog(tenant_id,key,system,description,schema,agents,enabled,kind,exec,deferred) VALUES($1,'run_info_gather',TRUE,'fixture','{}','["info_gatherer"]',TRUE,'builtin','{}',FALSE) ON CONFLICT(tenant_id,key) DO NOTHING`, tenant); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = settingsrepo.NewAgentCatalogStore(orm).SeedDefaultAgents(ctx); err != nil {
		t.Fatal(err)
	}
	var seedCount int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_custom_agent a JOIN ai_skill_agent b ON b.tenant_id=a.tenant_id AND b.agent_key=a.agent_key JOIN ai_skill s ON s.id=b.skill_id WHERE a.tenant_id=$1 AND a.agent_key='info_gatherer' AND a.enabled AND s.name='info-gath' AND s.enabled`, tenant).Scan(&seedCount); err != nil || seedCount != 1 {
		t.Fatalf("default agent or skill binding: %d %v", seedCount, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_skill_file f JOIN ai_skill s ON s.id=f.skill_id WHERE s.tenant_id=$1 AND s.name='info-gath' AND f.path='scripts/recon.sh'`, tenant).Scan(&seedCount); err != nil || seedCount != 1 {
		t.Fatalf("skill package missing script: %d %v", seedCount, err)
	}
	verifyInfoGatherUpgradeCollision(t, ctx, orm, user, tenant)
	registration := registryapp.NewRegistrationFacade(registryrepo.NewAssetRegistrationStore(orm, assetRegistrationCreators(orm)), airuntime.SecretBox{})
	registration.SetAIAssetGate(func(_ context.Context, _ int, _ *registryapp.RegistrationTaskScope, request string, items []registryapp.AssetInput) ([]string, error) {
		if !strings.HasPrefix(request, execution+"/") {
			t.Fatalf("AI gate missing execution identity: %s", request)
		}
		return make([]string, len(items)), nil
	})
	risk := securityapp.NewVulnerabilityFacade(securityrepo.NewVulnerabilityRepository(orm), securitywiring.NewSecurityTargetLookupAdapter(catalogrepo.NewTargetRepository(orm)))
	store := infogather.NewStore(orm, registration, risk)
	executor := &infoGatherFixture{}
	service := infogather.NewService(store, executor, nil)
	result, err := service.Execute(ctx, user, execution, "fixture-1", "auto-info.example", "normal", nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Sync == nil || result.Sync.Imported != 6 || result.Run.Imported != 6 || result.Run.Pending != 3 || result.Run.Observations != 1 {
		t.Fatalf("unexpected sync: %+v", result)
	}
	if result.AssetCount != 6 || result.VulnerabilityCount != 1 || len(result.AssetSamples) == 0 || len(result.VulnerabilitySamples) != 1 {
		t.Fatalf("AI did not receive a bounded result sample: %+v", result)
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM vulnerability v JOIN asset_entity a ON a.id=v.asset_id WHERE v.target_id=$1 AND v.source='info-gath' AND v.reviewed=FALSE AND v.vuln_type NOT LIKE '%待复核%' AND v.raw_output->>'confidence'='candidate' AND ((v.url='https://app.auto-info.example/api' AND a.asset_type='endpoint') OR (v.url='dns://auto-info.example' AND a.asset_type IN ('root_domain','subdomain')))`, target).Scan(&count); err != nil || count != 2 {
		t.Fatalf("reviewable risk: %d %v", count, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_info_gather_record WHERE run_id=$1 AND kind='observation' AND record_id=$2`, result.Run.ID, strings.Repeat("a", 64)).Scan(&count); err != nil || count != 0 {
		t.Fatalf("duplicate risk observation retained: %d %v", count, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE vulnerability SET reviewed=TRUE WHERE target_id=$1 AND url='https://app.auto-info.example/api' AND source='info-gath'`, target); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO ai_info_gather_record(run_id,kind,record_id,payload) VALUES($1,'observation',$2,jsonb_build_object('id',$2::text,'category','ANONYMOUS_DATA','value','https://app.auto-info.example/api','scope','in_scope','status','needs_review','evidence',jsonb_build_array('recon.json')))`, result.Run.ID, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	duplicate, err := store.SyncSelected(ctx, user, result.Run.ID, target, []infogather.RecordRef{{Kind: "observation", ID: strings.Repeat("a", 64)}}, false)
	if err != nil || duplicate.Imported != 1 {
		t.Fatalf("duplicate observation handling: %+v %v", duplicate, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM vulnerability WHERE target_id=$1 AND url='https://app.auto-info.example/api' AND reviewed=TRUE AND asset_id IS NOT NULL`, target).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate risk changed review or asset association: %d %v", count, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_info_gather_record WHERE run_id=$1 AND kind='observation' AND record_id=$2 AND status='ignored'`, result.Run.ID, strings.Repeat("a", 64)).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate evidence was not retained: %d %v", count, err)
	}
	var title, tech string
	if err = db.QueryRowContext(ctx, `SELECT title,tech::text FROM website WHERE target_id=$1 AND url='https://app.auto-info.example/'`, target).Scan(&title, &tech); err != nil || title != "Fixture console" || !strings.Contains(tech, "nginx") {
		t.Fatalf("web fingerprints were not synchronized: %s %s %v", title, tech, err)
	}
	if _, err = store.Run(ctx, other, result.Run.ID); err == nil {
		t.Fatal("another user read the run")
	}
	verifyInfoGatherHTTP(t, store, user, other, target, result.Run.ID)
	beforeCalls := executor.calls
	replayed, err := service.Execute(ctx, user, execution, "fixture-1", "auto-info.example", "normal", nil)
	if err != nil || executor.calls != beforeCalls || replayed.Run.ID != result.Run.ID {
		t.Fatalf("idempotent replay: %+v %v", replayed, err)
	}
	manual, err := store.SyncSelected(ctx, user, result.Run.ID, otherTarget, []infogather.RecordRef{{Kind: "asset", ID: strings.Repeat("5", 64)}}, false)
	if err != nil || manual.Imported != 1 {
		t.Fatalf("manual association: %+v %v", manual, err)
	}
	cdn, err := store.SyncSelected(ctx, user, result.Run.ID, target, []infogather.RecordRef{{Kind: "asset", ID: strings.Repeat("6", 64)}}, false)
	if err != nil || cdn.Imported != 0 || len(cdn.Failures) != 1 {
		t.Fatalf("CDN was not held: %+v %v", cdn, err)
	}
	var bucketTarget int
	if err := db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES('bucket.other.test','domain') RETURNING id`).Scan(&bucketTarget); err != nil {
		t.Fatal(err)
	}
	manual, err = store.SyncSelected(ctx, user, result.Run.ID, bucketTarget, []infogather.RecordRef{{Kind: "observation", ID: strings.Repeat("b", 64)}}, false)
	if err != nil || manual.Imported != 1 {
		t.Fatalf("manual risk association: %+v %v", manual, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM vulnerability v JOIN asset_entity a ON a.id=v.asset_id WHERE v.target_id=$1 AND v.url='https://bucket.other.test/' AND v.source='info-gath' AND NOT v.reviewed AND a.asset_type='endpoint'`, bucketTarget).Scan(&count); err != nil || count != 1 {
		t.Fatalf("bucket risk asset link: %d %v", count, err)
	}
	if _, err := store.IgnoreSelected(ctx, other, result.Run.ID, []infogather.RecordRef{{Kind: "observation", ID: strings.Repeat("d", 64)}}); err == nil {
		t.Fatal("another user could process this observation")
	}
	if _, err := store.IgnoreSelected(ctx, user, result.Run.ID, []infogather.RecordRef{{Kind: "observation", ID: strings.Repeat("d", 64)}, {Kind: "observation", ID: strings.Repeat("e", 64)}}); err == nil {
		t.Fatal("partially invalid selection was accepted")
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_info_gather_record WHERE run_id=$1 AND kind='observation' AND record_id=$2 AND status='pending'`, result.Run.ID, strings.Repeat("d", 64)).Scan(&count); err != nil || count != 1 {
		t.Fatalf("failed selection changed valid evidence: %d %v", count, err)
	}
	ignored, err := store.IgnoreSelected(ctx, user, result.Run.ID, []infogather.RecordRef{{Kind: "observation", ID: strings.Repeat("d", 64)}})
	if err != nil || ignored != 1 {
		t.Fatalf("ignore observation: %d %v", ignored, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_info_gather_record WHERE run_id=$1 AND kind='observation' AND record_id=$2 AND status='ignored'`, result.Run.ID, strings.Repeat("d", 64)).Scan(&count); err != nil || count != 1 {
		t.Fatalf("ignored evidence was lost: %d %v", count, err)
	}
	newRun, err := service.Execute(ctx, user, execution, "fixture-2", "new-info.example", "fast", nil)
	if err != nil || newRun.Run.Pending != 1 || newRun.Run.Imported != 0 {
		t.Fatalf("unknown target: %+v %v", newRun, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM target WHERE name='new-info.example' AND deleted_at IS NULL`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unexpected target creation: %d %v", count, err)
	}
	var newTarget int
	if err = db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES('new-info.example','domain') RETURNING id`).Scan(&newTarget); err != nil {
		t.Fatal(err)
	}
	manual, err = store.SyncSelected(ctx, user, newRun.Run.ID, newTarget, []infogather.RecordRef{{Kind: "asset", ID: strings.Repeat("9", 64)}}, false)
	if err != nil || manual.Imported != 1 {
		t.Fatalf("created-target association: %+v %v", manual, err)
	}
	chat, err := airuntime.NewChatRunner(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	provider := &independentMainProvider{tool: "run_info_gather", input: `{"target":"new-info.example","mode":"fast"}`}
	var toolResult bool
	_, err = chat.Run(ctx, airuntime.ChatRun{TenantID: tenant, UserID: user, SessionID: conversation, ExecutionID: execution, AgentKey: "info_gatherer", SystemPrompt: "Use the provided collection tool.", Message: "Collect the authorized new-info.example target", Model: "fixture", Provider: provider, Tools: []tool.CoreTool{service.Tool(user)}, MaxTurns: 4, MaxDuration: time.Minute}, func(event airuntime.Activity) {
		if event.Kind == "tool_result" && event.Tool == "run_info_gather" && !event.IsError {
			toolResult = true
		}
	})
	if err != nil || !toolResult {
		t.Fatalf("AI chat did not execute collection tool: result=%t err=%v", toolResult, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_info_gather_run WHERE execution_id=$1 AND tool_use_id='independent-tool' AND status='completed'`, execution).Scan(&count); err != nil || count != 1 {
		t.Fatalf("chat collection run: %d %v", count, err)
	}
}

func verifyInfoGatherHTTP(t *testing.T, store *infogather.Store, user, other, target int, runID string) {
	t.Helper()
	manager := auth.NewJWTManager("info-gather-http-fixture", time.Minute, time.Hour)
	ownerToken, _, err := manager.GenerateAccessToken(user, "registry-admin", 0)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, _, err := manager.GenerateAccessToken(other, "ai-other", 0)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/v1")
	group.Use(middleware.AuthMiddleware(manager, fixtureTokenVersions{}))
	infogather.NewHandler(store).Register(group)
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	path := "/v1/aiInfoGatherRuns/" + runID + "/records"
	if response := request(http.MethodGet, path, "", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated records: %d", response.Code)
	}
	if response := request(http.MethodGet, path, otherToken, ""); response.Code != http.StatusNotFound {
		t.Fatalf("cross-user records: %d", response.Code)
	}
	if response := request(http.MethodGet, path, ownerToken, ""); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "other-info.example") {
		t.Fatalf("owner records: %d %s", response.Code, response.Body.String())
	}
	body, _ := json.Marshal(map[string]any{"targetId": target, "records": []infogather.RecordRef{{Kind: "asset", ID: strings.Repeat("5", 64)}}})
	response := request(http.MethodPost, "/v1/aiInfoGatherRuns/"+runID+"/sync", ownerToken, string(body))
	var result infogather.ImportResult
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Imported != 0 || result.Failed != 1 {
		t.Fatalf("out of scope HTTP sync: %d %s", response.Code, response.Body.String())
	}
	riskBody, _ := json.Marshal(map[string]any{"targetId": target, "records": []infogather.RecordRef{{Kind: "observation", ID: strings.Repeat("b", 64)}}})
	response = request(http.MethodPost, "/v1/aiInfoGatherRuns/"+runID+"/sync", ownerToken, string(riskBody))
	result = infogather.ImportResult{}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Imported != 0 || result.Failed != 1 {
		t.Fatalf("out of scope risk HTTP sync: %d %s", response.Code, response.Body.String())
	}
	ignoreBody, _ := json.Marshal(map[string]any{"records": []infogather.RecordRef{{Kind: "observation", ID: strings.Repeat("d", 64)}}})
	if response := request(http.MethodPost, "/v1/aiInfoGatherRuns/"+runID+"/ignore", otherToken, string(ignoreBody)); response.Code != http.StatusNotFound {
		t.Fatalf("cross-user ignore: %d", response.Code)
	}
}

func verifyInfoGatherUpgradeCollision(t *testing.T, ctx context.Context, orm *gorm.DB, user int, tenant string) {
	t.Helper()
	root, err := filepath.Abs("../../../resources/skills")
	if err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("isolated upgrade fixture rollback")
	err = orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`DELETE FROM ai_skill WHERE tenant_id=? AND name='info-gath'`, tenant).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DELETE FROM ai_runtime_seed WHERE tenant_id=? AND seed_key='builtin_skill_entry_v1/info-gath'`, tenant).Error; err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO ai_skill(id,tenant_id,name,description,instructions,enabled,created_by) VALUES(?,?,'info-gath','user-owned','keep this custom skill',TRUE,?)`, uuid.NewString(), tenant, user).Error; err != nil {
			return err
		}
		store := settingsrepo.NewSkillStore(tx, root)
		if err := store.SeedBuiltinSkills(ctx); err != nil {
			return err
		}
		var instructions string
		if err := tx.Raw(`SELECT instructions FROM ai_skill WHERE tenant_id=? AND name='info-gath'`, tenant).Row().Scan(&instructions); err != nil {
			return err
		}
		if instructions != "keep this custom skill" {
			t.Fatal("upgrade overwrote a custom skill")
		}
		var count int
		if err := tx.Raw(`SELECT count(*) FROM ai_runtime_seed WHERE tenant_id=? AND seed_key='builtin_skill_entry_v1/info-gath'`, tenant).Row().Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Fatal("conflict must remain retryable")
		}
		if err := tx.Exec(`DELETE FROM ai_skill WHERE tenant_id=? AND name='info-gath'`, tenant).Error; err != nil {
			return err
		}
		if err := store.SeedBuiltinSkills(ctx); err != nil {
			return err
		}
		if err := tx.Raw(`SELECT count(*) FROM ai_skill WHERE tenant_id=? AND name='info-gath' AND builtin_key='info-gath'`, tenant).Row().Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Fatal("built-in skill did not recover after conflict removal")
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("upgrade collision fixture: %v", err)
	}
}

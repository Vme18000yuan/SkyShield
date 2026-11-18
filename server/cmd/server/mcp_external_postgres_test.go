package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/aivuln"
	"github.com/yyhuni/lunafox/server/internal/auth"
	serverdatabase "github.com/yyhuni/lunafox/server/internal/database"
	"github.com/yyhuni/lunafox/server/internal/mcp/external"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	assetrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
	security "github.com/yyhuni/lunafox/server/internal/modules/security/application"
	securityrepo "github.com/yyhuni/lunafox/server/internal/modules/security/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Exercise the real SDK, credential lifecycle, and cross-organization checks
// against a disposable database. No worker processes are started.
func TestExternalMCPIsolatedPostgres(t *testing.T) {
	dsn := os.Getenv(emptyPostgresMigrationTestDSNEnv)
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	var database string
	if err = db.QueryRow("SELECT current_database()").Scan(&database); err != nil || !strings.HasPrefix(database, "lunafox_migration_test_") || countPublicTables(t, ctx, db, false) != 0 {
		t.Fatal("expected an empty test database")
	}
	fs, path := serverdatabase.MigrationsFS, serverdatabase.MigrationsPath
	serverdatabase.MigrationsFS, serverdatabase.MigrationsPath = migrationsFS, "migrations"
	defer func() { serverdatabase.MigrationsFS, serverdatabase.MigrationsPath = fs, path }()
	if err = serverdatabase.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO auth_user(username,password,is_superuser) VALUES('registry-admin','unused',true),('mcp-other','unused',false)"); err != nil {
		t.Fatal(err)
	}
	owner, target, _ := taskFixtureScope(t, ctx, db, "inventory-a.example")
	_, targetB, _ := taskFixtureScope(t, ctx, db, "inventory-b.example")
	insertID := func(query string, args ...any) int {
		t.Helper()
		var id int
		if err := db.QueryRow(query, args...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	org := insertID("INSERT INTO organization(name) VALUES('Organization A') RETURNING id")
	otherOrg := insertID("INSERT INTO organization(name) VALUES('Organization B') RETURNING id")
	exec("INSERT INTO organization_target(organization_id,target_id) VALUES($1,$2),($3,$4)", org, target, otherOrg, targetB)
	const vulnInsert = `INSERT INTO vulnerability(target_id,url,vuln_type,severity,source,display_name,raw_output)
          VALUES($1,'https://fixture.example/','configuration','low','nuclei',$2,$3) RETURNING id`
	raw, _ := json.Marshal(map[string]string{"request": "GET / HTTP/1.1\r\nCookie: session=private-fixture\r\n\r\n", "response": "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{\"ok\":true,\"token\":\"private-fixture\"}"})
	v := insertID(vulnInsert, target, "Stored name", string(raw))
	hidden := insertID(vulnInsert, targetB, "Other organization", "{}")
	exp := insertID("INSERT INTO tianqiong_ai.explorations(goal,status) VALUES('Inventory summary','achieved') RETURNING id")
	task := insertID(`INSERT INTO ai_task(tenant_id,owner_user_id,name,goal,exploration_id,target_id,original_target_id,status,queued,active_run)
        VALUES($1,$2,'Stored task','Inventory summary',$3,$4,$4,'done',false,$5) RETURNING id`, assets.DefaultTenantID, owner, exp, target, uuid.NewString())
	chat, run := uuid.NewString(), uuid.NewString()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("SELECT set_config('tianqiong.execution_mode','human_finding_case',true),set_config('tianqiong.task_id',$1,true),set_config('tianqiong.owner_user_id',$2,true)", fmt.Sprint(task), fmt.Sprint(owner)); err != nil {
		t.Fatal(err)
	}
	var caseID int
	if err = tx.QueryRow(`INSERT INTO tianqiong_ai.finding_cases(task_id,origin_task_id,tenant_id,owner_user_id,title,reason,report)
	  VALUES($1,$1,$2,$3,'Stored case','Fixture','Saved report text') RETURNING id`, task, assets.DefaultTenantID, owner).Scan(&caseID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	other := insertID("SELECT id FROM auth_user WHERE username='mcp-other'")
	otherChat := uuid.NewString()
	exec("INSERT INTO ai_conversation(id,tenant_id,owner_user_id,title) VALUES($1,$2,$3,'Other private conversation')", otherChat, assets.DefaultTenantID, other)
	exec("INSERT INTO ai_conversation(id,tenant_id,owner_user_id,title) VALUES($1,$2,$3,'Private chat')", chat, assets.DefaultTenantID, owner)
	exec(`INSERT INTO ai_run(id,tenant_id,conversation_id,owner_user_id,status,lease_owner,lease_until,finished_at,terminal_reason)
      VALUES($1,$2,$3,$4,'completed',$5,now(),now(),'completed')`, run, assets.DefaultTenantID, chat, owner, uuid.NewString())
	exec(`INSERT INTO ai_conversation_activity(tenant_id,conversation_id,owner_user_id,run_id,kind,summary)
      VALUES($1,$2,$3,$4,'assistant_message','Private summary')`, assets.DefaultTenantID, chat, owner, run)
	exec(`INSERT INTO ai_conversation_vulnerability_report(tenant_id,owner_user_id,conversation_id,run_id,tool_use_id,input_hash,vulnerability_id,report,evidence)
      VALUES($1,$2,$3,$4,'fixture',repeat('0',64),$5,'{"name":"Stored name","summary":"Scoped finding"}','[]')`, assets.DefaultTenantID, owner, chat, run, v)
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	risk := security.NewVulnerabilityFacade(securityrepo.NewVulnerabilityRepository(orm), nil)
	service := external.New(orm, assets.NewFacade(assetrepo.NewAssetRegistryStore(orm)), aivuln.New(orm, t.TempDir(), risk), risk)
	router := gin.New()
	actor := owner
	service.RegisterManagement(router.Group("/v1", func(c *gin.Context) { c.Set("userClaims", &auth.Claims{UserID: actor}) }))
	router.Any("/mcp", gin.WrapH(service.Handler(nil)))
	send := func(method, path, key string, body any, headers map[string]string) (int, []byte) {
		t.Helper()
		data, _ := json.Marshal(body)
		for i := 0; i < 3; i++ {
			req := httptest.NewRequest(method, path, bytes.NewReader(data))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			if key != "" {
				req.Header.Set("Authorization", "Bearer "+key)
			}
			for name, value := range headers {
				req.Header.Set(name, value)
			}
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)
			if resp.Code == 429 {
				time.Sleep(time.Second)
				continue
			}
			return resp.Code, resp.Body.Bytes()
		}
		t.Fatal("rate limiter did not release")
		return 0, nil
	}
	grants := []string{"list_organizations", "get_organization_asset_overview", "search_asset_inventory", "get_vulnerability", "get_vulnerability_http_evidence", "list_ai_tasks", "get_ai_task_result", "list_ai_conversations", "get_ai_conversation_result", "get_ai_finding_report"}
	policy := map[string]any{"name": "test client", "enabled": true, "allOrganizations": false, "organizationIds": []int{org}, "tools": grants}
	status, data := send("POST", "/v1/mcpClients", "", policy, nil)
	var issued struct {
		ID  int
		Key string
	}
	if status != http.StatusCreated || json.Unmarshal(data, &issued) != nil || issued.Key == "" {
		t.Fatalf("issue status %d", status)
	}
	pathClient := fmt.Sprintf("/v1/mcpClients/%d", issued.ID)
	rpc := func(key, method string, params any) (int, []byte) {
		return send("POST", "/mcp", key, map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}, map[string]string{"MCP-Protocol-Version": "2025-06-18"})
	}
	check := func(method string, params any, want, exclude string) {
		t.Helper()
		status, body := rpc(issued.Key, method, params)
		if status != 200 || (want != "" && !bytes.Contains(body, []byte(want))) || (exclude != "" && bytes.Contains(body, []byte(exclude))) {
			t.Fatalf("%s: status %d, expected %q, excluded %q, response %.400s", method, status, want, exclude, body)
		}
	}
	call := func(name string, args map[string]any, want, exclude string) {
		t.Helper()
		check("tools/call", map[string]any{"name": name, "arguments": args}, want, exclude)
	}
	check("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "test", "version": "1"}}, "2025-06-18", "")
	modern := map[string]string{"MCP-Protocol-Version": "2026-07-28", "Mcp-Method": "server/discover"}
	discover := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "server/discover", "params": map[string]any{"_meta": map[string]any{"io.modelcontextprotocol/protocolVersion": "2026-07-28", "io.modelcontextprotocol/clientInfo": map[string]string{"name": "test", "version": "1"}, "io.modelcontextprotocol/clientCapabilities": map[string]any{}}}}
	if status, reply := send("POST", "/mcp", issued.Key, discover, modern); status != 200 || !bytes.Contains(reply, []byte("skyshield")) {
		t.Fatalf("discovery %d: %.300s", status, reply)
	}
	check("tools/list", map[string]any{}, "get_ai_task_result", "set_vulnerability_review")
	call("list_organizations", map[string]any{}, "Organization A", "Organization B")
	call("get_organization_asset_overview", map[string]any{"organization_id": org}, "rootDomains", "isError")
	call("search_asset_inventory", map[string]any{"organization_id": org}, "totalSize", "Other organization")
	call("get_organization_asset_overview", map[string]any{"organization_id": otherOrg}, "isError", "Organization B")
	call("get_vulnerability", map[string]any{"vulnerability_id": hidden}, "isError", "Other organization")
	call("get_vulnerability_http_evidence", map[string]any{"vulnerability_id": v}, "Stored name", "private-fixture")
	call("set_vulnerability_review", map[string]any{"vulnerability_id": v, "reviewed": true}, "error", "processedCount")
	call("list_ai_tasks", map[string]any{}, "Stored task", "isError")
	call("get_ai_task_result", map[string]any{"task_id": task}, "Stored task", "isError")
	call("get_ai_finding_report", map[string]any{"task_id": task, "case_id": caseID}, "Saved report text", "isError")
	call("list_ai_conversations", map[string]any{}, chat, "Private chat")
	call("get_ai_conversation_result", map[string]any{"conversation_id": chat}, "Scoped finding", "Private summary")
	policy["tools"] = append(grants, "set_vulnerability_review")
	policy["organizationIds"] = []int{}
	policy["allOrganizations"] = true
	if status, _ = send("PUT", pathClient, "", policy, nil); status != 200 {
		t.Fatalf("edit %d", status)
	}
	call("get_ai_conversation_result", map[string]any{"conversation_id": chat}, "Private summary", "isError")
	call("get_ai_conversation_result", map[string]any{"conversation_id": otherChat}, "isError", "Other private conversation")
	call("set_vulnerability_review", map[string]any{"vulnerability_id": v, "reviewed": true}, "reviewed", "isError")
	var reviewed bool
	if err = db.QueryRow("SELECT reviewed FROM vulnerability WHERE id=$1", v).Scan(&reviewed); err != nil || !reviewed {
		t.Fatal("review did not commit", err)
	}
	old := issued.Key
	status, data = send("POST", pathClient+"/rotate", "", nil, nil)
	if status != 200 || json.Unmarshal(data, &issued) != nil || issued.Key == old {
		t.Fatal("rotation failed")
	}
	if status, _ = rpc(old, "tools/list", map[string]any{}); status != 401 {
		t.Fatal("rotated key remained valid")
	}
	actor = other
	if status, _ = send("DELETE", pathClient, "", nil, nil); status != 404 {
		t.Fatalf("other owner deletion returned %d", status)
	}
	actor = owner
	status, _ = send("DELETE", pathClient, "", nil, nil)
	if status != 204 {
		t.Fatal(status)
	}
	if status, _ = rpc(issued.Key, "tools/list", map[string]any{}); status != 401 {
		t.Fatal("revoked key remained valid")
	}
}

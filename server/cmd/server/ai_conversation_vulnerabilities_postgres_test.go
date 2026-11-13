package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/aivuln"
	securitywiring "github.com/yyhuni/lunafox/server/internal/bootstrap/wiring/security"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	catalogrepo "github.com/yyhuni/lunafox/server/internal/modules/catalog/repository"
	securityapp "github.com/yyhuni/lunafox/server/internal/modules/security/application"
	securityrepo "github.com/yyhuni/lunafox/server/internal/modules/security/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyAIConversationVulnerabilities(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var user, other, target int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES('chat-vuln.example','domain') RETURNING id`).Scan(&target); err != nil {
		t.Fatal(err)
	}

	// Exercise the upgrade against reviewed legacy rows and a natural-key collision.
	var legacy int
	if err := db.QueryRowContext(ctx, `INSERT INTO vulnerability(target_id,url,vuln_type,severity,source,description,raw_output,reviewed) VALUES($1,'https://chat-vuln.example/legacy','匿名数据响应（待复核）','info','info-gath','existing','{}',TRUE) RETURNING id`, target).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO vulnerability(target_id,url,vuln_type,severity,source,description,raw_output,reviewed) VALUES($1,'https://chat-vuln.example/collision','匿名数据响应（待复核）','info','info-gath','old','{}',TRUE),($1,'https://chat-vuln.example/collision','匿名数据响应','info','info-gath','new','{}',FALSE)`, target); err != nil {
		t.Fatal(err)
	}
	migration, err := migrationsFS.ReadFile("migrations/000078_ai_conversation_vulnerability_reports.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	update := strings.Split(strings.Split(string(migration), "UPDATE vulnerability v SET")[1], "COMMIT;")[0]
	if _, err = db.ExecContext(ctx, "UPDATE vulnerability v SET"+update); err != nil {
		t.Fatal(err)
	}
	var label string
	var kept bool
	var count int
	if err = db.QueryRowContext(ctx, `SELECT vuln_type,reviewed FROM vulnerability WHERE id=$1`, legacy).Scan(&label, &kept); err != nil || label != "匿名数据响应" || !kept {
		t.Fatalf("legacy review changed: %s %v %v", label, kept, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM vulnerability WHERE target_id=$1`, target).Scan(&count); err != nil || count != 3 {
		t.Fatalf("collision lost data: %d %v", count, err)
	}
	tenant := assetapp.DefaultTenantID
	conversation, run := uuid.NewString(), uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO ai_conversation(id,tenant_id,owner_user_id,title,agent_key) VALUES($1,$2,$3,'finding fixture','pentest')`, conversation, tenant, user); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO ai_run(id,tenant_id,conversation_id,owner_user_id,status,lease_owner,lease_until) VALUES($1,$2,$3,$4,'running',$5,clock_timestamp()+interval '1 hour')`, run, tenant, conversation, user, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO ai_tool_catalog(tenant_id,key,system,description,schema,agents,enabled,kind,exec,deferred) VALUES($1,'report_vulnerability',TRUE,'fixture','{}','["pentest"]',TRUE,'builtin','{}',FALSE) ON CONFLICT(tenant_id,key) DO UPDATE SET enabled=TRUE,agents='["pentest"]'`, tenant); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	writer := securityapp.NewVulnerabilityFacade(securityrepo.NewVulnerabilityRepository(orm), securitywiring.NewSecurityTargetLookupAdapter(catalogrepo.NewTargetRepository(orm)))
	root := t.TempDir()
	work := filepath.Join(root, tenant, "user-"+strconv.Itoa(user), "work", "sessions", conversation)
	if err = os.MkdirAll(work, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(work, "proof.txt"), []byte("immutable evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	svc := aivuln.New(orm, root, writer)
	input := aivuln.Report{URL: "https://chat-vuln.example/api", VulnType: "Access control", Severity: "low", Summary: "Fixture finding", Evidence: "Observed response", EvidencePaths: []string{"proof.txt"}, Request: "GET /api HTTP/1.1\r\nHost: chat-vuln.example\r\n\r\n", Response: "HTTP/1.1 200 OK\r\n\r\nsaved"}
	result, err := svc.Register(ctx, user, run, "call-1", input)
	if err != nil || !result.Created || result.VulnerabilityID == 0 {
		t.Fatalf("register: %#v %v", result, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE vulnerability SET reviewed=TRUE WHERE id=$1`, result.VulnerabilityID); err != nil {
		t.Fatal(err)
	}
	for _, call := range []string{"call-1", "call-2"} {
		repeat, err := svc.Register(ctx, user, run, call, input)
		if err != nil || repeat.VulnerabilityID != result.VulnerabilityID || repeat.Created {
			t.Fatalf("retry %s: %#v %v", call, repeat, err)
		}
	}
	var reviewed bool
	if err = db.QueryRowContext(ctx, `SELECT reviewed FROM vulnerability WHERE id=$1`, result.VulnerabilityID).Scan(&reviewed); err != nil || !reviewed {
		t.Fatalf("review lost: %v %v", reviewed, err)
	}
	bad := input
	bad.Summary = "different"
	if _, err = svc.Register(ctx, user, run, "call-1", bad); err == nil {
		t.Fatal("reused tool ID with changed arguments")
	}
	bad = input
	bad.TargetID = target
	bad.URL = "https://elsewhere.example/api"
	if _, err = svc.Register(ctx, user, run, "out-of-scope", bad); err == nil {
		t.Fatal("out-of-scope URL accepted")
	}
	if _, err = svc.Register(ctx, other, run, "foreign", input); !errors.Is(err, aivuln.ErrForbidden) {
		t.Fatalf("cross-user registration: %v", err)
	}
	if err = os.WriteFile(filepath.Join(work, "proof.txt"), []byte("mutated"), 0600); err != nil {
		t.Fatal(err)
	}
	evidence, err := svc.EvidenceForVulnerability(ctx, user, result.VulnerabilityID)
	if err != nil || len(evidence) != 4 || evidence[1].Text != "immutable evidence" || evidence[0].Request != input.Request {
		t.Fatalf("snapshot: %#v %v", evidence, err)
	}
	foreign, err := svc.EvidenceForVulnerability(ctx, other, result.VulnerabilityID)
	if err != nil || len(foreign) != 0 {
		t.Fatalf("foreign evidence: %#v %v", foreign, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_run SET status='completed' WHERE id=$1`, run); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Register(ctx, user, run, "late", input); !errors.Is(err, aivuln.ErrForbidden) {
		t.Fatalf("terminal run wrote a finding: %v", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_run SET status='running',lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, run); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Register(ctx, user, run, "expired", input); !errors.Is(err, aivuln.ErrForbidden) {
		t.Fatalf("expired run wrote a finding: %v", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_run SET status='completed' WHERE id=$1`, run); err != nil {
		t.Fatal(err)
	}
}

package oast

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/miekg/dns"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// The supplied DSN only creates a uniquely named disposable database. No schema
// or data in the supplied database is changed or dropped.
func isolatedPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("OAST_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set OAST_TEST_POSTGRES_DSN for isolated PostgreSQL integration")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	name := "skyshield_oast_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(`CREATE DATABASE "` + name + `"`); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	cfg.Database = name
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(8)
	t.Cleanup(func() {
		_ = db.Close()
		_, _ = admin.Exec(`DROP DATABASE "` + name + `" WITH (FORCE)`)
		_ = admin.Close()
	})
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	return orm
}

func TestPostgresCallbackLifecycleAndIsolation(t *testing.T) {
	db := isolatedPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	exec := func(query string, args ...any) {
		t.Helper()
		if err := db.Exec(query, args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TABLE tenant(id uuid PRIMARY KEY,status text);
CREATE TABLE auth_user(id integer PRIMARY KEY,is_active boolean);
CREATE TABLE tenant_membership(tenant_id uuid,user_id integer,role text);
CREATE TABLE ai_conversation(id uuid PRIMARY KEY,tenant_id uuid,owner_user_id integer);
CREATE TABLE ai_run(id uuid PRIMARY KEY,tenant_id uuid,conversation_id uuid,owner_user_id integer,status text,lease_until timestamptz);
CREATE TABLE ai_task(id bigint PRIMARY KEY,tenant_id uuid,owner_user_id integer,deleted_at timestamptz,original_target_id integer,target_id integer,active_run uuid,status text,lease_until timestamptz);
CREATE TABLE ai_task_archive(task_id bigint,state text);
CREATE TABLE target(id integer PRIMARY KEY,deleted_at timestamptz);
CREATE TABLE ai_task_main_chat_run(task_id bigint,tenant_id uuid,owner_user_id integer,run_id uuid,status text,lease_until timestamptz);
CREATE SCHEMA tianqiong_ai;
CREATE FUNCTION tianqiong_ai.finding_case_review_conversation_visible(uuid) RETURNS boolean LANGUAGE sql AS 'SELECT true';`)
	// Exercise the actual shared task lease function used in production.
	source, err := os.ReadFile("../../cmd/server/migrations/000062_artex_independent_main_chat.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(source), "CREATE FUNCTION tianqiong_ai.task_run_live(")
	end := strings.Index(string(source)[start:], "CREATE FUNCTION tianqiong_ai.task_run_mode(")
	exec(string(source)[start : start+end])
	migration, err := os.ReadFile("../../cmd/server/migrations/000085_oast_monitor.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(string(migration))
	conversation, other, run, taskRun, mainRun := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO tenant VALUES(?,'active')`, assets.DefaultTenantID)
	exec(`INSERT INTO auth_user VALUES(7,true),(8,true)`)
	exec(`INSERT INTO tenant_membership VALUES(?,7,'admin'),(?,8,'analyst')`, assets.DefaultTenantID, assets.DefaultTenantID)
	exec(`INSERT INTO ai_conversation VALUES(?,?,7),(?,?,7)`, conversation, assets.DefaultTenantID, other, assets.DefaultTenantID)
	exec(`INSERT INTO ai_run VALUES(?,?,?,7,'running',now()+interval '1 hour')`, run, assets.DefaultTenantID, conversation)
	exec(`INSERT INTO ai_task(id,tenant_id,owner_user_id,active_run,status,lease_until) VALUES(41,?,7,?,'running',now()+interval '1 hour')`, assets.DefaultTenantID, taskRun)
	exec(`INSERT INTO ai_task_main_chat_run VALUES(41,?,7,?,'running',now()+interval '1 hour')`, assets.DefaultTenantID, mainRun)
	store := NewPostgresStore(db)
	newService := func() *Service {
		t.Helper()
		s, err := New(store, Config{PublicURL: "http://callback.example.test", DNSZone: "cb.example.test", DNSListen: "127.0.0.1:5353", DNSAnswerIP: "192.0.2.10"}, airepo.NewConversationStore(db), airepo.NewTaskStore(db))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := newService()
	exerciseChatTools(t, s, run, conversation)
	scope := Scope{ConversationID: conversation}
	for _, item := range []struct {
		run      string
		task     int64
		expected Scope
	}{{run, 0, scope}, {taskRun, 41, Scope{TaskID: 41}}, {mainRun, 41, Scope{TaskID: 41}}} {
		got, err := s.runScope(ctx, 7, item.run, item.task)
		if err != nil || got != item.expected {
			t.Fatalf("run scope=%+v %v", got, err)
		}
		if _, err = s.runScope(ctx, 8, item.run, item.task); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-owner run: %v", err)
		}
	}
	p, err := s.Create(ctx, 7, scope, run, CreateInput{Label: "SSRF 参数", RequestID: "tool:original"})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.Create(ctx, 7, scope, run, CreateInput{Label: "SSRF 参数", RequestID: "tool:original"})
	if err != nil || replay.ID != p.ID || replay.HTTPURL != p.HTTPURL {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	if _, err = s.Create(ctx, 7, scope, run, CreateInput{Label: "changed", RequestID: "tool:original"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting replay: %v", err)
	}
	if _, err = s.Poll(ctx, 8, scope, p.ID, "", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user read: %v", err)
	}
	if _, err = s.Poll(ctx, 7, Scope{ConversationID: other}, p.ID, "", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-conversation read: %v", err)
	}
	if err = s.Revoke(ctx, 7, Scope{TaskID: 41}, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-task revoke: %v", err)
	}
	collector := NewCollector(s)
	w := httptest.NewRecorder()
	collector.ServeHTTP(w, httptest.NewRequest("POST", p.HTTPURL+"/trailing/", bytes.NewBufferString("proof\x00bytes")))
	if w.Code != http.StatusNoContent {
		t.Fatalf("callback status %d", w.Code)
	}
	// Reconstruct the service to prove that evidence and capabilities are durable.
	s = newService()
	page, err := s.Poll(ctx, 7, scope, p.ID, "", 0)
	if err != nil || len(page.Items) != 1 || page.Probe.EventCount != 1 {
		t.Fatalf("durable callback: %+v %v", page, err)
	}
	after := page.NextAfter
	addr := startTestDNS(t, NewDNSCollector(collector, "cb.example.test", "192.0.2.10"))
	m := new(dns.Msg)
	m.SetQuestion(p.DNSName+".", dns.TypeA)
	if response, _, err := (&dns.Client{Timeout: time.Second}).Exchange(m, addr); err != nil || response.Rcode != dns.RcodeSuccess {
		t.Fatalf("DNS persistence: %v %v", response, err)
	}
	page, err = s.Poll(ctx, 7, scope, p.ID, after, 0)
	if err != nil || len(page.Items) != 1 || page.Items[0].Protocol != "dns" {
		t.Fatalf("incremental DNS evidence: %+v %v", page, err)
	}
	// A completed AI run rejects new tool activity but may receive late callbacks.
	exec(`UPDATE ai_run SET status='completed' WHERE id=?`, run)
	if _, err = s.runScope(ctx, 7, run, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale run: %v", err)
	}
	if err = s.Record(ctx, p.Token, Event{Protocol: "http", RemoteAddr: "192.0.2.5"}); err != nil {
		t.Fatal(err)
	}
	if err = s.Revoke(ctx, 7, scope, p.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Record(ctx, p.Token, Event{Protocol: "http"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked callback: %v", err)
	}
	page, err = s.Poll(ctx, 7, scope, p.ID, "", 0)
	if err != nil || len(page.Items) != 3 || page.Probe.State != "revoked" {
		t.Fatalf("revoked evidence: %+v %v", page, err)
	}
	// Concurrent appends cannot exceed the cap, including across service instances.
	busy, err := s.Create(ctx, 7, Scope{TaskID: 41}, taskRun, CreateInput{RequestID: "busy"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errorsSeen := make(chan error, MaxEvents+20)
	for i := 0; i < MaxEvents+20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := store.Append(ctx, busy.ID, Event{Protocol: "http", RemoteAddr: "192.0.2.2"})
			if err != nil && !errors.Is(err, ErrNotFound) {
				errorsSeen <- err
			}
		}()
	}
	wg.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Error(err)
	}
	all := 0
	cursor := ""
	for {
		page, err = s.Poll(ctx, 7, Scope{TaskID: 41}, busy.ID, cursor, 0)
		if err != nil {
			t.Fatal(err)
		}
		all += len(page.Items)
		cursor = page.NextAfter
		if !page.HasMore {
			break
		}
	}
	if all != MaxEvents || page.Probe.EventCount != MaxEvents || page.Probe.State != "saturated" {
		t.Fatalf("cap/paging=%d %+v", all, page.Probe)
	}
	// Expiry is enforced by the database, with retained evidence until cleanup.
	expired, err := s.Create(ctx, 7, scope, "", CreateInput{RequestID: "expired"})
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE oast_probe SET expires_at=now()-interval '1 second' WHERE id=?`, expired.ID)
	if err = s.Record(ctx, expired.Token, Event{Protocol: "http"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired callback: %v", err)
	}
	if err = store.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.get(ctx, 7, scope, expired.ID); err != nil {
		t.Fatal("cleanup removed recent evidence")
	}
	exec(`UPDATE oast_probe SET expires_at=now()-interval '8 days' WHERE id=?`, p.ID)
	if err = store.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Raw(`SELECT count(*) FROM oast_event WHERE probe_id=?`, p.ID).Scan(&count)
	if count != 0 {
		t.Fatal("retention left orphan evidence")
	}
	// Reads and callbacks follow the original task's soft-delete visibility.
	live, err := s.Create(ctx, 7, Scope{TaskID: 41}, "", CreateInput{RequestID: "deleted"})
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE ai_task SET deleted_at=now() WHERE id=41`)
	if err = s.Record(ctx, live.Token, Event{Protocol: "http"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted task callback: %v", err)
	}
	if _, err = s.List(ctx, 7, Scope{TaskID: 41}, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted task read: %v", err)
	}
	// Cancellation ends a long poll promptly.
	live, err = s.Create(ctx, 7, scope, "", CreateInput{RequestID: "wait"})
	if err != nil {
		t.Fatal(err)
	}
	short, stop := context.WithTimeout(ctx, 30*time.Millisecond)
	defer stop()
	_, err = s.Poll(short, 7, scope, live.ID, "", 20)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled wait: %v", err)
	}
	for i := 0; i < PageSize+1; i++ {
		if _, err = s.Create(ctx, 7, scope, "", CreateInput{RequestID: fmt.Sprintf("page-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	probes, err := s.List(ctx, 7, scope, "")
	if err != nil || len(probes.Items) != PageSize || probes.NextBefore == "" {
		t.Fatalf("probe pagination: %+v %v", probes, err)
	}
	second, err := s.List(ctx, 7, scope, probes.NextBefore)
	if err != nil || len(second.Items) == 0 || second.Items[0].Sequence >= probes.Items[len(probes.Items)-1].Sequence {
		t.Fatalf("probe page ordering: %+v %v", second, err)
	}
}

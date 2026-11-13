package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/skill"
	_ "github.com/jackc/pgx/v5/stdlib"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	settingsinfra "github.com/yyhuni/lunafox/server/internal/modules/aisettings/infrastructure"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func digestString(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func TestInfoGatherUpgradeOnIsolatedPostgres(t *testing.T) {
	dsn := os.Getenv("TIANQIONG_INFO_GATHER_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL DSN not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	var database string
	if err := db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&database); err != nil || !strings.HasPrefix(database, "tianqiong_info_gather_test_") {
		t.Fatalf("refusing non-test database: %q %v", database, err)
	}
	for _, query := range []string{
		`CREATE TABLE tenant(id UUID PRIMARY KEY,status TEXT NOT NULL)`,
		`CREATE TABLE auth_user(id INTEGER PRIMARY KEY,is_active BOOLEAN NOT NULL)`,
		`CREATE TABLE tenant_membership(tenant_id UUID NOT NULL,user_id INTEGER NOT NULL)`,
		`CREATE TABLE ai_runtime_seed(tenant_id UUID NOT NULL,seed_key TEXT NOT NULL,PRIMARY KEY(tenant_id,seed_key))`,
		`CREATE TABLE ai_skill(id UUID PRIMARY KEY,tenant_id UUID NOT NULL,name TEXT NOT NULL,builtin_key TEXT,enabled BOOLEAN NOT NULL)`,
		`CREATE TABLE ai_skill_agent(tenant_id UUID NOT NULL,skill_id UUID NOT NULL,agent_key TEXT NOT NULL)`,
		`CREATE TABLE ai_skill_file(tenant_id UUID NOT NULL,skill_id UUID NOT NULL,path TEXT NOT NULL,content BYTEA NOT NULL,is_dir BOOLEAN NOT NULL,mode INTEGER NOT NULL,updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),PRIMARY KEY(skill_id,path))`,
	} {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	const skillID = "11111111-1111-4111-8111-111111111111"
	if _, err = db.ExecContext(ctx, `INSERT INTO tenant VALUES($1,'active')`, assets.DefaultTenantID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO auth_user VALUES(7,true)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO tenant_membership VALUES($1,7)`, assets.DefaultTenantID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_skill VALUES($1,$2,'info-gath','info-gath',false)`, skillID, assets.DefaultTenantID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_skill_agent VALUES($1,$2,'info_gatherer')`, assets.DefaultTenantID, skillID); err != nil {
		t.Fatal(err)
	}
	insert := func(name, content string, directory bool, mode int) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `INSERT INTO ai_skill_file(tenant_id,skill_id,path,content,is_dir,mode) VALUES($1,$2,$3,$4,$5,$6)`, assets.DefaultTenantID, skillID, name, []byte(content), directory, mode); err != nil {
			t.Fatal(err)
		}
	}
	insert("scripts", "", true, 0755)
	insert("SKILL.md", "administrator document", false, 0644)
	insert("scripts/a.py", "original a", false, 0755)
	insert("scripts/b.py", "original b", false, 0644)
	insert("scripts/custom.py", "administrator script", false, 0644)
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "info-gath", "scripts"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"a.py": "updated a", "b.py": "updated b", "new.py": "new script"} {
		if err := os.WriteFile(filepath.Join(source, "info-gath", "scripts", name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := NewSkillStore(orm, source)
	original := map[string]string{"scripts/a.py": digestString("original a"), "scripts/b.py": digestString("original b"), "scripts/new.py": ""}
	read := func(name string) (string, int) {
		t.Helper()
		var content []byte
		var mode int
		if err := db.QueryRowContext(ctx, `SELECT content,mode FROM ai_skill_file WHERE skill_id=$1 AND path=$2`, skillID, name).Scan(&content, &mode); err != nil {
			t.Fatal(err)
		}
		return string(content), mode
	}
	if err := store.upgradeInfoGatherScriptsWithBaseline(ctx, original); err != nil {
		t.Fatal(err)
	}
	if content, mode := read("scripts/a.py"); content != "updated a" || mode != 0755 {
		t.Fatalf("upgrade did not preserve mode: %q %o", content, mode)
	}
	if content, _ := read("scripts/new.py"); content != "new script" {
		t.Fatalf("new script missing: %q", content)
	}
	if content, _ := read("SKILL.md"); content != "administrator document" {
		t.Fatal("administrator document was modified")
	}
	if content, _ := read("scripts/custom.py"); content != "administrator script" {
		t.Fatal("unrelated custom script was modified")
	}
	var enabled bool
	if err := db.QueryRowContext(ctx, `SELECT enabled FROM ai_skill WHERE id=$1`, skillID).Scan(&enabled); err != nil || enabled {
		t.Fatalf("enabled setting changed: %v %v", enabled, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_skill SET enabled=true WHERE id=$1`, skillID); err != nil {
		t.Fatal(err)
	}
	materialize := settingsinfra.NewSkillMaterializer(t.TempDir(), store)
	resolved, err := materialize(ctx, 7, "info_gatherer", []skill.Skill{{Name: "info-gath"}})
	if err != nil || len(resolved) != 1 {
		t.Fatalf("runtime materialization failed: %v", err)
	}
	installed, err := os.ReadFile(filepath.Join(resolved[0].Dir, "scripts/a.py"))
	if err != nil || string(installed) != "updated a" {
		t.Fatalf("runtime did not receive the updated script: %q %v", installed, err)
	}
	document, err := os.ReadFile(filepath.Join(resolved[0].Dir, "SKILL.md"))
	if err != nil || string(document) != "administrator document" {
		t.Fatalf("runtime lost the administrator document: %q %v", document, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_skill_file SET content=$1 WHERE skill_id=$2 AND path='scripts/a.py'`, []byte("edit after upgrade"), skillID); err != nil {
		t.Fatal(err)
	}
	if err := store.upgradeInfoGatherScriptsWithBaseline(ctx, original); err != nil {
		t.Fatal(err)
	}
	if content, _ := read("scripts/a.py"); content != "edit after upgrade" {
		t.Fatal("idempotent startup reset a later edit")
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM ai_runtime_seed WHERE tenant_id=$1`, assets.DefaultTenantID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_skill_file SET content=$1 WHERE skill_id=$2 AND path='scripts/a.py'`, []byte("original a"), skillID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_skill_file SET content=$1 WHERE skill_id=$2 AND path='scripts/b.py'`, []byte("administrator edit"), skillID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM ai_skill_file WHERE skill_id=$1 AND path='scripts/new.py'`, skillID); err != nil {
		t.Fatal(err)
	}
	if err := store.upgradeInfoGatherScriptsWithBaseline(ctx, original); err != nil {
		t.Fatal(err)
	}
	if content, _ := read("scripts/a.py"); content != "original a" {
		t.Fatal("partial upgrade applied despite conflicting edit")
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM ai_skill_file WHERE skill_id=$1 AND path='scripts/new.py'`, skillID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("new file inserted despite conflict: %d %v", count, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM ai_runtime_seed WHERE tenant_id=$1`, assets.DefaultTenantID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("conflict incorrectly recorded as complete: %d %v", count, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE ai_skill_file SET content=$1 WHERE skill_id=$2 AND path='scripts/b.py'`, []byte("original b"), skillID); err != nil {
		t.Fatal(err)
	}
	if err := store.upgradeInfoGatherScriptsVersion(ctx, original, "builtin_info_gather_agentic_v1/"); err != nil {
		t.Fatal(err)
	}
	if content, _ := read("scripts/new.py"); content != "new script" {
		t.Fatal("independently versioned phase adapter was not installed")
	}
	if content, _ := read("SKILL.md"); content != "administrator document" {
		t.Fatal("phase upgrade changed the customized Skill document")
	}
	if err := os.WriteFile(filepath.Join(source, "info-gath", "scripts", "new.py"), []byte("resumable script"), 0644); err != nil {
		t.Fatal(err)
	}
	resumeBaseline := map[string]string{"scripts/a.py": digestString("updated a"), "scripts/new.py": digestString("new script")}
	if err := store.upgradeInfoGatherScriptsVersion(ctx, resumeBaseline, "builtin_info_gather_resume_v1/"); err != nil {
		t.Fatal(err)
	}
	if content, _ := read("scripts/new.py"); content != "resumable script" {
		t.Fatal("resumable script was not installed")
	}
	if content, _ := read("SKILL.md"); content != "administrator document" {
		t.Fatal("resume update changed customized documentation")
	}
	insert("references", "", true, 0755)
	insert("references/contract.json", "old contract", false, 0644)
	if err := os.MkdirAll(filepath.Join(source, "info-gath", "references"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "info-gath", "references", "contract.json"), []byte("new contract"), 0644); err != nil {
		t.Fatal(err)
	}
	contractBaseline := map[string]string{"references/contract.json": digestString("old contract")}
	if err := store.upgradeInfoGatherScriptsVersion(ctx, contractBaseline, "builtin_info_gather_risk_contract_v1/"); err != nil {
		t.Fatal(err)
	}
	if content, _ := read("references/contract.json"); content != "new contract" {
		t.Fatal("risk contract was not upgraded")
	}
	if content, _ := read("SKILL.md"); content != "administrator document" {
		t.Fatal("risk contract upgrade changed the customized Skill document")
	}
	for _, query := range []string{
		`CREATE TABLE ai_custom_agent(tenant_id uuid,agent_key text,system_prompt text,created_by int,preset_key text,enabled boolean,updated_at timestamptz default now())`,
		`CREATE TABLE ai_agent_prompt_version(tenant_id uuid,agent_key text,version int,template_text text,updated_by int,UNIQUE(tenant_id,agent_key,version))`,
	} {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO ai_custom_agent(tenant_id,agent_key,system_prompt,created_by,preset_key,enabled) VALUES($1,'info_gatherer',$2,7,'info_gatherer',false)`, assets.DefaultTenantID, infoGathererPreviousPrompt); err != nil {
		t.Fatal(err)
	}
	agents := NewAgentCatalogStore(orm)
	if err := agents.upgradeInfoGathererPrompt(ctx); err != nil {
		t.Fatal(err)
	}
	var prompt string
	if err := db.QueryRowContext(ctx, `SELECT system_prompt,enabled FROM ai_custom_agent WHERE agent_key='info_gatherer'`).Scan(&prompt, &enabled); err != nil || prompt != infoGathererPrompt || enabled {
		t.Fatalf("default prompt or enabled state: enabled=%v err=%v", enabled, err)
	}
	if err := agents.upgradeInfoGathererPrompt(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM ai_agent_prompt_version`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("prompt history or idempotency: %d %v", count, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE ai_custom_agent SET system_prompt='administrator edit'`); err != nil {
		t.Fatal(err)
	}
	if err := agents.upgradeInfoGathererPrompt(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT system_prompt FROM ai_custom_agent`).Scan(&prompt); err != nil || prompt != "administrator edit" {
		t.Fatal("administrator prompt was overwritten")
	}
}

func TestPlanInfoGatherUpgradePreservesCustomFiles(t *testing.T) {
	original := map[string]string{"scripts/a.py": digestString("original a"), "scripts/b.py": digestString("original b"), "scripts/new.py": ""}
	desired := map[string]settings.SkillFile{
		"scripts/a.py":   {Path: "scripts/a.py", Content: []byte("updated a"), Mode: 0644},
		"scripts/b.py":   {Path: "scripts/b.py", Content: []byte("updated b"), Mode: 0644},
		"scripts/new.py": {Path: "scripts/new.py", Content: []byte("new script"), Mode: 0644},
	}
	current := map[string]settings.SkillFile{
		"SKILL.md":     {Path: "SKILL.md", Content: []byte("administrator document")},
		"scripts/a.py": {Path: "scripts/a.py", Content: []byte("original a"), Mode: 0755},
		"scripts/b.py": {Path: "scripts/b.py", Content: []byte("original b"), Mode: 0644},
	}
	updates, conflict := planInfoGatherUpgrade(current, desired, original)
	if conflict != "" || len(updates) != 3 {
		t.Fatalf("expected complete upgrade, got %d updates and %q", len(updates), conflict)
	}
	for _, item := range updates {
		if item.Path == "SKILL.md" || item.Path == "scripts/a.py" && item.Mode != 0755 {
			t.Fatalf("user document or file mode was changed: %+v", item)
		}
	}
	current["scripts/b.py"] = settings.SkillFile{Path: "scripts/b.py", Content: []byte("administrator edit")}
	if updates, conflict = planInfoGatherUpgrade(current, desired, original); conflict != "scripts/b.py" || len(updates) != 0 {
		t.Fatalf("expected an all-or-nothing conflict, got %d updates and %q", len(updates), conflict)
	}
	current["scripts/b.py"] = desired["scripts/b.py"]
	current["scripts/new.py"] = settings.SkillFile{Path: "scripts/new.py", Content: []byte("administrator-created file")}
	if updates, conflict = planInfoGatherUpgrade(current, desired, original); conflict != "scripts/new.py" || len(updates) != 0 {
		t.Fatalf("expected added-file conflict, got %d updates and %q", len(updates), conflict)
	}
	delete(current, "scripts/new.py")
	current["scripts/new.py/child"] = settings.SkillFile{Path: "scripts/new.py/child", Content: []byte("custom child")}
	if updates, conflict = planInfoGatherUpgrade(current, desired, original); conflict != "scripts/new.py" || len(updates) != 0 {
		t.Fatalf("expected overlapping-path conflict, got %d updates and %q", len(updates), conflict)
	}
}

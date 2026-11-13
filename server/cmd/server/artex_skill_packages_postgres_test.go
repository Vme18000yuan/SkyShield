package main

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"github.com/Autumn-27/norma/skill"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	infra "github.com/yyhuni/lunafox/server/internal/modules/aisettings/infrastructure"
	repository "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func verifyARTEXSkillPackagesPostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var admin, member int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&admin); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&member); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	source, err := filepath.Abs("../../../resources/skills")
	if err != nil {
		t.Fatal(err)
	}
	store := repository.NewSkillStore(orm, source)
	if err = store.SeedBuiltinSkills(ctx); err != nil {
		t.Fatal(err)
	}
	facade := settings.NewSkillPackageFacade(store)
	items, err := facade.List(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, item := range items {
		names[item.Name] = true
	}
	for _, name := range []string{"api-recon", "playwright-cli", "scopesentry"} {
		if !names[name] {
			t.Fatalf("native skill missing %s", name)
		}
	}
	text, err := facade.Read(ctx, admin, "api-recon", "SKILL.md")
	if err != nil || !strings.Contains(text, "api-recon") {
		t.Fatalf("builtin document: %q %v", text, err)
	}
	if _, err = facade.Read(ctx, member, "api-recon", "SKILL.md"); !errors.Is(err, settings.ErrForbidden) {
		t.Fatalf("member edited skill files: %v", err)
	}
	description := "edited built-in description"
	if err = facade.Metadata(ctx, admin, "api-recon", settings.SkillMetadataPatch{Description: &description}); err != nil {
		t.Fatal(err)
	}
	if err = store.SeedBuiltinSkills(ctx); err != nil {
		t.Fatal(err)
	}
	edited, err := facade.Read(ctx, admin, "api-recon", "SKILL.md")
	if err != nil || !strings.Contains(edited, description) {
		t.Fatal("startup reset built-in file edit")
	}
	original := []byte(text)
	if err = facade.Write(ctx, admin, "api-recon", settings.SkillFile{Path: "SKILL.md", Content: original}); err != nil {
		t.Fatal(err)
	}
	item := settings.SkillPackage{Name: "native-package-fixture", Description: "native package", Instructions: "Run ${SKILL_DIR}/scripts/check.py", License: "MIT", MCPs: []string{"fixture-mcp"}}
	if err = facade.Create(ctx, admin, item); err != nil {
		t.Fatal(err)
	}
	defer facade.Delete(context.Background(), admin, item.Name)
	if err = facade.Write(ctx, admin, item.Name, settings.SkillFile{Path: "scripts/check.py", Content: []byte("print('native skill')"), Mode: 0755}); err != nil {
		t.Fatal(err)
	}
	if err = facade.Write(ctx, admin, item.Name, settings.SkillFile{Path: "empty", Directory: true}); err != nil {
		t.Fatal(err)
	}
	if err = facade.Write(ctx, admin, item.Name, settings.SkillFile{Path: "../escape", Content: []byte("x")}); !errors.Is(err, settings.ErrInvalidProfile) {
		t.Fatalf("traversal accepted: %v", err)
	}
	if err = facade.Toggle(ctx, admin, item.Name, "auto", true); err != nil {
		t.Fatal(err)
	}
	skills := settings.NewSkillFacade(store)
	dataRoot := t.TempDir()
	skills.SetMaterializer(infra.NewSkillMaterializer(dataRoot, store))
	resolved, err := skills.Resolve(ctx, admin, "auto")
	if err != nil {
		t.Fatal(err)
	}
	var loaded skill.Skill
	for _, candidate := range resolved {
		if candidate.Name == item.Name {
			loaded = candidate
		}
	}
	if loaded.Dir == "" || loaded.License != "MIT" || len(loaded.MCPs) != 1 {
		t.Fatalf("runtime package metadata: %+v", loaded)
	}
	content, err := os.ReadFile(filepath.Join(loaded.Dir, "scripts/check.py"))
	if err != nil || !bytes.Contains(content, []byte("native skill")) {
		t.Fatalf("runtime files: %s %v", content, err)
	}
	archive := func(entries map[string]string) []byte {
		t.Helper()
		var buffer bytes.Buffer
		writer := zip.NewWriter(&buffer)
		for path, content := range entries {
			file, err := writer.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = file.Write([]byte(content))
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		return buffer.Bytes()
	}
	raw := archive(map[string]string{"wrapped/SKILL.md": "---\nname: native-archive-fixture\ndescription: archive\n---\nUse references/data.txt", "wrapped/references/资料.txt": "附件内容"})
	name, count, err := facade.Upload(ctx, admin, bytes.NewReader(raw), false)
	if err != nil || name != "native-archive-fixture" || count != 2 {
		t.Fatalf("zip import: %s %d %v", name, count, err)
	}
	defer facade.Delete(context.Background(), admin, name)
	if _, _, err = facade.Upload(ctx, admin, bytes.NewReader(raw), false); !errors.Is(err, settings.ErrProfileConflict) {
		t.Fatalf("zip overwrite not explicit: %v", err)
	}
	if _, _, err = facade.Upload(ctx, admin, bytes.NewReader(raw), true); err != nil {
		t.Fatal(err)
	}
	bad := archive(map[string]string{"SKILL.md": "---\nname: archive\n---\nbody", "../escape": "x"})
	if _, _, err = facade.Upload(ctx, admin, bytes.NewReader(bad), true); err == nil {
		t.Fatal("unsafe zip path accepted")
	}
	conversations := airepo.NewConversationStore(orm)
	id, run, instance := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = conversations.CreateAgentConversation(ctx, admin, id, "skill stats", "auto"); err != nil {
		t.Fatal(err)
	}
	defer conversations.DeleteOwnedConversations(context.Background(), admin, []string{id})
	if err = conversations.BeginRun(ctx, admin, id, run, instance, "load skill"); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{item.Name, "native-missing-fixture"} {
		if err = conversations.AppendActivity(ctx, admin, id, run, instance, airuntime.Activity{Kind: "tool_use", Tool: "Skill", ToolUseID: []string{"skill-present", "skill-missing"}[i], Detail: `{"name":"` + name + `","args":"fixture"}`}); err != nil {
			t.Fatal(err)
		}
	}
	if err = conversations.FinishRun(ctx, admin, id, run, instance, "completed"); err != nil {
		t.Fatal(err)
	}
	calls, err := facade.Usage(ctx, admin, item.Name, 20)
	if err != nil || len(calls) != 1 || calls[0].AgentKey != "auto" {
		t.Fatalf("skill usage: %+v %v", calls, err)
	}
	missing, err := facade.Missing(ctx, admin, 20)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, record := range missing {
		if record.Skill == "native-missing-fixture" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing-skill attempts not tracked")
	}
	if err = facade.Delete(ctx, admin, "scopesentry"); err != nil {
		t.Fatal(err)
	}
	if err = store.SeedBuiltinSkills(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = facade.Read(ctx, admin, "scopesentry", "SKILL.md"); !errors.Is(err, settings.ErrProfileNotFound) {
		t.Fatalf("deleted builtin recreated: %v", err)
	}
}

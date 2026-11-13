package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	serverdatabase "github.com/yyhuni/lunafox/server/internal/database"
	settingsrepo "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// This opt-in test is destructive to the supplied database. The directory
// checks intentionally restrict it to a private clone under /tmp.
func TestInfoGatherUpgradeExistingPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("LUNAFOX_INFO_GATHER_CLONE_DSN"))
	root := strings.TrimSpace(os.Getenv("LUNAFOX_INFO_GATHER_CLONE_ROOT"))
	if dsn == "" || root == "" {
		t.Skip("isolated clone not configured")
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil || !strings.HasPrefix(root, "/tmp/skyshield-infogather-upgrade-") {
		t.Fatal("refusing non-clone test directory")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var dataDir string
	if err = db.QueryRowContext(ctx, `SHOW data_directory`).Scan(&dataDir); err != nil {
		t.Fatal(err)
	}
	dataDir, err = filepath.EvalSymlinks(dataDir)
	if err != nil || dataDir != filepath.Join(root, "data") {
		t.Fatal("refusing database outside the isolated clone")
	}
	tables := []string{"auth_user", "target", "subdomain", "website", "endpoint", "vulnerability", "asset_entity", "ai_conversation"}
	counts := map[string]int64{}
	for _, table := range tables {
		var count int64
		if err = db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		counts[table] = count
	}
	fingerprints := func(table, key string) (map[string]string, error) {
		rows, err := db.QueryContext(ctx, fmt.Sprintf(`SELECT %s,md5(row_to_json(x)::text) FROM %s x`, key, table))
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		result := map[string]string{}
		for rows.Next() {
			var id, hash string
			if err = rows.Scan(&id, &hash); err != nil {
				return nil, err
			}
			result[id] = hash
		}
		return result, rows.Err()
	}
	agents, err := fingerprints("ai_custom_agent", "agent_key")
	if err != nil {
		t.Fatal(err)
	}
	skills, err := fingerprints("ai_skill", "id::text")
	if err != nil {
		t.Fatal(err)
	}
	oldFS, oldPath := serverdatabase.MigrationsFS, serverdatabase.MigrationsPath
	serverdatabase.MigrationsFS, serverdatabase.MigrationsPath = migrationsFS, "migrations"
	defer func() { serverdatabase.MigrationsFS, serverdatabase.MigrationsPath = oldFS, oldPath }()
	if err = serverdatabase.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = settingsrepo.NewAgentCatalogStore(orm).SeedDefaultAgents(ctx); err != nil {
		t.Fatal(err)
	}
	skillRoot, err := filepath.Abs("../../../resources/skills")
	if err != nil {
		t.Fatal(err)
	}
	if err = settingsrepo.NewSkillStore(orm, skillRoot).SeedBuiltinSkills(ctx); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		var after int64
		if err = db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&after); err != nil || after != counts[table] {
			t.Fatalf("%s changed during upgrade: %d -> %d, %v", table, counts[table], after, err)
		}
	}
	for table, before := range map[string]map[string]string{"ai_custom_agent": agents, "ai_skill": skills} {
		key := "agent_key"
		if table == "ai_skill" {
			key = "id::text"
		}
		after, err := fingerprints(table, key)
		if err != nil {
			t.Fatal(err)
		}
		for id, hash := range before {
			if after[id] != hash {
				t.Fatalf("existing %s setting was modified", table)
			}
		}
	}
	var tableExists bool
	if err = db.QueryRowContext(ctx, `SELECT to_regclass('public.ai_info_gather_run') IS NOT NULL`).Scan(&tableExists); err != nil || !tableExists {
		t.Fatalf("collection migration missing: %v", err)
	}
}

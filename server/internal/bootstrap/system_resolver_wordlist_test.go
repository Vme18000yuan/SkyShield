package bootstrap

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	catalogapp "github.com/yyhuni/lunafox/server/internal/modules/catalog/application"
	catalogrepo "github.com/yyhuni/lunafox/server/internal/modules/catalog/repository"
)

func TestSystemResolverUpdatesOnlyManagedDefaultAndPreservesUserEdit(t *testing.T) {
	db, base, imports := newWordlistBootstrapFixture(t)
	imports[0].entry.FileName = "resolvers.txt"
	ctx := context.Background()
	if err := bootstrapDefaultWordlists(ctx, db, base, imports); err != nil {
		t.Fatal(err)
	}
	repository := catalogrepo.NewWordlistRepository(db)
	rows, err := repository.ListAllContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := 0
	for _, row := range rows {
		if row.FileName == "resolvers.txt" {
			id = row.ID
		}
	}
	if id == 0 {
		t.Fatal("resolver fixture missing")
	}
	for _, desired := range []string{"127.0.0.53\n", "10.40.0.53\n"} {
		if err := syncSystemResolverWordlist(ctx, db, base, imports, desired); err != nil {
			t.Fatal(err)
		}
		row, err := repository.GetByID(id)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(row.FilePath)
		if err != nil || string(data) != desired || row.FileHash != dnsContentHash(data) || row.LineCount != 1 || row.FileSize != int64(len(data)) {
			t.Fatalf("managed resolver metadata: %+v %q %v", row, data, err)
		}
	}
	service := catalogapp.NewWordlistCommandService(repository, base, catalogapp.NewLocalWordlistFileStore())
	if _, err := service.UpdateWordlistContent(ctx, id, "192.168.90.53\n"); err != nil {
		t.Fatal(err)
	}
	if err := syncSystemResolverWordlist(ctx, db, base, imports, "10.41.0.53\n"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(base, "resolvers.txt"))
	if string(data) != "192.168.90.53\n" {
		t.Fatal("user resolver edit was overwritten")
	}
	for _, row := range rows {
		if row.ID == id {
			continue
		}
		data, err := os.ReadFile(row.FilePath)
		if err != nil || dnsContentHash(data) != row.FileHash {
			t.Fatal("unrelated wordlist was modified")
		}
	}
}

func TestSystemResolverReconcilesInterruptedWriteAndRollsBackDatabaseFailure(t *testing.T) {
	db, base, imports := newWordlistBootstrapFixture(t)
	imports[0].entry.FileName = "resolvers.txt"
	ctx := context.Background()
	if err := bootstrapDefaultWordlists(ctx, db, base, imports); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "resolvers.txt")
	previous, _ := os.ReadFile(path)
	desired := "10.60.0.53\n"
	marker, _ := json.Marshal(managedDNSState{Applied: dnsContentHash(previous), Pending: dnsContentHash([]byte(desired))})
	if err := os.WriteFile(filepath.Join(base, ".skyshield-system-dns.json"), marker, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeDNSFile(path, []byte(desired), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := syncSystemResolverWordlist(ctx, db, base, imports, desired); err != nil {
		t.Fatal(err)
	}
	rows, err := catalogrepo.NewWordlistRepository(db).ListAllContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.FileName == "resolvers.txt" && row.FileHash != dnsContentHash([]byte(desired)) {
			t.Fatal("interrupted resolver metadata did not recover")
		}
	}
	if err := db.Exec(`CREATE TRIGGER fail_resolver_update BEFORE UPDATE ON wordlist BEGIN SELECT RAISE(FAIL, 'fixture failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if err := syncSystemResolverWordlist(ctx, db, base, imports, "10.61.0.53\n"); err == nil {
		t.Fatal("database failure was ignored")
	}
	data, _ := os.ReadFile(path)
	if string(data) != desired {
		t.Fatalf("resolver content was not restored: %q", data)
	}
}

func TestSystemResolverOwnershipDecisionDoesNotClaimCustomContent(t *testing.T) {
	seed, custom := dnsContentHash([]byte("8.8.8.8\n")), dnsContentHash([]byte("192.168.1.53\n"))
	if resolverContentIsManaged(custom, seed, nil) || !resolverContentIsManaged(seed, seed, nil) {
		t.Fatal("default resolver ownership detection failed")
	}
	if resolverContentIsManaged(custom, seed, &managedDNSState{Applied: strings.Repeat("a", 64)}) {
		t.Fatal("custom content was claimed as managed")
	}
}

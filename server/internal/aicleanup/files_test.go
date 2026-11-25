package aicleanup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestCleanupNeverFollowsWorkspaceSymlinks(t *testing.T) {
	base := t.TempDir()
	tenant, id := uuid.NewString(), uuid.NewString()
	owner := filepath.Join(base, tenant, "user-7")
	if err := os.MkdirAll(filepath.Join(owner, "work", "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	protected := filepath.Join(outside, "keep")
	if err := os.WriteFile(protected, []byte("another owner"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(owner, "work", "sessions", id)); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(cleanupPayload{Conversations: []string{id}})
	service := New(nil, base, nil)
	job := cleanupJob{TenantID: tenant, OwnerUserID: 7, Kind: "conversation", ScopeID: id, Payload: payload}
	if err := service.removeFiles(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if value, err := os.ReadFile(protected); err != nil || string(value) != "another owner" {
		t.Fatal("outside file changed", err)
	}
	if err := service.removeFiles(context.Background(), job); err != nil {
		t.Fatal("retry failed", err)
	}
	job.Payload = []byte(`{"conversations":["../../another-owner"]}`)
	if err := service.removeFiles(context.Background(), job); err == nil {
		t.Fatal("invalid identifier accepted")
	}
}

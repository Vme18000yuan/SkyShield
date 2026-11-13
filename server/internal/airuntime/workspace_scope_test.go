package airuntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceRejectsSiblingAndSymlinkRoots(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "work")
	ctx := withWorkspace(t.Context(), work, filepath.Join(root, "outside"))
	if workspaceScopeError(ctx) == nil {
		t.Fatal("workspace escaped user root")
	}
	if err := os.MkdirAll(filepath.Join(work, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(work, "sessions", "bad")); err != nil {
		t.Fatal(err)
	}
	ctx = withWorkspace(t.Context(), work, filepath.Join(work, "sessions", "bad"))
	if workspaceScopeError(ctx) == nil {
		t.Fatal("symlink workspace accepted")
	}
	ctx = withWorkspace(t.Context(), work, filepath.Join(work, "sessions", "good"))
	path, read := scopedWorkspace(ctx, work)
	if workspaceScopeError(ctx) != nil || path == work || len(read) != 3 {
		t.Fatalf("invalid scoped grants: %s %v", path, read)
	}
}

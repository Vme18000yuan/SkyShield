package airuntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type workspaceScope struct {
	root string
	read []string
	err  error
}
type workspaceScopeKey struct{}

func withWorkspace(ctx context.Context, userWork, root string) context.Context {
	scope := workspaceScope{root: root}
	if err := os.MkdirAll(userWork, 0700); err != nil {
		scope.err = err
		return context.WithValue(ctx, workspaceScopeKey{}, scope)
	}
	rel, err := filepath.Rel(userWork, root)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		scope.err = errors.New("invalid execution workspace")
		return context.WithValue(ctx, workspaceScopeKey{}, scope)
	}
	for path := root; path != userWork; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			scope.err = errors.New("执行工作目录不能包含符号链接")
			return context.WithValue(ctx, workspaceScopeKey{}, scope)
		}
		if err != nil && !os.IsNotExist(err) {
			scope.err = err
			return context.WithValue(ctx, workspaceScopeKey{}, scope)
		}
	}
	// Shared code and evidence are read-only grants, not writable parent roots.
	var read []string
	for _, path := range []string{filepath.Join(userWork, ".skills"), filepath.Join(userWork, "info-gather"), filepath.Join(userWork, "noa")} {
		_ = os.MkdirAll(path, 0700)
		if info, err := os.Lstat(path); err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			read = append(read, path)
		}
	}
	scope.read = read
	return context.WithValue(ctx, workspaceScopeKey{}, scope)
}

func workspaceScopeError(ctx context.Context) error {
	scope, _ := ctx.Value(workspaceScopeKey{}).(workspaceScope)
	return scope.err
}
func scopedWorkspace(ctx context.Context, fallback string) (string, []string) {
	if scope, ok := ctx.Value(workspaceScopeKey{}).(workspaceScope); ok {
		return scope.root, append([]string(nil), scope.read...)
	}
	return fallback, nil
}

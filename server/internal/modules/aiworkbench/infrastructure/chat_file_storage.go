package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

// ChatFileStorage confines all attachment operations to an owned AI workspace.
// os.Root prevents a symlink inside an upload directory from escaping that root.
type ChatFileStorage struct{ root string }

func NewChatFileStorage(root string) *ChatFileStorage { return &ChatFileStorage{root: root} }
func (s *ChatFileStorage) workspace(user int) (string, error) {
	if user <= 0 || !filepath.IsAbs(s.root) {
		return "", aiapp.ErrInvalidInput
	}
	return filepath.Join(s.root, assets.DefaultTenantID, fmt.Sprintf("user-%d", user), "work"), nil
}
func (s *ChatFileStorage) open(user int) (*os.Root, error) {
	path, err := s.workspace(user)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(path, 0700); err != nil {
		return nil, err
	}
	return os.OpenRoot(path)
}
func scopePath(scope, id string) (string, error) {
	sub := map[string]string{"session": "sessions", "task": "tasks", "staging": "drafts"}[scope]
	if sub == "" || id == "" || strings.ContainsAny(id, "/\\.") {
		return "", aiapp.ErrInvalidInput
	}
	return filepath.Join(sub, id), nil
}
func (s *ChatFileStorage) Save(ctx context.Context, user int, scope, id, name string, reader io.Reader) (aiapp.ChatAttachment, error) {
	root, err := s.open(user)
	if err != nil {
		return aiapp.ChatAttachment{}, err
	}
	defer root.Close()
	base, err := scopePath(scope, id)
	if err != nil {
		return aiapp.ChatAttachment{}, err
	}
	dir := filepath.Join(base, "uploads")
	if err = root.MkdirAll(dir, 0700); err != nil {
		return aiapp.ChatAttachment{}, err
	}
	filename := uuid.NewString()[:8] + "-" + name
	path := filepath.Join(dir, filename)
	file, err := root.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return aiapp.ChatAttachment{}, err
	}
	size, copyErr := io.Copy(file, io.LimitReader(reader, aiapp.MaxChatUpload+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || size > aiapp.MaxChatUpload || ctx.Err() != nil {
		_ = root.Remove(path)
		if copyErr != nil {
			return aiapp.ChatAttachment{}, copyErr
		}
		if closeErr != nil {
			return aiapp.ChatAttachment{}, closeErr
		}
		if ctx.Err() != nil {
			return aiapp.ChatAttachment{}, ctx.Err()
		}
		return aiapp.ChatAttachment{}, aiapp.ErrInvalidInput
	}
	workspace, _ := s.workspace(user)
	return aiapp.ChatAttachment{Name: name, Path: "uploads/" + filename, Size: size, Abs: filepath.Join(workspace, path)}, nil
}
func (s *ChatFileStorage) Absolute(user int, scope, id, path string) (string, error) {
	if !strings.HasPrefix(path, "uploads/") || strings.Count(path, "/") != 1 || filepath.ToSlash(filepath.Clean(path)) != path || strings.ContainsAny(path, "\\\x00") {
		return "", aiapp.ErrInvalidInput
	}
	base, err := scopePath(scope, id)
	if err != nil {
		return "", err
	}
	root, err := s.open(user)
	if err != nil {
		return "", err
	}
	defer root.Close()
	file, err := root.Open(filepath.Join(base, path))
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", aiapp.ErrInvalidInput
	}
	workspace, _ := s.workspace(user)
	return filepath.Join(workspace, base, path), nil
}
func (s *ChatFileStorage) Remove(user int, scope, id, path string) error {
	base, err := scopePath(scope, id)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(path, "uploads/") || strings.Count(path, "/") != 1 || filepath.ToSlash(filepath.Clean(path)) != path {
		return aiapp.ErrInvalidInput
	}
	root, err := s.open(user)
	if err != nil {
		return err
	}
	defer root.Close()
	err = root.Remove(filepath.Join(base, path))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
func (s *ChatFileStorage) RemoveSession(user int, id string) error {
	base, err := scopePath("session", id)
	if err != nil {
		return err
	}
	root, err := s.open(user)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.RemoveAll(base)
}

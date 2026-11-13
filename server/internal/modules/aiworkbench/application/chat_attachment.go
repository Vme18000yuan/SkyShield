package application

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
)

const MaxChatUpload = 128 << 20

type ChatAttachment struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	Abs  string `json:"abs,omitempty"`
}
type ChatAttachmentStore interface {
	AuthorizeAttachmentScope(context.Context, int, string, string) error
	SaveChatAttachment(context.Context, int, string, string, ChatAttachment) error
	FindChatAttachments(context.Context, int, string, string, []string) ([]ChatAttachment, error)
}
type ChatFileStorage interface {
	Save(context.Context, int, string, string, string, io.Reader) (ChatAttachment, error)
	Absolute(int, string, string, string) (string, error)
	Remove(int, string, string, string) error
	RemoveSession(int, string) error
}
type AttachmentFacade struct {
	store ChatAttachmentStore
	files ChatFileStorage
}

func NewAttachmentFacade(store ChatAttachmentStore, files ChatFileStorage) *AttachmentFacade {
	return &AttachmentFacade{store: store, files: files}
}

var attachmentScopeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func normalizeAttachmentScope(scope, id string) (string, error) {
	if scope == "session" {
		id = strings.TrimPrefix(id, "conv-")
		if !validConversationID(id) {
			return "", ErrInvalidInput
		}
	} else if (scope != "task" && scope != "staging") || !attachmentScopeID.MatchString(id) {
		return "", ErrInvalidInput
	}
	return id, nil
}
func (f *AttachmentFacade) Upload(ctx context.Context, user int, scope, id, name string, reader io.Reader) (ChatAttachment, error) {
	id, err := normalizeAttachmentScope(scope, id)
	if err != nil || user <= 0 || reader == nil {
		return ChatAttachment{}, ErrInvalidInput
	}
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || len(name) > 240 || strings.ContainsAny(name, "/\\\x00\r\n") {
		return ChatAttachment{}, ErrInvalidInput
	}
	if err = f.store.AuthorizeAttachmentScope(ctx, user, scope, id); err != nil {
		return ChatAttachment{}, err
	}
	file, err := f.files.Save(ctx, user, scope, id, name, reader)
	if err != nil {
		return ChatAttachment{}, err
	}
	file.ID = uuid.NewString()
	if err = f.store.SaveChatAttachment(ctx, user, scope, id, file); err != nil {
		_ = f.files.Remove(user, scope, id, file.Path)
		return ChatAttachment{}, err
	}
	return file, nil
}
func (f *AttachmentFacade) Compose(ctx context.Context, user int, scope, id, text string, requested []ChatAttachment) (string, airuntime.Activity, error) {
	text = strings.TrimSpace(text)
	activity := airuntime.Activity{Kind: "user", Summary: chatPreview(text, 200), Detail: text}
	if len(text) > 64*1024 || len(requested) > 20 || (text == "" && len(requested) == 0) {
		return "", activity, ErrInvalidInput
	}
	id, err := normalizeAttachmentScope(scope, id)
	if err != nil {
		return "", activity, err
	}
	if err = f.store.AuthorizeAttachmentScope(ctx, user, scope, id); err != nil {
		return "", activity, err
	}
	if len(requested) == 0 {
		return text, activity, nil
	}
	paths := []string{}
	seen := map[string]bool{}
	for _, item := range requested {
		if item.Path == "" || seen[item.Path] {
			return "", activity, ErrInvalidInput
		}
		seen[item.Path] = true
		paths = append(paths, item.Path)
	}
	files, err := f.store.FindChatAttachments(ctx, user, scope, id, paths)
	if err != nil {
		return "", activity, err
	}
	if len(files) != len(paths) {
		return "", activity, ErrInvalidInput
	}
	found := map[string]ChatAttachment{}
	for _, file := range files {
		found[file.Path] = file
	}
	ordered := make([]ChatAttachment, 0, len(paths))
	var message strings.Builder
	message.WriteString(text)
	message.WriteString("\n\n【用户上传的附件】(绝对路径，需要时用 Read/Bash 查看)：")
	for _, path := range paths {
		file, ok := found[path]
		if !ok {
			return "", activity, ErrInvalidInput
		}
		absolute, err := f.files.Absolute(user, scope, id, path)
		if err != nil {
			return "", activity, err
		}
		file.Abs = ""
		ordered = append(ordered, file)
		fmt.Fprintf(&message, "\n- %s（%d B）", absolute, file.Size)
	}
	blob, err := json.Marshal(map[string]any{"text": text, "attachments": ordered})
	if err != nil {
		return "", activity, err
	}
	activity.Detail = string(blob)
	if activity.Summary == "" {
		names := []string{}
		for _, file := range ordered {
			names = append(names, file.Name)
		}
		activity.Summary = chatPreview("附件："+strings.Join(names, "、"), 200)
	}
	return message.String(), activity, nil
}
func chatPreview(value string, limit int) string {
	value = strings.TrimSpace(value)
	if i := strings.IndexAny(value, "\r\n"); i >= 0 {
		value = value[:i]
	}
	if utf8.RuneCountInString(value) > limit {
		return string([]rune(value)[:limit]) + "…"
	}
	return value
}
func safeAttachmentPath(path string) bool {
	return strings.HasPrefix(path, "uploads/") && filepath.ToSlash(filepath.Clean(path)) == path && !strings.ContainsAny(path, "\\\x00") && strings.Count(path, "/") == 1
}

func (f *ConversationFacade) SetAttachments(attachments *AttachmentFacade) {
	f.attachments = attachments
}
func (f *ConversationFacade) UploadAttachment(ctx context.Context, user int, scope, id, name string, reader io.Reader) (ChatAttachment, error) {
	if f.attachments == nil {
		return ChatAttachment{}, ErrInvalidInput
	}
	return f.attachments.Upload(ctx, user, scope, id, name, reader)
}
func (f *ConversationFacade) SendWithAttachments(ctx context.Context, user int, id, text string, attachments []ChatAttachment) (string, error) {
	if len(attachments) == 0 {
		return f.Send(ctx, user, id, text)
	}
	if f.attachments == nil {
		return "", ErrInvalidInput
	}
	message, activity, err := f.attachments.Compose(ctx, user, "session", id, text, attachments)
	if err != nil {
		return "", err
	}
	return f.send(ctx, user, id, message, &activity, strings.TrimSpace(text))
}

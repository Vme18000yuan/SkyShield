package application

import (
	"context"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"path"
	"strings"
	"time"
	"unicode/utf8"
)

type SkillFile struct {
	Path      string
	Content   []byte
	Directory bool
	Mode      int
}
type SkillPackage struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	License       string     `json:"license,omitempty"`
	Compatibility string     `json:"compatibility,omitempty"`
	MCPs          []string   `json:"mcps"`
	Instructions  string     `json:"-"`
	Files         []string   `json:"files"`
	Calls         int64      `json:"calls"`
	Tasks         int64      `json:"tasks"`
	UsageAgents   []string   `json:"usage_agents"`
	LastUsed      *time.Time `json:"last_used,omitempty"`
	Enabled       bool       `json:"enabled"`
}
type SkillMetadataPatch struct {
	MCPs          *[]string `json:"mcps"`
	Description   *string   `json:"description"`
	License       *string   `json:"license"`
	Compatibility *string   `json:"compatibility"`
}
type SkillCall struct {
	TS        time.Time `json:"ts"`
	AgentKey  string    `json:"agent_key"`
	TaskID    int64     `json:"task_id"`
	SessionID string    `json:"session_id"`
	ArgsLen   int       `json:"args_len"`
}
type MissingSkill struct {
	Skill    string    `json:"skill"`
	Calls    int64     `json:"calls"`
	Agents   []string  `json:"agents"`
	LastUsed time.Time `json:"last_used"`
}
type SkillPackageStore interface {
	ListSkillPackages(context.Context, int) ([]SkillPackage, error)
	SkillPackageFiles(context.Context, int, string) ([]SkillFile, error)
	SaveSkillPackage(context.Context, int, SkillPackage, []SkillFile, bool, bool) error
	SaveSkillPackagePath(context.Context, int, string, SkillFile, bool) error
	DeleteSkillPackage(context.Context, int, string) error
	SkillPackageVisibility(context.Context, int, string) ([]string, error)
	SetSkillPackageVisibility(context.Context, int, string, string, bool) error
	SkillPackageUsage(context.Context, int, string, int) ([]SkillCall, error)
	MissingSkillUsage(context.Context, int, int) ([]MissingSkill, error)
}
type SkillPackageFacade struct{ store SkillPackageStore }

func NewSkillPackageFacade(store SkillPackageStore) *SkillPackageFacade {
	return &SkillPackageFacade{store: store}
}
func validSkillPackageName(name string) bool {
	return name != "" && name != "." && !strings.Contains(name, "..") && len(name) <= 120 && utf8.ValidString(name) && !strings.ContainsAny(name, "/\\\r\n\x00")
}
func ValidSkillFilePath(value string) bool {
	return value != "" && len(value) <= 512 && utf8.ValidString(value) && path.Clean(value) == value && !strings.HasPrefix(value, "/") && !strings.Contains(value, "..") && !strings.ContainsAny(value, "\\\x00\r\n\t")
}
func ParseSkillDocument(name string, content []byte) (SkillPackage, error) {
	result := SkillPackage{Name: name, Enabled: true, MCPs: []string{}, Files: []string{}, UsageAgents: []string{}}
	if !validSkillPackageName(name) || len(content) > 20<<20 || !utf8.Valid(content) {
		return result, ErrInvalidProfile
	}
	body := string(content)
	if strings.HasPrefix(body, "---") {
		rest := strings.TrimLeft(strings.TrimPrefix(body, "---"), "\r\n")
		end := strings.Index(rest, "\n---")
		if end < 0 {
			result.Instructions = strings.TrimSpace(body)
			return result, nil
		}
		var meta map[string]any
		if err := yaml.Unmarshal([]byte(rest[:end]), &meta); err != nil {
			result.Instructions = strings.TrimSpace(strings.TrimLeft(rest[end+4:], "-\r\n"))
			return result, nil
		}
		text := func(key string) string { value, _ := meta[key].(string); return value }
		if declared := text("name"); declared != "" && declared != name {
			return result, ErrInvalidProfile
		}
		result.Description = text("description")
		result.License = text("license")
		result.Compatibility = text("compatibility")
		switch value := meta["mcps"].(type) {
		case string:
			for _, part := range strings.Split(value, ",") {
				if part = strings.TrimSpace(part); part != "" {
					result.MCPs = append(result.MCPs, part)
				}
			}
		case []any:
			for _, part := range value {
				if name, ok := part.(string); ok && strings.TrimSpace(name) != "" {
					result.MCPs = append(result.MCPs, strings.TrimSpace(name))
				}
			}
		}
		body = strings.TrimLeft(rest[end+4:], "-\r\n")
	}
	result.Instructions = strings.TrimSpace(body)
	return result, nil
}
func RenderSkillDocument(item SkillPackage) []byte {
	meta := map[string]any{"name": item.Name, "description": item.Description}
	if item.License != "" {
		meta["license"] = item.License
	}
	if item.Compatibility != "" {
		meta["compatibility"] = item.Compatibility
	}
	if len(item.MCPs) > 0 {
		meta["mcps"] = item.MCPs
	}
	raw, _ := yaml.Marshal(meta)
	return []byte("---\n" + string(raw) + "---\n" + item.Instructions)
}
func (f *SkillPackageFacade) List(ctx context.Context, user int) ([]SkillPackage, error) {
	if user <= 0 {
		return nil, ErrForbidden
	}
	return f.store.ListSkillPackages(ctx, user)
}
func (f *SkillPackageFacade) Create(ctx context.Context, user int, item SkillPackage) error {
	if user <= 0 {
		return ErrForbidden
	}
	if !validSkillPackageName(item.Name) || strings.TrimSpace(item.Description) == "" {
		return ErrInvalidProfile
	}
	if strings.TrimSpace(item.Instructions) == "" {
		item.Instructions = "## " + item.Name + "\n\n<!-- Describe step-by-step instructions in Markdown. -->\n\n1. \n2. \n3. \n"
	}
	item.Enabled = true
	return f.store.SaveSkillPackage(ctx, user, item, []SkillFile{{Path: "SKILL.md", Content: RenderSkillDocument(item), Mode: 0644}}, true, false)
}
func (f *SkillPackageFacade) Files(ctx context.Context, user int, name string) ([]SkillFile, error) {
	if user <= 0 {
		return nil, ErrForbidden
	}
	if !validSkillPackageName(name) {
		return nil, ErrInvalidProfile
	}
	return f.store.SkillPackageFiles(ctx, user, name)
}
func (f *SkillPackageFacade) Read(ctx context.Context, user int, name, file string) (string, error) {
	if !ValidSkillFilePath(file) {
		return "", ErrInvalidProfile
	}
	files, err := f.Files(ctx, user, name)
	if err != nil {
		return "", err
	}
	for _, entry := range files {
		if entry.Path == file && !entry.Directory {
			if !utf8.Valid(entry.Content) {
				return "", fmt.Errorf("二进制文件无法作为文本编辑")
			}
			return string(entry.Content), nil
		}
	}
	return "", ErrProfileNotFound
}
func (f *SkillPackageFacade) Write(ctx context.Context, user int, name string, file SkillFile) error {
	if user <= 0 {
		return ErrForbidden
	}
	if !validSkillPackageName(name) || !ValidSkillFilePath(file.Path) || len(file.Content) > 20<<20 {
		return ErrInvalidProfile
	}
	if file.Path == "SKILL.md" {
		if _, err := ParseSkillDocument(name, file.Content); err != nil {
			return err
		}
	}
	if file.Mode == 0 {
		file.Mode = 0644
	}
	return f.store.SaveSkillPackagePath(ctx, user, name, file, false)
}
func (f *SkillPackageFacade) DeletePath(ctx context.Context, user int, name, file string) error {
	if user <= 0 {
		return ErrForbidden
	}
	if !validSkillPackageName(name) || !ValidSkillFilePath(file) {
		return ErrInvalidProfile
	}
	return f.store.SaveSkillPackagePath(ctx, user, name, SkillFile{Path: file}, true)
}
func (f *SkillPackageFacade) Delete(ctx context.Context, user int, name string) error {
	if user <= 0 {
		return ErrForbidden
	}
	if !validSkillPackageName(name) {
		return ErrInvalidProfile
	}
	return f.store.DeleteSkillPackage(ctx, user, name)
}
func (f *SkillPackageFacade) Metadata(ctx context.Context, user int, name string, patch SkillMetadataPatch) error {
	raw, err := f.Read(ctx, user, name, "SKILL.md")
	if err != nil {
		return err
	}
	updated, err := rewriteSkillMetadata([]byte(raw), patch)
	if err != nil {
		return err
	}
	return f.Write(ctx, user, name, SkillFile{Path: "SKILL.md", Content: updated, Mode: 0644})
}
func (f *SkillPackageFacade) Visibility(ctx context.Context, user int, name string) ([]string, error) {
	if user <= 0 {
		return nil, ErrForbidden
	}
	return f.store.SkillPackageVisibility(ctx, user, name)
}
func (f *SkillPackageFacade) Toggle(ctx context.Context, user int, name, agent string, on bool) error {
	if user <= 0 {
		return ErrForbidden
	}
	if !validSkillPackageName(name) || !skillAgentKeyPattern.MatchString(agent) {
		return ErrInvalidProfile
	}
	return f.store.SetSkillPackageVisibility(ctx, user, name, agent, on)
}
func (f *SkillPackageFacade) Usage(ctx context.Context, user int, name string, limit int) ([]SkillCall, error) {
	if user <= 0 {
		return nil, ErrForbidden
	}
	if limit < 1 || limit > 500 {
		return nil, ErrInvalidProfile
	}
	return f.store.SkillPackageUsage(ctx, user, name, limit)
}
func (f *SkillPackageFacade) Missing(ctx context.Context, user, limit int) ([]MissingSkill, error) {
	if user <= 0 {
		return nil, ErrForbidden
	}
	if limit < 1 || limit > 500 {
		return nil, ErrInvalidProfile
	}
	return f.store.MissingSkillUsage(ctx, user, limit)
}
func (f *SkillPackageFacade) Upload(ctx context.Context, user int, reader io.Reader, overwrite bool) (string, int, error) {
	if _, err := f.List(ctx, user); err != nil {
		return "", 0, err
	}
	raw, err := io.ReadAll(io.LimitReader(reader, 20<<20+1))
	if err != nil || len(raw) > 20<<20 {
		return "", 0, ErrInvalidProfile
	}
	item, files, err := ReadSkillArchive(raw)
	if err != nil {
		return "", 0, err
	}
	if err = f.store.SaveSkillPackage(ctx, user, item, files, true, overwrite); err != nil {
		return "", 0, err
	}
	return item.Name, len(files), nil
}

package application

import (
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

func ReadSkillArchive(raw []byte) (SkillPackage, []SkillFile, error) {
	var item SkillPackage
	reader, err := newSkillZipReader(raw)
	if err != nil {
		return item, nil, err
	}
	entries := skillZipEntries(reader)
	if len(entries) == 0 || len(entries) > 4000 {
		return item, nil, ErrInvalidProfile
	}
	if err = checkSkillZipMethods(entries); err != nil {
		return item, nil, err
	}
	root := ""
	found := false
	depth := 1000
	for _, entry := range entries {
		if !ValidSkillFilePath(entry.name) || entry.f.Mode()&os.ModeSymlink != 0 {
			return item, nil, fmt.Errorf("压缩包包含非法路径或符号链接：%s", entry.name)
		}
		if path.Base(entry.name) == "SKILL.md" {
			candidateDepth := strings.Count(entry.name, "/")
			if candidateDepth < depth {
				root = strings.TrimSuffix(entry.name, "SKILL.md")
				depth = candidateDepth
				found = true
			} else if candidateDepth == depth {
				return item, nil, fmt.Errorf("压缩包包含多个 Skill")
			}
		}
	}
	if !found {
		return item, nil, fmt.Errorf("压缩包缺少 SKILL.md")
	}
	files := []SkillFile{}
	seen := map[string]bool{}
	var total int64
	var document []byte
	for _, entry := range entries {
		if !strings.HasPrefix(entry.name, root) {
			continue
		}
		name := strings.TrimPrefix(entry.name, root)
		if !ValidSkillFilePath(name) || seen[name] {
			return item, nil, ErrInvalidProfile
		}
		seen[name] = true
		if entry.f.UncompressedSize64 > 20<<20 {
			return item, nil, fmt.Errorf("单文件超过 20 MiB：%s", name)
		}
		file, err := entry.f.Open()
		if err != nil {
			return item, nil, err
		}
		body, err := io.ReadAll(io.LimitReader(file, (20<<20)+1))
		file.Close()
		if err != nil || len(body) > 20<<20 {
			return item, nil, ErrInvalidProfile
		}
		total += int64(len(body))
		if total > 100<<20 {
			return item, nil, fmt.Errorf("解压内容超过 100 MiB")
		}
		mode := 0644
		if entry.f.Mode().Perm()&0111 != 0 {
			mode = 0755
		}
		files = append(files, SkillFile{Path: name, Content: body, Mode: mode})
		if name == "SKILL.md" {
			document = body
		}
	}
	name := ""
	if root != "" {
		name = path.Base(strings.TrimSuffix(root, "/"))
	}
	// The package name is authoritative in SKILL.md, including flat zip layouts.
	if declared := SkillDocumentName(document); declared != "" {
		name = declared
	}
	if !validSkillPackageName(name) {
		return item, nil, fmt.Errorf("SKILL.md 缺少合法 name")
	}
	item, err = ParseSkillDocument(name, document)
	if err != nil {
		return item, nil, err
	}
	return item, files, nil
}

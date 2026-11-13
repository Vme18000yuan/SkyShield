package repository

import (
	"context"
	"encoding/json"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func (s *SkillStore) SeedSkillPackageFiles(ctx context.Context) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAgentSettingsTenant(tx); err != nil {
			return err
		}
		var records []struct {
			ID, Name, Description, Instructions, License, Compatibility, BuiltinKey string
			MCPs                                                                    string
		}
		if err := tx.Raw(`SELECT id::text,name,description,instructions,license,compatibility,COALESCE(builtin_key,'') builtin_key,mcps::text FROM ai_skill WHERE tenant_id=?`, assets.DefaultTenantID).Scan(&records).Error; err != nil {
			return err
		}
		for _, record := range records {
			flag := "skill_files_v1/" + record.ID
			var done bool
			if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_runtime_seed WHERE tenant_id=? AND seed_key=?)`, assets.DefaultTenantID, flag).Scan(&done).Error; err != nil {
				return err
			}
			if done {
				continue
			}
			files := []settings.SkillFile{}
			if record.BuiltinKey != "" && s.builtinRoot != "" {
				root, err := os.OpenRoot(s.builtinRoot)
				if err != nil {
					return err
				}
				err = fs.WalkDir(root.FS(), record.BuiltinKey, func(path string, entry fs.DirEntry, walkErr error) error {
					if walkErr != nil {
						return walkErr
					}
					if entry.Type()&os.ModeSymlink != 0 {
						return settings.ErrInvalidProfile
					}
					relative := strings.TrimPrefix(strings.TrimPrefix(path, record.BuiltinKey), "/")
					if relative == "" {
						return nil
					}
					if !settings.ValidSkillFilePath(relative) {
						return settings.ErrInvalidProfile
					}
					info, err := entry.Info()
					if err != nil {
						return err
					}
					file := settings.SkillFile{Path: filepath.ToSlash(relative), Directory: entry.IsDir(), Mode: int(info.Mode().Perm())}
					if !file.Directory {
						if info.Size() > 20<<20 {
							return settings.ErrInvalidProfile
						}
						file.Content, err = root.ReadFile(path)
						if err != nil {
							return err
						}
					}
					files = append(files, file)
					return nil
				})
				root.Close()
				if err != nil {
					return err
				}
			} else {
				var mcps []string
				_ = json.Unmarshal([]byte(record.MCPs), &mcps)
				files = append(files, settings.SkillFile{Path: "SKILL.md", Mode: 0644, Content: settings.RenderSkillDocument(settings.SkillPackage{Name: record.Name, Description: record.Description, Instructions: record.Instructions, License: record.License, Compatibility: record.Compatibility, MCPs: mcps})})
			}
			for _, file := range files {
				if err := writeSkillFile(tx, record.ID, file); err != nil {
					return err
				}
				if file.Path == "SKILL.md" {
					item, err := settings.ParseSkillDocument(record.Name, file.Content)
					if err != nil {
						return err
					}
					if err = syncSkillDocument(tx, record.ID, item); err != nil {
						return err
					}
				}
			}
			if err := tx.Exec(`INSERT INTO ai_runtime_seed(tenant_id,seed_key) VALUES(?,?) ON CONFLICT DO NOTHING`, assets.DefaultTenantID, flag).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

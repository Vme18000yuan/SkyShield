package infrastructure

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/Autumn-27/norma/skill"
	"github.com/google/uuid"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"os"
	"path/filepath"
)

func NewSkillMaterializer(root string, store settings.SkillRuntimeFileStore) settings.SkillMaterializer {
	return func(ctx context.Context, user int, key string, items []skill.Skill) ([]skill.Skill, error) {
		workspace := filepath.Join(root, assets.DefaultTenantID, fmt.Sprintf("user-%d", user), "work")
		if err := os.MkdirAll(workspace, 0700); err != nil {
			return nil, err
		}
		owned, err := os.OpenRoot(workspace)
		if err != nil {
			return nil, err
		}
		defer owned.Close()
		for i := range items {
			files, err := store.RuntimeSkillFiles(ctx, user, key, items[i].Name)
			if err != nil {
				return nil, err
			}
			hash := sha256.New()
			for _, file := range files {
				fmt.Fprintf(hash, "%s:%d:%t\x00", file.Path, file.Mode, file.Directory)
				_, _ = hash.Write(file.Content)
			}
			directory := filepath.Join(".skills", fmt.Sprintf("%x", hash.Sum(nil)))
			if err = owned.MkdirAll(directory, 0700); err != nil {
				return nil, err
			}
			for _, file := range files {
				if !settings.ValidSkillFilePath(file.Path) {
					return nil, settings.ErrInvalidProfile
				}
				destination := filepath.Join(directory, file.Path)
				if file.Directory {
					if err = owned.MkdirAll(destination, 0700); err != nil {
						return nil, err
					}
					continue
				}
				if err = owned.MkdirAll(filepath.Dir(destination), 0700); err != nil {
					return nil, err
				}
				temp := destination + "." + uuid.NewString() + ".tmp"
				if err = owned.WriteFile(temp, file.Content, os.FileMode(file.Mode)&0777); err != nil {
					return nil, err
				}
				if err = owned.Rename(temp, destination); err != nil {
					_ = owned.Remove(temp)
					return nil, err
				}
			}
			items[i].Dir = filepath.Join(workspace, directory)
		}
		return items, nil
	}
}

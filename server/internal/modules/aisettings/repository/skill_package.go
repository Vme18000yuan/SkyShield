package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/lib/pq"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"github.com/yyhuni/lunafox/server/internal/pkg/dbtx"
	"gorm.io/gorm"
)

func skillPackageID(tx *gorm.DB, name string, lock bool) (string, error) {
	var id string
	query := `SELECT id::text FROM ai_skill WHERE tenant_id=? AND name=?`
	if lock {
		query += ` FOR UPDATE`
	}
	err := tx.Raw(query, assets.DefaultTenantID, name).Row().Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = settings.ErrProfileNotFound
	}
	return id, err
}
func readSkillPackageFiles(tx *gorm.DB, id string) ([]settings.SkillFile, error) {
	files := []settings.SkillFile{}
	rows, err := tx.Raw(`SELECT path,content,is_dir,mode FROM ai_skill_file WHERE tenant_id=? AND skill_id=? ORDER BY path`, assets.DefaultTenantID, id).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var file settings.SkillFile
		if err = rows.Scan(&file.Path, &file.Content, &file.Directory, &file.Mode); err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, rows.Err()
}
func writeSkillFile(tx *gorm.DB, id string, file settings.SkillFile) error {
	if file.Content == nil {
		file.Content = []byte{}
	}
	var conflict bool
	if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_skill_file WHERE skill_id=? AND (path=? AND is_dir<>? OR left(?,length(path)+1)=path||'/' AND NOT is_dir OR left(path,length(?)+1)=?||'/' AND NOT ?))`, id, file.Path, file.Directory, file.Path, file.Path, file.Path, file.Directory).Scan(&conflict).Error; err != nil {
		return err
	}
	if conflict {
		return settings.ErrInvalidProfile
	}
	return tx.Exec(`INSERT INTO ai_skill_file(tenant_id,skill_id,path,content,is_dir,mode) VALUES(?,?,?,?,?,?) ON CONFLICT(skill_id,path) DO UPDATE SET content=EXCLUDED.content,is_dir=EXCLUDED.is_dir,mode=EXCLUDED.mode,updated_at=CURRENT_TIMESTAMP`, assets.DefaultTenantID, id, file.Path, file.Content, file.Directory, file.Mode).Error
}
func syncSkillDocument(tx *gorm.DB, id string, item settings.SkillPackage) error {
	mcps, _ := json.Marshal(item.MCPs)
	return tx.Exec(`UPDATE ai_skill SET description=?,instructions=?,license=?,compatibility=?,mcps=?::jsonb,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=? AND id=?`, item.Description, item.Instructions, item.License, item.Compatibility, string(mcps), assets.DefaultTenantID, id).Error
}
func (s *SkillStore) ListSkillPackages(ctx context.Context, user int) ([]settings.SkillPackage, error) {
	items := []settings.SkillPackage{}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		rows, err := tx.Raw(`SELECT s.id::text,s.name,s.description,s.license,s.compatibility,s.mcps,s.enabled,COALESCE(u.calls,0),COALESCE(u.tasks,0),COALESCE(u.agents,'{}'::text[]),u.last_used FROM ai_skill s LEFT JOIN LATERAL(SELECT COUNT(*) FILTER(WHERE status='loaded') calls,COUNT(DISTINCT task_id) FILTER(WHERE status='loaded' AND task_id>0) tasks,array_agg(DISTINCT agent_key) FILTER(WHERE status='loaded') agents,MAX(ts) FILTER(WHERE status='loaded') last_used FROM ai_skill_usage WHERE tenant_id=s.tenant_id AND skill=s.name) u ON TRUE WHERE s.tenant_id=? AND EXISTS(SELECT 1 FROM ai_skill_file WHERE skill_id=s.id AND path='SKILL.md' AND NOT is_dir) ORDER BY s.name`, assets.DefaultTenantID).Rows()
		if err != nil {
			return err
		}
		for rows.Next() {
			var item settings.SkillPackage
			var mcps []byte
			var agents pq.StringArray
			if err = rows.Scan(&item.ID, &item.Name, &item.Description, &item.License, &item.Compatibility, &mcps, &item.Enabled, &item.Calls, &item.Tasks, &agents, &item.LastUsed); err != nil {
				rows.Close()
				return err
			}
			if err = json.Unmarshal(mcps, &item.MCPs); err != nil {
				rows.Close()
				return err
			}
			item.UsageAgents = []string(agents)
			item.Files = []string{}
			items = append(items, item)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for i := range items {
			var entries []struct {
				Path  string
				IsDir bool
			}
			if err = tx.Raw(`SELECT path,is_dir FROM ai_skill_file WHERE skill_id=? ORDER BY path`, items[i].ID).Scan(&entries).Error; err != nil {
				return err
			}
			for _, entry := range entries {
				path := entry.Path
				if entry.IsDir {
					path += "/"
				}
				items[i].Files = append(items[i].Files, path)
			}
		}
		return nil
	})
	return items, err
}
func (s *SkillStore) SkillPackageFiles(ctx context.Context, user int, name string) ([]settings.SkillFile, error) {
	var files []settings.SkillFile
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		id, err := skillPackageID(tx, name, false)
		if err != nil {
			return err
		}
		files, err = readSkillPackageFiles(tx, id)
		return err
	})
	return files, err
}
func (s *SkillStore) SaveSkillPackage(ctx context.Context, user int, item settings.SkillPackage, files []settings.SkillFile, create, overwrite bool) error {
	return dbtx.WithinTransaction(ctx, s.db, func(_ context.Context, tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		if err := lockAgentSettingsTenant(tx); err != nil {
			return err
		}
		id, err := skillPackageID(tx, item.Name, true)
		if err != nil && !errors.Is(err, settings.ErrProfileNotFound) {
			return err
		}
		if err == nil && !overwrite && create {
			return settings.ErrProfileConflict
		}
		if err != nil {
			id = uuid.NewString()
			if err = tx.Exec(`INSERT INTO ai_skill(id,tenant_id,name,description,instructions,enabled,created_by) VALUES(?,?,?,?,?,TRUE,?)`, id, assets.DefaultTenantID, item.Name, item.Description, item.Instructions, user).Error; err != nil {
				return err
			}
		}
		if overwrite {
			if err = tx.Exec(`DELETE FROM ai_skill_file WHERE tenant_id=? AND skill_id=?`, assets.DefaultTenantID, id).Error; err != nil {
				return err
			}
		}
		for _, file := range files {
			if err = writeSkillFile(tx, id, file); err != nil {
				return err
			}
		}
		return syncSkillDocument(tx, id, item)
	})
}
func (s *SkillStore) SaveSkillPackagePath(ctx context.Context, user int, name string, file settings.SkillFile, remove bool) error {
	return dbtx.WithinTransaction(ctx, s.db, func(_ context.Context, tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		id, err := skillPackageID(tx, name, true)
		if err != nil {
			return err
		}
		if remove {
			result := tx.Exec(`DELETE FROM ai_skill_file WHERE skill_id=? AND (path=? OR left(path,length(?)+1)=?||'/')`, id, file.Path, file.Path, file.Path)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return settings.ErrProfileNotFound
			}
			return nil
		}
		if err = writeSkillFile(tx, id, file); err != nil {
			return err
		}
		if file.Path == "SKILL.md" && !file.Directory {
			item, err := settings.ParseSkillDocument(name, file.Content)
			if err != nil {
				return err
			}
			return syncSkillDocument(tx, id, item)
		}
		return nil
	})
}
func (s *SkillStore) DeleteSkillPackage(ctx context.Context, user int, name string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		result := tx.Exec(`DELETE FROM ai_skill WHERE tenant_id=? AND name=?`, assets.DefaultTenantID, name)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return settings.ErrProfileNotFound
		}
		return nil
	})
}
func (s *SkillStore) SkillPackageVisibility(ctx context.Context, user int, name string) ([]string, error) {
	out := []string{}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		id, err := skillPackageID(tx, name, false)
		if err != nil {
			return err
		}
		return tx.Raw(`SELECT agent_key FROM ai_skill_agent WHERE tenant_id=? AND skill_id=? ORDER BY agent_key`, assets.DefaultTenantID, id).Scan(&out).Error
	})
	return out, err
}
func (s *SkillStore) SetSkillPackageVisibility(ctx context.Context, user int, name, key string, on bool) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		if err := lockAgentSettingsTenant(tx); err != nil {
			return err
		}
		if err := requireAgentKey(tx, key); err != nil {
			return err
		}
		id, err := skillPackageID(tx, name, true)
		if err != nil {
			return err
		}
		if on {
			return tx.Exec(`INSERT INTO ai_skill_agent(tenant_id,skill_id,agent_key) VALUES(?,?,?) ON CONFLICT DO NOTHING`, assets.DefaultTenantID, id, key).Error
		}
		return tx.Exec(`DELETE FROM ai_skill_agent WHERE tenant_id=? AND skill_id=? AND agent_key=?`, assets.DefaultTenantID, id, key).Error
	})
}
func (s *SkillStore) SkillPackageUsage(ctx context.Context, user int, name string, limit int) ([]settings.SkillCall, error) {
	items := []settings.SkillCall{}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		return tx.Raw(`SELECT ts,agent_key,task_id,session_id,args_len FROM ai_skill_usage WHERE tenant_id=? AND skill=? AND status='loaded' ORDER BY ts DESC,id DESC LIMIT ?`, assets.DefaultTenantID, name, limit).Scan(&items).Error
	})
	return items, err
}
func (s *SkillStore) MissingSkillUsage(ctx context.Context, user, limit int) ([]settings.MissingSkill, error) {
	items := []settings.MissingSkill{}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		rows, err := tx.Raw(`SELECT skill,COUNT(*),array_agg(DISTINCT agent_key),MAX(ts) FROM ai_skill_usage WHERE tenant_id=? AND status='missing' GROUP BY skill ORDER BY MAX(ts) DESC LIMIT ?`, assets.DefaultTenantID, limit).Rows()
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item settings.MissingSkill
			var agents pq.StringArray
			if err = rows.Scan(&item.Skill, &item.Calls, &agents, &item.LastUsed); err != nil {
				return err
			}
			item.Agents = []string(agents)
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

var _ settings.SkillPackageStore = (*SkillStore)(nil)

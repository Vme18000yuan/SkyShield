package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Autumn-27/norma/skill"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lib/pq"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

type SkillStore struct {
	db          *gorm.DB
	builtinRoot string
}

var builtinSkillAgentKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func NewSkillStore(db *gorm.DB, builtinRoot ...string) *SkillStore {
	store := &SkillStore{db: db}
	if len(builtinRoot) > 0 {
		store.builtinRoot = strings.TrimSpace(builtinRoot[0])
	}
	return store
}

type builtinSkillManifest struct {
	Skills []builtinSkillEntry `json:"skills"`
}

type builtinSkillEntry struct {
	Directory   string   `json:"directory"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Agents      []string `json:"agents"`
}

func (s *SkillStore) SeedBuiltinSkills(ctx context.Context) error {
	if s == nil || s.db == nil || s.builtinRoot == "" {
		return errors.New("AI built-in skill root is required")
	}
	root, err := filepath.Abs(s.builtinRoot)
	if err != nil {
		return fmt.Errorf("resolve AI built-in skill root: %w", err)
	}
	manifestPath := filepath.Join(root, "manifest.json")
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil || !manifestInfo.Mode().IsRegular() {
		return fmt.Errorf("AI built-in skill manifest is unavailable")
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read AI built-in skill manifest: %w", err)
	}
	var manifest builtinSkillManifest
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil || len(manifest.Skills) == 0 {
		return fmt.Errorf("AI built-in skill manifest is invalid")
	}
	registry, err := skill.LoadDir(root)
	if err != nil {
		return fmt.Errorf("load AI built-in skills: %w", err)
	}
	var adminID int
	if err := s.db.WithContext(ctx).Raw(`SELECT u.id FROM auth_user u JOIN tenant_membership m ON m.user_id=u.id
WHERE m.tenant_id=? AND u.is_active=TRUE AND u.is_superuser=TRUE ORDER BY u.id LIMIT 1`, assetapp.DefaultTenantID).Row().Scan(&adminID); err != nil {
		return fmt.Errorf("find an active administrator for built-in skill records: %w", err)
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		seen := map[string]bool{}
		for _, entry := range manifest.Skills {
			entry.Directory = strings.TrimSpace(entry.Directory)
			entry.Name = strings.TrimSpace(entry.Name)
			entry.Description = strings.TrimSpace(entry.Description)
			if entry.Directory == "" || entry.Name == "" || seen[entry.Directory] {
				return fmt.Errorf("AI built-in skill manifest has an invalid or duplicate entry")
			}
			seen[entry.Directory] = true
			directory := filepath.Clean(entry.Directory)
			if filepath.IsAbs(directory) || directory == "." || directory == ".." || strings.HasPrefix(directory, ".."+string(filepath.Separator)) || strings.ContainsAny(directory, `/\\`) {
				return fmt.Errorf("AI built-in skill directory is invalid")
			}
			loaded, ok := registry.Get(entry.Name)
			if !ok || loaded.Instructions == "" {
				return fmt.Errorf("AI built-in skill %q is missing a readable SKILL.md", entry.Name)
			}
			var previouslySeeded bool
			flag := "builtin_skill_entry_v1/" + entry.Directory
			if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_runtime_seed WHERE tenant_id=? AND seed_key=?)`, assetapp.DefaultTenantID, flag).Scan(&previouslySeeded).Error; err != nil {
				return err
			}
			if previouslySeeded {
				continue
			}
			if err := tx.Exec(`INSERT INTO ai_runtime_seed(tenant_id,seed_key) VALUES(?,?) ON CONFLICT DO NOTHING`, assetapp.DefaultTenantID, flag).Error; err != nil {
				return err
			}
			var existingID string
			existingErr := tx.Raw(`SELECT id::text FROM ai_skill WHERE tenant_id=? AND builtin_key=?`, assetapp.DefaultTenantID, entry.Directory).Row().Scan(&existingID)
			if existingErr == nil {
				continue
			}
			if !errors.Is(existingErr, sql.ErrNoRows) {
				return existingErr
			}
			var nameExists bool
			if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_skill WHERE tenant_id=? AND name=?)`, assetapp.DefaultTenantID, entry.Name).Scan(&nameExists).Error; err != nil {
				return err
			}
			if nameExists {
				// A pre-existing custom skill belongs to the administrator. Avoid
				// blocking an upgrade, and retry the built-in seed if the conflict
				// is later removed; never replace the custom package.
				if entry.Directory == "info-gath" {
					if err := tx.Exec(`DELETE FROM ai_runtime_seed WHERE tenant_id=? AND seed_key=?`, assetapp.DefaultTenantID, flag).Error; err != nil {
						return err
					}
					continue
				}
				return fmt.Errorf("AI built-in skill %q conflicts with an existing user skill", entry.Name)
			}
			description := entry.Description
			if description == "" {
				description = loaded.Description
			}
			id := uuid.NewString()
			if err := tx.Exec(`INSERT INTO ai_skill(id,tenant_id,name,description,instructions,enabled,created_by,builtin_key)
VALUES(?,?,?,?,?,TRUE,?,?)`, id, assetapp.DefaultTenantID, entry.Name, description, loaded.Instructions, adminID, entry.Directory).Error; err != nil {
				return err
			}
			for _, agentKey := range entry.Agents {
				if !builtinSkillAgentKeyPattern.MatchString(agentKey) {
					return fmt.Errorf("AI built-in skill %q has an invalid Agent binding", entry.Name)
				}
				if err := tx.Exec(`INSERT INTO ai_skill_agent(tenant_id,skill_id,agent_key) VALUES(?,?,?) ON CONFLICT DO NOTHING`, assetapp.DefaultTenantID, id, agentKey).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := s.SeedSkillPackageFiles(ctx); err != nil {
		return err
	}
	if err := s.upgradeInfoGatherScripts(ctx); err != nil {
		return err
	}
	if err := s.upgradeInfoGatherAgenticScripts(ctx); err != nil {
		return err
	}
	if err := s.upgradeInfoGatherResumeScripts(ctx); err != nil {
		return err
	}
	if err := s.upgradeInfoGatherRiskContract(ctx); err != nil {
		return err
	}
	if err := s.upgradeInfoGatherEvidenceScripts(ctx); err != nil {
		return err
	}
	if err := s.upgradeInfoGatherSourceReliabilityScripts(ctx); err != nil {
		return err
	}
	return s.upgradeInfoGatherPortResumeScripts(ctx)
}

func readSkills(tx *gorm.DB, id string) ([]settingsapp.SkillRecord, error) {
	rows, err := tx.Raw(`SELECT s.id::text,s.name,s.description,s.instructions,s.enabled,s.created_at,s.updated_at,s.builtin_key,s.license,s.compatibility,s.mcps,
 COALESCE(array_agg(a.agent_key ORDER BY a.agent_key) FILTER(WHERE a.agent_key IS NOT NULL),'{}'::text[])
 FROM ai_skill s LEFT JOIN ai_skill_agent a ON a.tenant_id=s.tenant_id AND a.skill_id=s.id
 WHERE s.tenant_id=? AND (?='' OR s.id=NULLIF(?,'')::uuid) GROUP BY s.id ORDER BY s.name,s.id LIMIT 500`, assetapp.DefaultTenantID, id, id).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []settingsapp.SkillRecord{}
	for rows.Next() {
		var item settingsapp.SkillRecord
		var agents pq.StringArray
		var builtinKey sql.NullString
		var mcps []byte
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.Instructions, &item.Enabled, &item.CreatedAt, &item.UpdatedAt, &builtinKey, &item.License, &item.Compatibility, &mcps, &agents); err != nil {
			return nil, err
		}
		item.BuiltinKey = builtinKey.String
		if err := json.Unmarshal(mcps, &item.MCPs); err != nil {
			return nil, err
		}
		item.Agents = []string(agents)
		out = append(out, item)
	}
	return out, rows.Err()
}
func (s *SkillStore) ListSkills(ctx context.Context, user int) ([]settingsapp.SkillRecord, error) {
	var out []settingsapp.SkillRecord
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		var err error
		out, err = readSkills(tx, "")
		return err
	})
	return out, err
}
func (s *SkillStore) SaveSkill(ctx context.Context, user int, item settingsapp.SkillRecord, create bool) (*settingsapp.SkillRecord, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		if create {
			if err := tx.Exec(`INSERT INTO ai_skill(id,tenant_id,name,description,instructions,enabled,created_by) VALUES(?,?,?,?,?,?,?)`, item.ID, assetapp.DefaultTenantID, item.Name, item.Description, item.Instructions, item.Enabled, user).Error; err != nil {
				return err
			}
		} else {
			q := tx.Exec(`UPDATE ai_skill SET name=?,description=?,instructions=?,enabled=?,updated_at=clock_timestamp() WHERE tenant_id=? AND id=?`, item.Name, item.Description, item.Instructions, item.Enabled, assetapp.DefaultTenantID, item.ID)
			if q.Error != nil {
				return q.Error
			}
			if q.RowsAffected == 0 {
				return settingsapp.ErrProfileNotFound
			}
		}
		if err := tx.Exec(`DELETE FROM ai_skill_agent WHERE tenant_id=? AND skill_id=?`, assetapp.DefaultTenantID, item.ID).Error; err != nil {
			return err
		}
		for _, agent := range item.Agents {
			if err := tx.Exec(`INSERT INTO ai_skill_agent(tenant_id,skill_id,agent_key) VALUES(?,?,?)`, assetapp.DefaultTenantID, item.ID, agent).Error; err != nil {
				return err
			}
		}
		values, err := readSkills(tx, item.ID)
		if err != nil {
			return err
		}
		item = values[0]
		return writeSkillFile(tx, item.ID, settingsapp.SkillFile{Path: "SKILL.md", Content: settingsapp.RenderSkillDocument(settingsapp.SkillPackage{Name: item.Name, Description: item.Description, Instructions: item.Instructions, License: item.License, Compatibility: item.Compatibility, MCPs: item.MCPs}), Mode: 0644})
	})
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return nil, settingsapp.ErrProfileConflict
		}
		return nil, err
	}
	return &item, nil
}
func (s *SkillStore) DeleteSkill(ctx context.Context, user int, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		q := tx.Exec(`DELETE FROM ai_skill WHERE tenant_id=? AND id=?`, assetapp.DefaultTenantID, id)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected == 0 {
			return settingsapp.ErrProfileNotFound
		}
		return nil
	})
}
func (s *SkillStore) ResolveSkills(ctx context.Context, user int, key string) ([]skill.Skill, error) {
	if err := requireAgentCatalogMember(ctx, s.db, user); err != nil {
		return nil, err
	}
	rows, err := s.db.WithContext(ctx).Raw(`SELECT s.name,s.description,s.instructions,s.license,s.compatibility,s.mcps FROM ai_skill s JOIN ai_skill_agent a ON a.tenant_id=s.tenant_id AND a.skill_id=s.id WHERE s.tenant_id=? AND a.agent_key=? AND s.enabled=TRUE AND EXISTS(SELECT 1 FROM ai_skill_file WHERE skill_id=s.id AND path='SKILL.md' AND NOT is_dir) ORDER BY s.name,s.id LIMIT 500`, assetapp.DefaultTenantID, key).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []skill.Skill{}
	for rows.Next() {
		var item skill.Skill
		var mcps []byte
		if err = rows.Scan(&item.Name, &item.Description, &item.Instructions, &item.License, &item.Compatibility, &mcps); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(mcps, &item.MCPs); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

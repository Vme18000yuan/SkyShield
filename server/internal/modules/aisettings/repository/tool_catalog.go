package repository

import (
	"context"
	"encoding/json"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"github.com/yyhuni/lunafox/server/internal/pkg/dbtx"
	"gorm.io/gorm"
	"strings"
)

type ToolCatalogStore struct{ db *gorm.DB }

func NewToolCatalogStore(db *gorm.DB) *ToolCatalogStore { return &ToolCatalogStore{db: db} }
func (s *ToolCatalogStore) ListToolCatalog(ctx context.Context, user int, admin bool) ([]settings.ToolRecord, error) {
	items := []settings.ToolRecord{}
	err := dbtx.WithinTransaction(ctx, s.db, func(_ context.Context, tx *gorm.DB) error {
		if admin {
			if err := requireAdmin(tx, user); err != nil {
				return err
			}
		} else if err := requireAgentCatalogMember(ctx, tx, user); err != nil {
			return err
		}
		rows, err := tx.Raw(`SELECT c.key,c.system,c.description,c.schema,c.agents,c.enabled,c.kind,c.exec,c.deferred,(SELECT COUNT(*) FROM ai_conversation_activity a WHERE a.tenant_id=c.tenant_id AND a.kind='tool_use' AND a.tool=c.key)+(SELECT COUNT(*) FROM tianqiong_ai.activity a JOIN ai_task task ON task.exploration_id=a.exploration_id WHERE task.tenant_id=c.tenant_id AND a.kind='tool_use' AND a.tool=c.key) FROM ai_tool_catalog c WHERE c.tenant_id=? ORDER BY c.key`, assets.DefaultTenantID).Rows()
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item settings.ToolRecord
			var schema, agents, exec []byte
			if err = rows.Scan(&item.Key, &item.System, &item.Description, &schema, &agents, &item.Enabled, &item.Kind, &exec, &item.Deferred, &item.Calls); err != nil {
				return err
			}
			item.Schema = schema
			item.Exec = exec
			if err = json.Unmarshal(agents, &item.Agents); err != nil {
				return err
			}
			if item.Agents == nil {
				item.Agents = []string{}
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}
func (s *ToolCatalogStore) SaveToolCatalog(ctx context.Context, user int, item settings.ToolRecord, create bool) error {
	return dbtx.WithinTransaction(ctx, s.db, func(_ context.Context, tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		if err := lockAgentSettingsTenant(tx); err != nil {
			return err
		}
		agents, _ := json.Marshal(item.Agents)
		if len(item.Exec) == 0 {
			item.Exec = json.RawMessage("{}")
		}
		if create {
			err := tx.Exec(`INSERT INTO ai_tool_catalog(tenant_id,key,system,description,schema,agents,enabled,kind,exec,deferred) VALUES(?,?,FALSE,?,?::jsonb,?::jsonb,?,?,?::jsonb,?)`, assets.DefaultTenantID, item.Key, item.Description, string(item.Schema), string(agents), item.Enabled, item.Kind, string(item.Exec), item.Deferred).Error
			if err != nil && strings.Contains(err.Error(), "23505") {
				return settings.ErrToolCatalogInvalid
			}
			return err
		}
		result := tx.Exec(`UPDATE ai_tool_catalog SET description=?,schema=?::jsonb,agents=?::jsonb,enabled=?,kind=?,exec=?::jsonb,deferred=?,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=? AND key=? AND system=?`, item.Description, string(item.Schema), string(agents), item.Enabled, item.Kind, string(item.Exec), item.Deferred, assets.DefaultTenantID, item.Key, item.System)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return settings.ErrToolCatalogNotFound
		}
		return nil
	})
}
func (s *ToolCatalogStore) DeleteToolCatalog(ctx context.Context, user int, key string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		result := tx.Exec(`DELETE FROM ai_tool_catalog WHERE tenant_id=? AND key=? AND system=FALSE`, assets.DefaultTenantID, key)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return settings.ErrToolCatalogNotFound
		}
		return nil
	})
}
func (s *ToolCatalogStore) SeedToolCatalog(ctx context.Context, items []settings.ToolRecord) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAgentSettingsTenant(tx); err != nil {
			return err
		}
		for _, item := range items {
			agents, _ := json.Marshal(item.Agents)
			if agents == nil {
				agents = []byte("[]")
			}
			if len(item.Exec) == 0 {
				item.Exec = json.RawMessage("{}")
			}
			if err := tx.Exec(`INSERT INTO ai_tool_catalog(tenant_id,key,system,description,schema,agents,enabled,kind,exec,deferred) VALUES(?,?,?,?,?::jsonb,?::jsonb,?,?,?::jsonb,?) ON CONFLICT DO NOTHING`, assets.DefaultTenantID, item.Key, item.System, item.Description, string(item.Schema), string(agents), item.Enabled, item.Kind, string(item.Exec), item.Deferred).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

var _ settings.ToolCatalogStore = (*ToolCatalogStore)(nil)

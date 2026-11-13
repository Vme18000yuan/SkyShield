package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lib/pq"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"github.com/yyhuni/lunafox/server/internal/pkg/dbtx"
	"gorm.io/gorm"
)

type MCPStore struct{ db *gorm.DB }

func NewMCPStore(db *gorm.DB) *MCPStore { return &MCPStore{db: db} }
func readMCP(tx *gorm.DB, id string) ([]settings.MCPServer, error) {
	rows, err := tx.Raw(`SELECT s.id::text,s.name,s.endpoint,s.enabled,s.insecure,s.encrypted_headers,s.created_at,s.updated_at,s.transport,s.command,s.args,s.encrypted_env,s.tools_cache,COALESCE(array_agg(a.agent_key ORDER BY a.agent_key) FILTER(WHERE a.agent_key IS NOT NULL),'{}'::text[]) FROM ai_mcp_server s LEFT JOIN ai_mcp_server_agent a ON a.tenant_id=s.tenant_id AND a.server_id=s.id WHERE s.tenant_id=? AND (?='' OR s.id=NULLIF(?,'')::uuid) GROUP BY s.id ORDER BY s.name,s.id LIMIT 200`, assets.DefaultTenantID, id, id).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []settings.MCPServer{}
	for rows.Next() {
		var item settings.MCPServer
		var agents pq.StringArray
		var args, tools []byte
		if err = rows.Scan(&item.ID, &item.Name, &item.Endpoint, &item.Enabled, &item.Insecure, &item.EncryptedHeaders, &item.CreatedAt, &item.UpdatedAt, &item.Transport, &item.Command, &args, &item.EncryptedEnv, &tools, &agents); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(args, &item.Args); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(tools, &item.Tools); err != nil {
			return nil, err
		}
		item.HasHeaders = len(item.EncryptedHeaders) > 0
		item.HasEnv = len(item.EncryptedEnv) > 0
		item.Agents = []string(agents)
		out = append(out, item)
	}
	return out, rows.Err()
}
func (s *MCPStore) ListMCP(ctx context.Context, user int) ([]settings.MCPServer, error) {
	var out []settings.MCPServer
	err := dbtx.WithinTransaction(ctx, s.db, func(_ context.Context, tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		var err error
		out, err = readMCP(tx, "")
		for i := range out {
			out[i].EncryptedHeaders = nil
			out[i].EncryptedEnv = nil
		}
		return err
	})
	return out, err
}
func (s *MCPStore) GetMCPForAdmin(ctx context.Context, user int, id string) (*settings.MCPServer, error) {
	var item *settings.MCPServer
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		items, err := readMCP(tx, id)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return settings.ErrProfileNotFound
		}
		item = &items[0]
		return nil
	})
	return item, err
}
func (s *MCPStore) SaveMCP(ctx context.Context, user int, item settings.MCPServer, create, clearHeaders bool) (*settings.MCPServer, error) {
	err := dbtx.WithinTransaction(ctx, s.db, func(_ context.Context, tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		if err := lockAgentSettingsTenant(tx); err != nil {
			return err
		}
		if !create && item.PreserveAgents {
			current, err := readMCP(tx, item.ID)
			if err != nil {
				return err
			}
			if len(current) == 0 {
				return settings.ErrProfileNotFound
			}
			item.Agents = current[0].Agents
		}
		for _, key := range item.Agents {
			if err := requireAgentKey(tx, key); err != nil {
				return err
			}
		}
		if item.Args == nil {
			item.Args = []string{}
		}
		args, _ := json.Marshal(item.Args)
		if create {
			if err := tx.Exec(`INSERT INTO ai_mcp_server(id,tenant_id,name,endpoint,enabled,insecure,encrypted_headers,created_by,transport,command,args,encrypted_env) VALUES(?,?,?,?,?,?,?,?,?,?,?::jsonb,?)`, item.ID, assets.DefaultTenantID, item.Name, item.Endpoint, item.Enabled, item.Insecure, item.EncryptedHeaders, user, item.Transport, item.Command, string(args), item.EncryptedEnv).Error; err != nil {
				return err
			}
		} else {
			result := tx.Exec(`UPDATE ai_mcp_server SET name=?,endpoint=?,enabled=?,insecure=?,encrypted_headers=CASE WHEN ? THEN NULL ELSE COALESCE(?,encrypted_headers) END,encrypted_env=CASE WHEN ? THEN ? ELSE encrypted_env END,transport=?,command=?,args=?::jsonb,tools_cache=CASE WHEN transport<>? OR command<>? OR args<>?::jsonb OR endpoint<>? OR insecure<>? OR ? THEN '[]'::jsonb ELSE tools_cache END,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=? AND id=?`, item.Name, item.Endpoint, item.Enabled, item.Insecure, clearHeaders, item.EncryptedHeaders, item.ReplaceEnv, item.EncryptedEnv, item.Transport, item.Command, string(args), item.Transport, item.Command, string(args), item.Endpoint, item.Insecure, item.ReplaceEnv || clearHeaders || len(item.EncryptedHeaders) > 0, assets.DefaultTenantID, item.ID)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return settings.ErrProfileNotFound
			}
		}
		if err := tx.Exec(`DELETE FROM ai_mcp_server_agent WHERE tenant_id=? AND server_id=?`, assets.DefaultTenantID, item.ID).Error; err != nil {
			return err
		}
		for _, key := range item.Agents {
			if err := tx.Exec(`INSERT INTO ai_mcp_server_agent(tenant_id,server_id,agent_key) VALUES(?,?,?)`, assets.DefaultTenantID, item.ID, key).Error; err != nil {
				return err
			}
		}
		items, err := readMCP(tx, item.ID)
		if err != nil {
			return err
		}
		item = items[0]
		item.EncryptedHeaders = nil
		item.EncryptedEnv = nil
		return nil
	})
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return nil, settings.ErrProfileConflict
		}
		return nil, err
	}
	return &item, nil
}
func (s *MCPStore) DeleteMCP(ctx context.Context, user int, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		result := tx.Exec(`DELETE FROM ai_mcp_server WHERE tenant_id=? AND id=?`, assets.DefaultTenantID, id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return settings.ErrProfileNotFound
		}
		return nil
	})
}
func (s *MCPStore) ResolveMCP(ctx context.Context, user int, key string) ([]settings.MCPServer, error) {
	if err := requireAgentCatalogMember(ctx, s.db, user); err != nil {
		return nil, err
	}
	rows, err := readMCP(s.db.WithContext(ctx), "")
	if err != nil {
		return nil, err
	}
	var gated []string
	if err = s.db.WithContext(ctx).Raw(`SELECT DISTINCT jsonb_array_elements_text(CASE WHEN jsonb_typeof(s.mcps)='array' THEN s.mcps ELSE '[]'::jsonb END) FROM ai_skill s JOIN ai_skill_agent a ON a.skill_id=s.id AND a.tenant_id=s.tenant_id WHERE s.tenant_id=? AND a.agent_key=? AND s.enabled`, assets.DefaultTenantID, key).Scan(&gated).Error; err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, name := range gated {
		names[name] = true
	}
	out := []settings.MCPServer{}
	for _, item := range rows {
		if !item.Enabled {
			continue
		}
		direct := false
		for _, agent := range item.Agents {
			if agent == key {
				direct = true
			}
		}
		if direct || names[item.Name] {
			item.SkillGated = !direct
			out = append(out, item)
		}
	}
	return out, nil
}
func (s *MCPStore) CacheMCPTools(ctx context.Context, user int, id string, tools []settings.MCPTool) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		raw, _ := json.Marshal(tools)
		result := tx.Exec(`UPDATE ai_mcp_server SET tools_cache=?::jsonb WHERE tenant_id=? AND id=?`, string(raw), assets.DefaultTenantID, id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return settings.ErrProfileNotFound
		}
		return nil
	})
}
func (s *MCPStore) MCPVisibility(ctx context.Context, user int, id string) ([]string, error) {
	item, err := s.GetMCPForAdmin(ctx, user, id)
	if err != nil {
		return nil, err
	}
	return item.Agents, nil
}
func (s *MCPStore) SetMCPVisible(ctx context.Context, user int, id, key string, on bool) error {
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
		items, err := readMCP(tx, id)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return settings.ErrProfileNotFound
		}
		if on {
			return tx.Exec(`INSERT INTO ai_mcp_server_agent(tenant_id,server_id,agent_key) VALUES(?,?,?) ON CONFLICT DO NOTHING`, assets.DefaultTenantID, id, key).Error
		}
		return tx.Exec(`DELETE FROM ai_mcp_server_agent WHERE tenant_id=? AND server_id=? AND agent_key=?`, assets.DefaultTenantID, id, key).Error
	})
}
func (s *MCPStore) SeedMCPDefaults(ctx context.Context) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAgentSettingsTenant(tx); err != nil {
			return err
		}
		var admin int
		err := tx.Raw(`SELECT u.id FROM auth_user u JOIN tenant_membership m ON m.user_id=u.id WHERE m.tenant_id=? AND m.role='admin' AND u.is_active AND u.is_superuser ORDER BY u.id LIMIT 1`, assets.DefaultTenantID).Row().Scan(&admin)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, item := range []struct{ name, transport, command, args string }{{"browser", "stdio", "npx", `["@playwright/mcp","--headless"]`}, {"ScopeSentry", "http", "", "[]"}} {
			flag := "mcp_default_v1/" + item.name
			var done bool
			if err = tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_runtime_seed WHERE tenant_id=? AND seed_key=?)`, assets.DefaultTenantID, flag).Scan(&done).Error; err != nil {
				return err
			}
			if done {
				continue
			}
			if err = tx.Exec(`INSERT INTO ai_mcp_server(id,tenant_id,name,endpoint,enabled,created_by,transport,command,args) VALUES(?,?,?,'',FALSE,?,?,?,?::jsonb) ON CONFLICT(tenant_id,name) DO NOTHING`, uuid.NewString(), assets.DefaultTenantID, item.name, admin, item.transport, item.command, item.args).Error; err != nil {
				return err
			}
			if err = tx.Exec(`INSERT INTO ai_runtime_seed(tenant_id,seed_key) VALUES(?,?) ON CONFLICT DO NOTHING`, assets.DefaultTenantID, flag).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

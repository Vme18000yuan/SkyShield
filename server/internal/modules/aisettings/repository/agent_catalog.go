package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	"github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository/persistence"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

type AgentCatalogStore struct{ db *gorm.DB }

func NewAgentCatalogStore(db *gorm.DB) *AgentCatalogStore {
	if db == nil {
		panic("AI agent catalog database is required")
	}
	return &AgentCatalogStore{db: db}
}

func (store *AgentCatalogStore) ListCustomAgents(ctx context.Context, userID int) ([]airuntime.ChatAgentDefinition, error) {
	if err := requireAgentCatalogMember(ctx, store.db, userID); err != nil {
		return nil, err
	}
	var records []persistence.CustomAgent
	if err := store.db.WithContext(ctx).Where("tenant_id = ?", assetapp.DefaultTenantID).Order("created_at, agent_key").Find(&records).Error; err != nil {
		return nil, err
	}
	items := make([]airuntime.ChatAgentDefinition, 0, len(records))
	for _, record := range records {
		items = append(items, customAgentToDefinition(record))
	}
	return items, nil
}

func (store *AgentCatalogStore) GetCustomAgent(ctx context.Context, userID int, key string) (*airuntime.ChatAgentDefinition, error) {
	if err := requireAgentCatalogMember(ctx, store.db, userID); err != nil {
		return nil, err
	}
	var record persistence.CustomAgent
	err := store.db.WithContext(ctx).Where("tenant_id = ? AND agent_key = ?", assetapp.DefaultTenantID, key).Take(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	item := customAgentToDefinition(record)
	return &item, nil
}

func (store *AgentCatalogStore) CreateCustomAgent(ctx context.Context, userID int, item airuntime.ChatAgentDefinition) (*airuntime.ChatAgentDefinition, error) {
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, userID); err != nil {
			return err
		}
		if err := lockAgentSettingsTenant(tx); err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO ai_custom_agent(tenant_id,agent_key,display_name,description,system_prompt,enabled,max_turns,run_seconds,created_by,updated_by)
VALUES(?,?,?,?,?,?,?,?,?,?)`, assetapp.DefaultTenantID, item.Key, item.Name, item.Description, item.SystemPrompt, item.Enabled, item.MaxTurns, item.RunSeconds, userID, userID).Error; err != nil {
			return err
		}
		return appendAgentPromptVersion(tx, userID, item.Key, item.SystemPrompt)
	})
	if err != nil {
		return nil, mapAgentCatalogConflict(err)
	}
	return store.GetCustomAgent(ctx, userID, item.Key)
}

func (store *AgentCatalogStore) UpdateCustomAgent(ctx context.Context, userID int, item airuntime.ChatAgentDefinition) (*airuntime.ChatAgentDefinition, error) {
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, userID); err != nil {
			return err
		}
		if err := lockAgentSettingsTenant(tx); err != nil {
			return err
		}
		var oldPrompt string
		if err := tx.Raw(`SELECT system_prompt FROM ai_custom_agent WHERE tenant_id=? AND agent_key=? FOR UPDATE`, assetapp.DefaultTenantID, item.Key).Row().Scan(&oldPrompt); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return settingsapp.ErrAgentCatalogNotFound
			}
			return err
		}
		if err := ensureAgentPromptInitialVersion(tx, userID, item.Key, oldPrompt); err != nil {
			return err
		}
		result := tx.Exec(`UPDATE ai_custom_agent SET display_name=?,description=?,system_prompt=?,enabled=?,max_turns=?,run_seconds=?,updated_by=?,updated_at=clock_timestamp()
WHERE tenant_id=? AND agent_key=?`, item.Name, item.Description, item.SystemPrompt, item.Enabled, item.MaxTurns, item.RunSeconds, userID, assetapp.DefaultTenantID, item.Key)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return settingsapp.ErrAgentCatalogNotFound
		}
		if oldPrompt != item.SystemPrompt {
			if err := appendAgentPromptVersion(tx, userID, item.Key, item.SystemPrompt); err != nil {
				return err
			}
		}
		if err := updateAgentLLMBinding(tx, userID, item); err != nil {
			return err
		}
		return updateAgentWrapup(tx, userID, item)
	})
	if err != nil {
		return nil, mapAgentCatalogConflict(err)
	}
	return store.GetCustomAgent(ctx, userID, item.Key)
}

func (store *AgentCatalogStore) DeleteCustomAgent(ctx context.Context, userID int, key string) error {
	return store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, userID); err != nil {
			return err
		}
		if err := lockAgentSettingsTenant(tx); err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE ai_run r SET status='stopped',finished_at=CURRENT_TIMESTAMP FROM ai_conversation c WHERE r.conversation_id=c.id AND c.tenant_id=? AND c.agent_key=? AND r.status='running'`, assetapp.DefaultTenantID, key).Error; err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE ai_agent_trigger_job SET status='cancelled',updated_at=CURRENT_TIMESTAMP WHERE tenant_id=? AND agent_key=? AND status IN ('queued','starting','running')`, assetapp.DefaultTenantID, key).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DELETE FROM ai_agent_trigger WHERE tenant_id=? AND agent_key=?`, assetapp.DefaultTenantID, key).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DELETE FROM ai_agent_option WHERE tenant_id=? AND agent_key=?`, assetapp.DefaultTenantID, key).Error; err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE ai_tool_catalog SET agents=agents-? WHERE tenant_id=? AND jsonb_exists(agents,?)`, key, assetapp.DefaultTenantID, key).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DELETE FROM ai_skill_agent WHERE tenant_id=? AND agent_key=?`, assetapp.DefaultTenantID, key).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DELETE FROM ai_mcp_server_agent WHERE tenant_id=? AND agent_key=?`, assetapp.DefaultTenantID, key).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DELETE FROM ai_agent_llm_binding WHERE tenant_id=? AND agent_key=?`, assetapp.DefaultTenantID, key).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DELETE FROM ai_agent_wrapup_config WHERE tenant_id=? AND agent_key=?`, assetapp.DefaultTenantID, key).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DELETE FROM ai_agent_prompt_version WHERE tenant_id=? AND agent_key=?`, assetapp.DefaultTenantID, key).Error; err != nil {
			return err
		}
		result := tx.Exec(`DELETE FROM ai_custom_agent WHERE tenant_id=? AND agent_key=?`, assetapp.DefaultTenantID, key)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return settingsapp.ErrAgentCatalogNotFound
		}
		return nil
	})
}

func customAgentToDefinition(record persistence.CustomAgent) airuntime.ChatAgentDefinition {
	return airuntime.ChatAgentDefinition{
		Key: record.AgentKey, Name: record.DisplayName, Description: record.Description,
		Role: "assistant", SettingsKey: "assistant", SystemPrompt: record.SystemPrompt, Enabled: record.Enabled,
		PromptTemplate: record.SystemPrompt,
		MaxTurns:       record.MaxTurns, RunSeconds: record.RunSeconds,
	}
}

func (store *AgentCatalogStore) ListAgentRuntimeSettings(ctx context.Context, userID int) (map[string]settingsapp.AgentRuntimeSettings, error) {
	items := make(map[string]settingsapp.AgentRuntimeSettings)
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCatalogMember(ctx, tx, userID); err != nil {
			return err
		}
		var configs []struct {
			AgentKey       string
			Enabled        bool
			MaxTurns       int
			RunSeconds     int
			PromptTemplate string
		}
		if err := tx.Raw(`SELECT agent_key,enabled,max_turns,run_seconds,prompt_template FROM ai_builtin_agent_config WHERE tenant_id=?`, assetapp.DefaultTenantID).Scan(&configs).Error; err != nil {
			return err
		}
		for _, c := range configs {
			items[c.AgentKey] = settingsapp.AgentRuntimeSettings{Configured: true, Enabled: c.Enabled, MaxTurns: c.MaxTurns, RunSeconds: c.RunSeconds, PromptTemplate: c.PromptTemplate}
		}
		var bindings []struct {
			AgentKey  string
			ProfileID string
		}
		if err := tx.Raw(`SELECT agent_key,profile_id::text AS profile_id FROM ai_agent_llm_binding WHERE tenant_id=?`, assetapp.DefaultTenantID).Scan(&bindings).Error; err != nil {
			return err
		}
		for _, binding := range bindings {
			item := items[binding.AgentKey]
			id := binding.ProfileID
			item.LLMProfileID = &id
			items[binding.AgentKey] = item
		}
		var wrapups []struct {
			AgentKey            string
			WrapupPrompt        string
			WrapupMaxTurns      int
			TaskTimeoutPrompt   string
			TaskTimeoutMaxTurns int
		}
		if err := tx.Raw(`SELECT agent_key,wrapup_prompt,wrapup_max_turns,task_timeout_prompt,task_timeout_max_turns FROM ai_agent_wrapup_config WHERE tenant_id=?`, assetapp.DefaultTenantID).Scan(&wrapups).Error; err != nil {
			return err
		}
		for _, wrapup := range wrapups {
			item := items[wrapup.AgentKey]
			item.WrapupPrompt, item.WrapupMaxTurns = wrapup.WrapupPrompt, wrapup.WrapupMaxTurns
			item.TaskTimeoutPrompt, item.TaskTimeoutMaxTurns = wrapup.TaskTimeoutPrompt, wrapup.TaskTimeoutMaxTurns
			items[wrapup.AgentKey] = item
		}
		var options []struct {
			AgentKey string
			Options  string
		}
		if err := tx.Raw(`SELECT agent_key,options::text FROM ai_agent_option WHERE tenant_id=?`, assetapp.DefaultTenantID).Scan(&options).Error; err != nil {
			return err
		}
		for _, row := range options {
			value := airuntime.DefaultAgentOptions(row.AgentKey)
			if err := json.Unmarshal([]byte(row.Options), &value); err != nil {
				return err
			}
			item := items[row.AgentKey]
			item.Options = &value
			items[row.AgentKey] = item
		}
		var counts []struct {
			AgentKey             string
			MCPCount, SkillCount int64
		}
		if err := tx.Raw(`SELECT agent_key,SUM(mcp_count) mcp_count,SUM(skill_count) skill_count FROM (SELECT agent_key,COUNT(*) mcp_count,0::bigint skill_count FROM ai_mcp_server_agent WHERE tenant_id=? GROUP BY agent_key UNION ALL SELECT agent_key,0::bigint,COUNT(*) FROM ai_skill_agent WHERE tenant_id=? GROUP BY agent_key) counts GROUP BY agent_key`, assetapp.DefaultTenantID, assetapp.DefaultTenantID).Scan(&counts).Error; err != nil {
			return err
		}
		for _, row := range counts {
			item := items[row.AgentKey]
			item.MCPCount, item.SkillCount = row.MCPCount, row.SkillCount
			items[row.AgentKey] = item
		}
		var toolCounts []struct {
			AgentKey  string
			ToolCount int64
		}
		if err := tx.Raw(`SELECT binding.agent_key,COUNT(*) AS tool_count FROM ai_tool_catalog t CROSS JOIN LATERAL jsonb_array_elements_text(t.agents) binding(agent_key) WHERE t.tenant_id=? GROUP BY binding.agent_key`, assetapp.DefaultTenantID).Scan(&toolCounts).Error; err != nil {
			return err
		}
		for _, row := range toolCounts {
			item := items[row.AgentKey]
			item.ToolCount = row.ToolCount
			items[row.AgentKey] = item
		}
		return nil
	})
	return items, err
}

func (store *AgentCatalogStore) UpdateBuiltinAgent(ctx context.Context, userID int, item airuntime.ChatAgentDefinition) error {
	return store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, userID); err != nil {
			return err
		}
		if err := lockAgentSettingsTenant(tx); err != nil {
			return err
		}
		var oldPrompt string
		err := tx.Raw(`SELECT prompt_template FROM ai_builtin_agent_config WHERE tenant_id=? AND agent_key=? FOR UPDATE`, assetapp.DefaultTenantID, item.Key).Row().Scan(&oldPrompt)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if errors.Is(err, sql.ErrNoRows) {
			if builtin, ok := airuntime.BuiltinChatAgent(item.Key); ok {
				oldPrompt = builtin.PromptTemplate
			}
		}
		if err := ensureAgentPromptInitialVersion(tx, userID, item.Key, oldPrompt); err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO ai_builtin_agent_config(tenant_id,agent_key,enabled,max_turns,run_seconds,prompt_template,updated_by)
VALUES(?,?,?,?,?,?,?) ON CONFLICT(tenant_id,agent_key) DO UPDATE SET enabled=EXCLUDED.enabled,max_turns=EXCLUDED.max_turns,run_seconds=EXCLUDED.run_seconds,prompt_template=EXCLUDED.prompt_template,updated_by=EXCLUDED.updated_by,updated_at=clock_timestamp()`,
			assetapp.DefaultTenantID, item.Key, item.Enabled, item.MaxTurns, item.RunSeconds, item.SystemPrompt, userID).Error; err != nil {
			return err
		}
		if oldPrompt != item.SystemPrompt {
			if err := appendAgentPromptVersion(tx, userID, item.Key, item.SystemPrompt); err != nil {
				return err
			}
		}
		if err := updateAgentLLMBinding(tx, userID, item); err != nil {
			return err
		}
		return updateAgentWrapup(tx, userID, item)
	})
}

func lockAgentSettingsTenant(tx *gorm.DB) error {
	var tenant string
	return tx.Raw(`SELECT id::text FROM tenant WHERE id=? AND status='active' FOR UPDATE`, assetapp.DefaultTenantID).Row().Scan(&tenant)
}

func (store *AgentCatalogStore) ListAgentPromptVersions(ctx context.Context, userID int, key string) ([]settingsapp.AgentPromptVersion, error) {
	if err := requireAgentCatalogMember(ctx, store.db, userID); err != nil {
		return nil, err
	}
	var rows []struct {
		Version   int
		Template  string
		CreatedAt time.Time
	}
	if err := store.db.WithContext(ctx).Raw(`SELECT version,template_text AS template,created_at FROM ai_agent_prompt_version WHERE tenant_id=? AND agent_key=? ORDER BY version DESC LIMIT 200`, assetapp.DefaultTenantID, key).Scan(&rows).Error; err != nil {
		return nil, err
	}
	versions := make([]settingsapp.AgentPromptVersion, 0, len(rows))
	for _, row := range rows {
		versions = append(versions, settingsapp.AgentPromptVersion{Version: row.Version, Template: row.Template, CreatedAt: row.CreatedAt})
	}
	return versions, nil
}

func ensureAgentPromptInitialVersion(tx *gorm.DB, userID int, key, prompt string) error {
	return tx.Exec(`INSERT INTO ai_agent_prompt_version(tenant_id,agent_key,version,template_text,updated_by)
SELECT ?,?,1,?,? WHERE NOT EXISTS (SELECT 1 FROM ai_agent_prompt_version WHERE tenant_id=? AND agent_key=?)`,
		assetapp.DefaultTenantID, key, prompt, userID, assetapp.DefaultTenantID, key).Error
}

func appendAgentPromptVersion(tx *gorm.DB, userID int, key, prompt string) error {
	return tx.Exec(`INSERT INTO ai_agent_prompt_version(tenant_id,agent_key,version,template_text,updated_by)
SELECT ?,?,COALESCE(MAX(version),0)+1,?,? FROM ai_agent_prompt_version WHERE tenant_id=? AND agent_key=?`,
		assetapp.DefaultTenantID, key, prompt, userID, assetapp.DefaultTenantID, key).Error
}

func updateAgentLLMBinding(tx *gorm.DB, userID int, item airuntime.ChatAgentDefinition) error {
	if !item.UpdateLLMBinding {
		return nil
	}
	if item.LLMProfileID == nil {
		return tx.Exec(`DELETE FROM ai_agent_llm_binding WHERE tenant_id=? AND agent_key=?`, assetapp.DefaultTenantID, item.Key).Error
	}
	var exists bool
	if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_llm_profile WHERE tenant_id=? AND id=?)`, assetapp.DefaultTenantID, *item.LLMProfileID).Scan(&exists).Error; err != nil {
		return err
	}
	if !exists {
		return settingsapp.ErrProfileNotFound
	}
	return tx.Exec(`INSERT INTO ai_agent_llm_binding(tenant_id,agent_key,profile_id,updated_by) VALUES(?,?,?,?)
ON CONFLICT(tenant_id,agent_key) DO UPDATE SET profile_id=EXCLUDED.profile_id,updated_by=EXCLUDED.updated_by,updated_at=clock_timestamp()`, assetapp.DefaultTenantID, item.Key, *item.LLMProfileID, userID).Error
}

func updateAgentWrapup(tx *gorm.DB, userID int, item airuntime.ChatAgentDefinition) error {
	if !item.UpdateWrapup {
		return nil
	}
	return tx.Exec(`INSERT INTO ai_agent_wrapup_config(tenant_id,agent_key,wrapup_prompt,wrapup_max_turns,task_timeout_prompt,task_timeout_max_turns,updated_by)
VALUES(?,?,?,?,?,?,?) ON CONFLICT(tenant_id,agent_key) DO UPDATE SET wrapup_prompt=EXCLUDED.wrapup_prompt,wrapup_max_turns=EXCLUDED.wrapup_max_turns,task_timeout_prompt=EXCLUDED.task_timeout_prompt,task_timeout_max_turns=EXCLUDED.task_timeout_max_turns,updated_by=EXCLUDED.updated_by,updated_at=clock_timestamp()`,
		assetapp.DefaultTenantID, item.Key, item.WrapupPrompt, item.WrapupMaxTurns, item.TaskTimeoutPrompt, item.TaskTimeoutMaxTurns, userID).Error
}

func requireAgentCatalogMember(ctx context.Context, db *gorm.DB, userID int) error {
	if userID <= 0 {
		return settingsapp.ErrForbidden
	}
	var allowed bool
	err := db.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
WHERE m.tenant_id=? AND m.user_id=? AND t.status='active' AND u.is_active=TRUE)`, assetapp.DefaultTenantID, userID).Scan(&allowed).Error
	if err != nil {
		return err
	}
	if !allowed {
		return settingsapp.ErrForbidden
	}
	return nil
}

func mapAgentCatalogConflict(err error) error {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "23505" {
		return settingsapp.ErrAgentCatalogConflict
	}
	if errors.Is(err, sql.ErrNoRows) {
		return settingsapp.ErrAgentCatalogNotFound
	}
	return err
}

var _ settingsapp.AgentCatalogStore = (*AgentCatalogStore)(nil)

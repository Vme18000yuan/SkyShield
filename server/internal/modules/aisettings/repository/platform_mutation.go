package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"github.com/yyhuni/lunafox/server/internal/pkg/dbtx"
	"gorm.io/gorm"
)

var ErrPlatformRunInactive = errors.New("AI platform action has no active authorized run")

type PlatformMutationStore struct{ db *gorm.DB }

func NewPlatformMutationStore(db *gorm.DB) *PlatformMutationStore {
	if db == nil {
		panic("AI platform mutation database is required")
	}
	return &PlatformMutationStore{db: db}
}

// Apply makes the settings write and its replay receipt one transaction. The
// run row is locked until commit, so cancellation cannot race a late write.
func (s *PlatformMutationStore) Apply(ctx context.Context, user int, run, useID, operation string, raw json.RawMessage, action func(context.Context) (string, error)) (string, error) {
	parsed, err := uuid.Parse(run)
	if err != nil || parsed.String() != run || user <= 0 || len(useID) < 1 || len(useID) > 256 || !utf8.ValidString(useID) || strings.ContainsRune(useID, '\x00') || len(raw) > 1<<20 || action == nil {
		return "", settings.ErrInvalidProfile
	}
	switch operation {
	case "create_skill", "update_skill_file", "create_custom_tool", "update_custom_tool", "create_mcp", "update_mcp":
	default:
		return "", settings.ErrInvalidProfile
	}
	var input map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&input); err != nil || input == nil {
		return "", settings.ErrInvalidProfile
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return "", settings.ErrInvalidProfile
	}
	canonical, err := json.Marshal(input)
	if err != nil {
		return "", settings.ErrInvalidProfile
	}
	digest := sha256.Sum256(canonical)
	hash := hex.EncodeToString(digest[:])
	var result string
	err = dbtx.WithinTransaction(ctx, s.db, func(txCtx context.Context, tx *gorm.DB) error {
		if err := requireAdmin(tx, user); err != nil {
			return err
		}
		// Match settings writers' lock order before holding the catalog row;
		// otherwise a concurrent UI edit can deadlock with an Agent mutation.
		if err := lockAgentSettingsTenant(tx); err != nil {
			return err
		}
		var active string
		err := tx.Raw(`SELECT r.id::text FROM ai_run r JOIN ai_conversation c ON c.id=r.conversation_id AND c.tenant_id=r.tenant_id AND c.owner_user_id=r.owner_user_id
 JOIN ai_tool_catalog tool ON tool.tenant_id=r.tenant_id AND tool.key=? AND tool.system AND tool.enabled AND tool.agents @> jsonb_build_array(CASE c.agent_key WHEN 'assistant' THEN 'mainagent' ELSE c.agent_key END)
 WHERE r.tenant_id=?::uuid AND r.owner_user_id=? AND r.id=?::uuid AND r.status='running' AND r.lease_until>clock_timestamp() AND tianqiong_ai.finding_case_review_conversation_visible(c.id)
 AND NOT EXISTS(SELECT 1 FROM ai_agent_trigger_job job LEFT JOIN ai_task task ON task.id=job.source_task_id AND task.tenant_id=job.tenant_id AND task.owner_user_id=job.owner_user_id
  WHERE job.run_id=r.id AND job.source_task_id IS NOT NULL AND (job.status<>'running' OR task.id IS NULL OR task.deleted_at IS NOT NULL OR (task.original_target_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM target x WHERE x.id=task.target_id AND x.deleted_at IS NULL)) OR EXISTS(SELECT 1 FROM ai_task_archive a WHERE a.task_id=task.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting'))))
 FOR SHARE OF c,tool`, operation, assets.DefaultTenantID, user, run).Row().Scan(&active)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPlatformRunInactive
		}
		if err != nil {
			return err
		}
		// Conversation startup locks c before r. Use the same order to avoid a
		// deadlock if a run expires while another message is beginning.
		err = tx.Raw(`SELECT id::text FROM ai_run WHERE tenant_id=?::uuid AND owner_user_id=? AND id=?::uuid AND status='running' AND lease_until>clock_timestamp() FOR UPDATE`, assets.DefaultTenantID, user, run).Row().Scan(&active)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPlatformRunInactive
		}
		if err != nil {
			return err
		}
		var previous struct {
			Operation, RequestHash string
			Result                 []byte
		}
		prior := tx.Raw(`SELECT operation,request_hash,result FROM ai_tool_mutation_receipt WHERE tenant_id=?::uuid AND owner_user_id=? AND execution_id=?::uuid AND tool_use_id=?`, assets.DefaultTenantID, user, run, useID).Scan(&previous)
		if prior.Error != nil {
			return prior.Error
		}
		if prior.RowsAffected > 0 {
			if previous.Operation != operation || previous.RequestHash != hash {
				return settings.ErrInvalidProfile
			}
			var stored struct {
				Message string `json:"message"`
			}
			if err = json.Unmarshal(previous.Result, &stored); err != nil {
				return err
			}
			result = stored.Message
			return nil
		}
		result, err = action(txCtx)
		if err != nil {
			return err
		}
		if len(result) > 4096 || !utf8.ValidString(result) {
			return settings.ErrInvalidProfile
		}
		var valid bool
		if err = tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_run WHERE id=?::uuid AND tenant_id=?::uuid AND owner_user_id=? AND status='running' AND lease_until>clock_timestamp())`, run, assets.DefaultTenantID, user).Scan(&valid).Error; err != nil {
			return err
		}
		if !valid {
			return ErrPlatformRunInactive
		}
		receipt, _ := json.Marshal(map[string]string{"message": result})
		return tx.Exec(`INSERT INTO ai_tool_mutation_receipt(tenant_id,owner_user_id,execution_id,tool_use_id,operation,request_hash,result) VALUES(?::uuid,?,?::uuid,?,?,?,?::jsonb)`, assets.DefaultTenantID, user, run, useID, operation, hash, string(receipt)).Error
	})
	return result, err
}

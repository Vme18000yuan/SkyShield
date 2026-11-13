package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

func (s *TaskStore) CreateAutoTaskHints(ctx context.Context, user int, taskID int64, mutation aiapp.AutoTaskMutation, items []aiapp.AutoTaskHint) (aiapp.AutoTaskHintResult, error) {
	result := aiapp.AutoTaskHintResult{IDs: make([]int64, len(items)), Errors: map[string]string{}}
	if user <= 0 || taskID <= 0 || len(items) < 1 || len(items) > 100 {
		return result, aiapp.ErrInvalidInput
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := acquireCalendarLock(tx, true); err != nil {
			return err
		}
		var active string
		err := tx.Raw(`SELECT r.id::text FROM ai_run r JOIN ai_conversation c ON c.id=r.conversation_id AND c.tenant_id=r.tenant_id AND c.owner_user_id=r.owner_user_id JOIN ai_tool_catalog tool ON tool.tenant_id=r.tenant_id AND tool.key='add_task_hint' AND tool.system AND tool.enabled AND tool.agents @> jsonb_build_array(CASE c.agent_key WHEN 'assistant' THEN 'mainagent' ELSE c.agent_key END) WHERE r.tenant_id=?::uuid AND r.owner_user_id=? AND r.id=?::uuid AND r.status='running' AND r.lease_until>clock_timestamp() FOR SHARE OF c,tool`, assets.DefaultTenantID, user, mutation.RunID).Row().Scan(&active)
		if errors.Is(err, sql.ErrNoRows) {
			return aiapp.ErrRunLeaseLost
		}
		if err != nil {
			return err
		}
		err = tx.Raw(`SELECT id::text FROM ai_run WHERE tenant_id=?::uuid AND owner_user_id=? AND id=?::uuid AND status='running' AND lease_until>clock_timestamp() FOR UPDATE`, assets.DefaultTenantID, user, mutation.RunID).Row().Scan(&active)
		if errors.Is(err, sql.ErrNoRows) {
			return aiapp.ErrRunLeaseLost
		}
		if err != nil {
			return err
		}
		scope, err := lockHumanGoalScope(tx, user, taskID)
		if err != nil {
			return err
		}
		if err = tx.Exec(`SELECT set_config('tianqiong.task_id',?,TRUE),set_config('tianqiong.owner_user_id',?,TRUE),set_config('tianqiong.execution_id',?,TRUE),set_config('tianqiong.execution_mode','auto_hint',TRUE)`, strconv.FormatInt(taskID, 10), strconv.Itoa(user), mutation.RunID).Error; err != nil {
			return err
		}
		var checked int64
		if err = tx.Raw(`SELECT (tianqiong_ai.assert_auto_hint_scope(?,NULL)).id`, scope.ExplorationID).Row().Scan(&checked); err != nil || checked != taskID {
			return aiapp.ErrRunLeaseLost
		}
		var previous struct {
			Operation, RequestHash string
			Result                 []byte
		}
		prior := tx.Raw(`SELECT operation,request_hash,result FROM ai_tool_mutation_receipt WHERE tenant_id=?::uuid AND owner_user_id=? AND execution_id=?::uuid AND tool_use_id=?`, assets.DefaultTenantID, user, mutation.RunID, mutation.ToolUseID).Scan(&previous)
		if prior.Error != nil {
			return prior.Error
		}
		if prior.RowsAffected > 0 {
			if previous.Operation != "auto_task_hint" || previous.RequestHash != mutation.InputHash {
				return aiapp.ErrInvalidInput
			}
			return json.Unmarshal(previous.Result, &result)
		}
		texts := []string{}
		for index, item := range items {
			if item.ValidationError != "" {
				result.Errors[strconv.Itoa(index)] = item.ValidationError
				continue
			}
			point := fmt.Sprintf("auto_hint_%d", index)
			if err = tx.SavePoint(point).Error; err != nil {
				return err
			}
			payload := map[string]any{"text": item.Text}
			if len(item.TrafficRefs) > 0 {
				payload["traffic_refs"] = item.TrafficRefs
			}
			raw, _ := json.Marshal(payload)
			var id int64
			err = tx.Raw(`INSERT INTO tianqiong_ai.exploration_nodes(exploration_id,kind,payload,priority,state,origin) VALUES(?,'hint',?::jsonb,0,'active','human') RETURNING id`, scope.ExplorationID, string(raw)).Row().Scan(&id)
			if err == nil {
				err = tx.Exec(`SELECT set_config('tianqiong.hint_id',?,TRUE)`, strconv.FormatInt(id, 10)).Error
			}
			if err == nil {
				for _, asset := range item.AssetIDs {
					if err = tx.Exec(`INSERT INTO tianqiong_ai.exploration_anchors(node_id,asset_id) VALUES(?,?) ON CONFLICT DO NOTHING`, id, asset).Error; err != nil {
						break
					}
				}
			}
			if err != nil {
				var pgErr *pgconn.PgError
				if !errors.As(err, &pgErr) || pgErr.Code != "P0001" && pgErr.Code != "23503" {
					return err
				}
				if rollbackErr := tx.RollbackTo(point).Error; rollbackErr != nil {
					return rollbackErr
				}
				result.Errors[strconv.Itoa(index)] = "提示、资产或流量引用不可用"
				continue
			}
			result.IDs[index] = id
			texts = append(texts, item.Text)
		}
		if len(texts) > 0 {
			if err = insertHumanGoalEvent(tx, user, taskID, "hint", map[string]any{"hints": texts}); err != nil {
				return err
			}
			cleanup := tx.Exec(`DELETE FROM tianqiong_ai.auto_hint_staging WHERE task_id=? AND run_id=?::uuid AND transaction_id=txid_current()`, taskID, mutation.RunID)
			if cleanup.Error != nil {
				return cleanup.Error
			}
			if cleanup.RowsAffected != int64(len(texts)) {
				return errors.New("AI Auto hint staging cleanup failed")
			}
		}
		if err = tx.Raw(`SELECT (tianqiong_ai.assert_auto_hint_scope(?,NULL)).id`, scope.ExplorationID).Row().Scan(&checked); err != nil || checked != taskID {
			return aiapp.ErrRunLeaseLost
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Exec(`INSERT INTO ai_tool_mutation_receipt(tenant_id,owner_user_id,execution_id,tool_use_id,operation,request_hash,result) VALUES(?::uuid,?,?::uuid,?,'auto_task_hint',?,?::jsonb)`, assets.DefaultTenantID, user, mutation.RunID, mutation.ToolUseID, mutation.InputHash, string(raw)).Error
	})
	return result, err
}

var _ aiapp.AutoTaskHintStore = (*TaskStore)(nil)

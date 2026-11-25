package aicleanup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/traffic"
	"github.com/lib/pq"
	"github.com/yyhuni/lunafox/server/internal/aitraffic"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

func (s *Service) DeleteExecutions(ctx context.Context, user int, kind string, rawIDs []string) (Result, error) {
	if (kind != "task" && kind != "conversation") || len(rawIDs) < 1 || len(rawIDs) > 100 {
		return Result{}, ErrInvalid
	}
	ids := []int64{}
	seen := map[int64]bool{}
	for _, raw := range rawIDs {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != raw || seen[id] {
			return Result{}, ErrInvalid
		}
		seen[id] = true
		ids = append(ids, id)
	}
	var deleted int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := authorize(tx, user); err != nil {
			return err
		}
		for _, id := range ids {
			var scope string
			var query string
			if kind == "conversation" {
				query = `SELECT a.conversation_id::text FROM ai_conversation_activity a WHERE a.tenant_id=?::uuid AND a.owner_user_id=? AND a.id=? AND a.kind='tool_use'`
			} else {
				query = `SELECT t.id::text FROM tianqiong_ai.activity a JOIN ai_task t ON t.exploration_id=a.exploration_id WHERE t.tenant_id=?::uuid AND t.owner_user_id=? AND a.id=? AND a.kind='tool_use'`
			}
			err := tx.Raw(query, assets.DefaultTenantID, user, id).Row().Scan(&scope)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
			exp, err := lockScope(tx, user, kind, scope)
			if err != nil {
				return err
			}
			if kind == "task" && s.activeTask != nil {
				taskID, _ := strconv.ParseInt(scope, 10, 64)
				if s.activeTask(taskID) {
					return ErrBusy
				}
			}
			var pair []int64
			if kind == "conversation" {
				query = `SELECT a.id FROM ai_conversation_activity u JOIN ai_conversation_activity a
 ON a.run_id=u.run_id AND a.conversation_id=u.conversation_id AND (a.id=u.id OR (a.kind='tool_result' AND (
  (u.trace_call_id IS NOT NULL AND a.trace_call_id=u.trace_call_id) OR
  (u.trace_call_id IS NULL AND a.trace_call_id IS NULL AND u.tool_use_id<>'' AND a.tool_use_id=u.tool_use_id AND a.id>u.id
   AND NOT EXISTS(SELECT 1 FROM ai_conversation_activity later WHERE later.run_id=u.run_id AND later.kind='tool_use'
    AND later.tool_use_id=u.tool_use_id AND later.id>u.id AND later.id<a.id))
 ))) WHERE u.id=? ORDER BY a.id`
			} else {
				query = `SELECT a.id FROM tianqiong_ai.activity u JOIN tianqiong_ai.activity a ON a.exploration_id=u.exploration_id AND (a.id=u.id OR (a.kind='tool_result' AND u.execution_id IS NOT NULL AND u.tool_use_id IS NOT NULL AND a.execution_id=u.execution_id AND a.tool_use_id=u.tool_use_id AND a.worker IS NOT DISTINCT FROM u.worker AND a.node_id IS NOT DISTINCT FROM u.node_id AND a.id>u.id AND NOT EXISTS(SELECT 1 FROM tianqiong_ai.activity later WHERE later.exploration_id=u.exploration_id AND later.execution_id=u.execution_id AND later.worker IS NOT DISTINCT FROM u.worker AND later.node_id IS NOT DISTINCT FROM u.node_id AND later.tool_use_id=u.tool_use_id AND later.kind='tool_use' AND later.id>u.id AND later.id<a.id))) WHERE u.id=? ORDER BY a.id`
			}
			if err = tx.Raw(query, id).Scan(&pair).Error; err != nil {
				return err
			}
			if kind == "conversation" {
				if err = tx.Exec(`DELETE FROM ai_conversation_activity WHERE id=ANY(?::bigint[])`, pq.Int64Array(pair)).Error; err != nil {
					return err
				}
			} else {
				job, err := queue(tx, user, "task_tools", scope, exp, map[string]any{"activity_ids": pair})
				if err != nil {
					return err
				}
				if err = tx.Exec(`SELECT set_config('tianqiong.execution_mode','storage_cleanup',TRUE),set_config('tianqiong.storage_cleanup_id',?,TRUE),set_config('tianqiong.owner_user_id',?,TRUE)`, job, strconv.Itoa(user)).Error; err != nil {
					return err
				}
				if err = tx.Exec(`DELETE FROM tianqiong_ai.activity WHERE id=ANY(?::bigint[])`, pq.Int64Array(pair)).Error; err != nil {
					return err
				}
				if err = tx.Exec(`DELETE FROM ai_storage_cleanup WHERE id=?::uuid`, job).Error; err != nil {
					return err
				}
			}
			deleted++
		}
		return nil
	})
	return Result{Deleted: deleted}, err
}

func (s *Service) DeleteTraffic(ctx context.Context, user int, kind, id string, ids []string, all bool) (Result, error) {
	if s.traffic == nil || all && len(ids) > 0 || !all && (len(ids) == 0 || len(ids) > 100) {
		return Result{}, ErrInvalid
	}
	seen := map[string]bool{}
	for _, value := range ids {
		if value == "" || len(value) > 128 || strings.ContainsRune(value, 0) || seen[value] {
			return Result{}, ErrInvalid
		}
		seen[value] = true
	}
	var deleted int64
	var operationErr error
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockScope(tx, user, kind, id); err != nil {
			return err
		}
		scope := aitraffic.Scope{TenantID: assets.DefaultTenantID, UserID: user}
		if kind == "conversation" {
			scope.ConversationID = id
		} else {
			scope.TaskID, _ = strconv.ParseInt(id, 10, 64)
		}
		if s.traffic.ScopeInUse(scope) || kind == "task" && s.activeTask != nil && s.activeTask(scope.TaskID) {
			return ErrBusy
		}
		_, err := s.traffic.Read(ctx, scope, func(store *traffic.Traffic) error {
			var err error
			if all {
				deleted, _, err = store.DeleteAll()
			} else {
				deleted, err = store.DeleteExchanges(ids)
			}
			if err == nil {
				err = store.FinishCleanup()
			}
			return err
		})
		operationErr = err
		if err != nil {
			_, err = queue(tx, user, "traffic", id, 0, cleanupPayload{TrafficKind: kind})
		}
		return err
	})
	if err != nil {
		return Result{}, err
	}
	if operationErr != nil && deleted == 0 {
		return Result{CleanupPending: true}, operationErr
	}
	return Result{Deleted: deleted, CleanupPending: operationErr != nil}, nil
}

func (s *Service) finishTrafficCleanup(ctx context.Context, tx *gorm.DB, job cleanupJob) error {
	var payload cleanupPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return err
	}
	if s.traffic == nil {
		return ErrBusy
	}
	if _, err := lockScope(tx, job.OwnerUserID, payload.TrafficKind, job.ScopeID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	scope := aitraffic.Scope{TenantID: job.TenantID, UserID: job.OwnerUserID}
	if payload.TrafficKind == "task" {
		scope.TaskID, _ = strconv.ParseInt(job.ScopeID, 10, 64)
	} else {
		scope.ConversationID = job.ScopeID
	}
	if s.traffic.ScopeInUse(scope) || payload.TrafficKind == "task" && s.activeTask != nil && s.activeTask(scope.TaskID) {
		return ErrBusy
	}
	_, err := s.traffic.Read(ctx, scope, func(store *traffic.Traffic) error { return store.FinishCleanup() })
	return err
}

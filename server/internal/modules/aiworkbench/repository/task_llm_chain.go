package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/lib/pq"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

// Profile deletion and chain replacement take the same tenant → task → profile
// lock order. Quota transitions lock the task and are fenced by the run lease.
func lockTaskProfileTenant(tx *gorm.DB) error {
	var tenant string
	return tx.Raw(`SELECT id::text FROM tenant WHERE id=? AND status='active' FOR UPDATE`, assets.DefaultTenantID).Row().Scan(&tenant)
}

func insertTaskLLMChain(tx *gorm.DB, taskID int64, ids []string, active *string) error {
	for position, id := range ids {
		var found string
		err := tx.Raw(`SELECT id::text FROM ai_llm_profile WHERE tenant_id=? AND id=? FOR KEY SHARE`, assets.DefaultTenantID, id).Row().Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return aiapp.ErrInvalidInput
		}
		if err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO ai_task_llm_profile(tenant_id,task_id,profile_id,position) VALUES(?,?,?,?)`, assets.DefaultTenantID, taskID, id, position).Error; err != nil {
			return err
		}
	}
	return tx.Exec(`UPDATE ai_task SET active_llm_profile_id=?,llm_chain_revision=llm_chain_revision+1 WHERE tenant_id=? AND id=?`, active, assets.DefaultTenantID, taskID).Error
}

func taskLLMState(tx *gorm.DB, user int, id int64) (aiapp.TaskLLMState, error) {
	state := aiapp.TaskLLMState{Profiles: []aiapp.TaskLLMProfile{}, State: "default"}
	var active sql.NullString
	var raw []byte
	err := tx.Raw(`SELECT t.active_llm_profile_id::text,t.llm_chain_revision,COALESCE((SELECT jsonb_agg(jsonb_build_object('profileId',p.profile_id::text,'position',p.position,'status',p.status,'lastError',p.last_error,'exhaustedAt',p.exhausted_at) ORDER BY p.position) FROM ai_task_llm_profile p WHERE p.task_id=t.id),'[]'::jsonb)`+taskOwnerJoin+` WHERE `+taskOwnerWhere+` AND t.id=?`, assets.DefaultTenantID, user, id).Row().Scan(&active, &state.Revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return state, aiapp.ErrTaskNotFound
	}
	if err != nil {
		return state, err
	}
	if active.Valid {
		state.ActiveProfileID = &active.String
	}
	if err = json.Unmarshal(raw, &state.Profiles); err != nil {
		return state, err
	}
	if len(state.Profiles) > 0 {
		state.State = "chain_exhausted"
		for _, entry := range state.Profiles {
			if state.ActiveProfileID != nil && entry.ProfileID == *state.ActiveProfileID && entry.Status == "ready" {
				state.State = "ready"
			}
		}
	}
	var latest int64
	for _, entry := range state.Profiles {
		if entry.ExhaustedAt != nil && entry.ExhaustedAt.UnixNano() > latest {
			latest = entry.ExhaustedAt.UnixNano()
			state.Reason = entry.LastError
		}
	}
	return state, nil
}

func (s *TaskStore) TaskLLMState(ctx context.Context, user int, id int64) (aiapp.TaskLLMState, error) {
	return taskLLMState(s.db.WithContext(ctx), user, id)
}

func (s *TaskStore) ReplaceTaskLLMChain(ctx context.Context, user int, id int64, ids []string, active *string) (aiapp.TaskLLMState, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := scheduleWriteRole(tx, user); err != nil {
			return err
		}
		if err := lockTaskProfileTenant(tx); err != nil {
			return err
		}
		var previous sql.NullString
		var status, run string
		var live bool
		err := tx.Raw(`SELECT t.active_llm_profile_id::text,t.status,t.active_run::text,COALESCE(t.lease_until>clock_timestamp(),FALSE)`+taskOwnerJoin+` WHERE `+taskOwnerWhere+` AND t.id=? FOR UPDATE OF t`, assets.DefaultTenantID, user, id).Row().Scan(&previous, &status, &run, &live)
		if errors.Is(err, sql.ErrNoRows) {
			return aiapp.ErrTaskNotFound
		}
		if err != nil {
			return err
		}
		if err = tx.Exec(`DELETE FROM ai_task_llm_profile WHERE tenant_id=? AND task_id=?`, assets.DefaultTenantID, id).Error; err != nil {
			return err
		}
		if err = insertTaskLLMChain(tx, id, ids, active); err != nil {
			return err
		}
		var old *string
		if previous.Valid {
			old = &previous.String
		}
		if err = tx.Exec(`INSERT INTO ai_task_llm_history(tenant_id,task_id,owner_user_id,mode,previous_profile_id,next_profile_id,reason) VALUES(?,?,?,'manual',?,?,'用户更新任务模型链')`, assets.DefaultTenantID, id, user, old, active).Error; err != nil {
			return err
		}
		if status == "running" && live {
			return emitTaskLLMTransition(tx, id, user, run, "manual", old, active, "用户更新任务模型链")
		}
		return nil
	})
	if err != nil {
		return aiapp.TaskLLMState{}, err
	}
	return s.TaskLLMState(ctx, user, id)
}

func (s *TaskStore) ExhaustTaskLLMProfile(ctx context.Context, task airuntime.TaskExecution, profileID string, revision int64, reason string) (aiapp.TaskLLMTransition, error) {
	transition := aiapp.TaskLLMTransition{PreviousProfileID: profileID}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current sql.NullString
		var currentRevision int64
		err := tx.Raw(`SELECT t.active_llm_profile_id::text,t.llm_chain_revision`+taskOwnerJoin+` WHERE `+taskOwnerWhere+` AND t.id=? AND tianqiong_ai.task_run_live(t.id,t.owner_user_id,?::uuid) FOR UPDATE OF t`, assets.DefaultTenantID, task.UserID, task.ID, task.RunID).Row().Scan(&current, &currentRevision)
		if errors.Is(err, sql.ErrNoRows) {
			return aiapp.ErrRunLeaseLost
		}
		if err != nil {
			return err
		}
		if currentRevision != revision {
			transition.Stale = true
			if current.Valid {
				transition.NextProfileID = &current.String
			}
			return nil
		}
		if !current.Valid {
			transition.Exhausted = true
			return nil
		}
		if current.String != profileID {
			transition.Stale = true
			transition.NextProfileID = &current.String
			return nil
		}
		var position int
		err = tx.Raw(`SELECT position FROM ai_task_llm_profile WHERE tenant_id=? AND task_id=? AND profile_id=?`, assets.DefaultTenantID, task.ID, profileID).Row().Scan(&position)
		if errors.Is(err, sql.ErrNoRows) {
			transition.Stale = true
			return nil
		}
		if err != nil {
			return err
		}
		reason = strings.TrimSpace(strings.ToValidUTF8(reason, "�"))
		for len(reason) > 1000 {
			_, size := utf8.DecodeLastRuneInString(reason)
			reason = reason[:len(reason)-size]
		}
		if err = tx.Exec(`UPDATE ai_task_llm_profile SET status='quota_exhausted',last_error=?,exhausted_at=clock_timestamp() WHERE tenant_id=? AND task_id=? AND profile_id=?`, reason, assets.DefaultTenantID, task.ID, profileID).Error; err != nil {
			return err
		}
		var next sql.NullString
		err = tx.Raw(`SELECT profile_id::text FROM ai_task_llm_profile WHERE tenant_id=? AND task_id=? AND position>? AND status='ready' ORDER BY position LIMIT 1`, assets.DefaultTenantID, task.ID, position).Row().Scan(&next)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if next.Valid {
			transition.NextProfileID = &next.String
		} else {
			transition.Exhausted = true
		}
		transition.Advanced = true
		if err = tx.Exec(`UPDATE ai_task SET active_llm_profile_id=?,llm_chain_revision=llm_chain_revision+1 WHERE tenant_id=? AND id=?`, transition.NextProfileID, assets.DefaultTenantID, task.ID).Error; err != nil {
			return err
		}
		mode := "automatic"
		if transition.Exhausted {
			mode = "exhausted"
		}
		if err = tx.Exec(`INSERT INTO ai_task_llm_history(tenant_id,task_id,owner_user_id,mode,previous_profile_id,next_profile_id,reason) VALUES(?,?,?,?,?,?,?)`, assets.DefaultTenantID, task.ID, task.UserID, mode, profileID, transition.NextProfileID, reason).Error; err != nil {
			return err
		}
		return emitTaskLLMTransition(tx, task.ID, task.UserID, task.RunID, mode, &profileID, transition.NextProfileID, reason)
	})
	return transition, err
}

func emitTaskLLMTransition(tx *gorm.DB, taskID int64, user int, run, mode string, previous, next *string, reason string) error {
	if err := tx.Exec(`SELECT set_config('tianqiong.task_id',?,TRUE),set_config('tianqiong.owner_user_id',?,TRUE),set_config('tianqiong.execution_id',?,TRUE),set_config('tianqiong.execution_mode',tianqiong_ai.task_run_mode(?::bigint,?::uuid),TRUE)`, strconv.FormatInt(taskID, 10), strconv.Itoa(user), run, taskID, run).Error; err != nil {
		return err
	}
	metadata, _ := json.Marshal(map[string]any{"llm_transition": map[string]any{"mode": mode, "reason": reason, "previous_profile_id": previous, "next_profile_id": next}})
	summary := "任务模型配置已切换"
	if mode == "exhausted" {
		summary = "任务模型链额度已耗尽"
	}
	return tx.Exec(`INSERT INTO tianqiong_ai.activity(exploration_id,worker,kind,summary,detail,metadata) SELECT exploration_id,'system','llm_failover',?,?,?::jsonb FROM ai_task WHERE tenant_id=? AND owner_user_id=? AND id=?`, summary, reason, string(metadata), assets.DefaultTenantID, user, taskID).Error
}

// Hydration batches the owner-authorized task list and does not expose other tenants.
func (s *TaskStore) hydrateTaskLLM(ctx context.Context, items []aiapp.Task) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]int64, len(items))
	index := map[int64]int{}
	for i := range items {
		ids[i] = items[i].ID
		index[ids[i]] = i
		items[i].LLMProfileIDs = []string{}
		items[i].LLMFailoverState = "default"
	}
	var entries []struct {
		TaskID      int64
		ProfileID   string
		Status      string
		LastError   string
		ExhaustedAt sql.NullTime
	}
	if err := s.db.WithContext(ctx).Raw(`SELECT task_id,profile_id::text,status,last_error,exhausted_at FROM ai_task_llm_profile WHERE tenant_id=? AND task_id=ANY(?::bigint[]) ORDER BY task_id,position`, assets.DefaultTenantID, pq.Int64Array(ids)).Scan(&entries).Error; err != nil {
		return err
	}
	latest := map[int64]int64{}
	for _, entry := range entries {
		item := &items[index[entry.TaskID]]
		item.LLMProfileIDs = append(item.LLMProfileIDs, entry.ProfileID)
		item.LLMFailoverState = "chain_exhausted"
		if entry.ExhaustedAt.Valid && entry.ExhaustedAt.Time.UnixNano() > latest[entry.TaskID] {
			latest[entry.TaskID] = entry.ExhaustedAt.Time.UnixNano()
			item.LLMFailoverReason = entry.LastError
		}
	}
	for i := range items {
		if len(items[i].LLMProfileIDs) > 0 && items[i].ActiveLLMProfileID != nil {
			for _, entry := range entries {
				if entry.TaskID == items[i].ID && entry.ProfileID == *items[i].ActiveLLMProfileID && entry.Status == "ready" {
					items[i].LLMFailoverState = "ready"
					break
				}
			}
		}
	}
	return nil
}

var _ aiapp.TaskLLMStore = (*TaskStore)(nil)

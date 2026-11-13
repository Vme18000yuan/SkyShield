package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

type LLMUsageStore struct{ db *gorm.DB }

func NewLLMUsageStore(db *gorm.DB) *LLMUsageStore { return &LLMUsageStore{db: db} }
func (s *LLMUsageStore) StartLLMRecording(ctx context.Context, item airuntime.LLMUsageRecord) (bool, airuntime.LLMRecordingLease, error) {
	var enabled bool
	lease := airuntime.LLMRecordingLease{}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCatalogMember(ctx, tx, item.UserID); err != nil {
			return err
		}
		if err := tx.Raw(`SELECT COALESCE((SELECT options->>'llm_record' FROM ai_runtime_setting WHERE tenant_id=?::uuid),'false')='true'`, assets.DefaultTenantID).Row().Scan(&enabled); err != nil || !enabled {
			return err
		}
		if item.ConversationID != "" {
			if parsed, err := uuid.Parse(item.ConversationID); err != nil || parsed.String() != item.ConversationID {
				return settingsapp.ErrInvalidUsageQuery
			}
			var exists bool
			if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_conversation WHERE id=?::uuid AND tenant_id=?::uuid AND owner_user_id=?)`, item.ConversationID, assets.DefaultTenantID, item.UserID).Scan(&exists).Error; err != nil {
				return err
			} else if !exists {
				return settingsapp.ErrUsageTaskNotFound
			}
		}
		if item.ExecutionID != "" {
			if parsed, err := uuid.Parse(item.ExecutionID); err != nil || parsed.String() != item.ExecutionID {
				return settingsapp.ErrInvalidUsageQuery
			}
		}
		task := item.TaskID
		if task == 0 && item.ConversationID != "" && item.ExecutionID != "" {
			if err := tx.Raw(`SELECT COALESCE((SELECT source_task_id FROM ai_agent_trigger_job WHERE tenant_id=?::uuid AND owner_user_id=? AND conversation_id=?::uuid AND run_id=?::uuid LIMIT 1),0)`, assets.DefaultTenantID, item.UserID, item.ConversationID, item.ExecutionID).Row().Scan(&task); err != nil {
				return err
			}
		}
		if task > 0 {
			if err := tx.Raw(`SELECT t.id,t.exploration_id FROM ai_task t WHERE t.id=? AND t.tenant_id=?::uuid AND t.owner_user_id=? AND t.deleted_at IS NULL AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target x WHERE x.id=t.target_id AND x.deleted_at IS NULL))`, task, assets.DefaultTenantID, item.UserID).Row().Scan(&lease.TaskID, &lease.ExplorationID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return settingsapp.ErrUsageTaskNotFound
				}
				return err
			}
			if err := tx.Raw(`SELECT COALESCE((SELECT generation FROM ai_llm_record_epoch WHERE tenant_id=?::uuid AND owner_user_id=? AND task_id=?),0)`, assets.DefaultTenantID, item.UserID, task).Row().Scan(&lease.Generation); err != nil {
				return err
			}
		}
		return nil
	})
	return enabled, lease, err
}
func (s *LLMUsageStore) InsertLLMRecording(ctx context.Context, id string, item airuntime.LLMUsageRecord, lease airuntime.LLMRecordingLease, truncated bool, sealed []byte) error {
	if s == nil || s.db == nil || item.UserID <= 0 || len(sealed) == 0 || len(sealed) > 40<<20 || len(item.SessionID) > 500 || !utf8.ValidString(item.SessionID) || strings.ContainsRune(item.SessionID, '\x00') {
		return errors.New("invalid model recording")
	}
	if lease.TaskID < 0 || lease.Generation < 0 || item.TaskID > 0 && item.TaskID != lease.TaskID {
		return errors.New("invalid model recording scope")
	}
	for _, value := range []string{id, item.ProfileID, item.ConversationID, item.ExecutionID} {
		if value != "" {
			if parsed, err := uuid.Parse(value); err != nil || parsed.String() != value {
				return errors.New("invalid model recording identity")
			}
		}
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if lease.TaskID > 0 {
			if err := tx.Exec(`INSERT INTO ai_llm_record_epoch(tenant_id,owner_user_id,task_id) VALUES(?::uuid,?,?) ON CONFLICT DO NOTHING`, assets.DefaultTenantID, item.UserID, lease.TaskID).Error; err != nil {
				return err
			}
			var generation int64
			if err := tx.Raw(`SELECT generation FROM ai_llm_record_epoch WHERE tenant_id=?::uuid AND owner_user_id=? AND task_id=? FOR SHARE`, assets.DefaultTenantID, item.UserID, lease.TaskID).Row().Scan(&generation); err != nil {
				return err
			}
			if generation != lease.Generation {
				return nil
			}
		}
		return tx.Exec(`INSERT INTO ai_llm_record(id,tenant_id,owner_user_id,task_id,exploration_id,conversation_id,execution_id,session_id,worker,model,profile_id,profile_name,duration_ms,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,status,truncated,sealed_payload)
 VALUES(?::uuid,?::uuid,?,NULLIF(?,0),NULLIF(?,0),NULLIF(?,'')::uuid,NULLIF(?,'')::uuid,?,?,?,NULLIF(?,'')::uuid,?,?,?,?,?,?,?,?,?)`,
			id, assets.DefaultTenantID, item.UserID, lease.TaskID, lease.ExplorationID, item.ConversationID, item.ExecutionID, item.SessionID, item.Worker, item.Model, item.ProfileID, item.ProfileName, max(int64(item.Duration.Milliseconds()), 0), max(item.Usage.InputTokens, 0), max(item.Usage.OutputTokens, 0), max(item.Usage.CacheReadTokens, 0), max(item.Usage.CacheWriteTokens, 0), item.Status, truncated, sealed).Error
	})
}

const visibleLLMRecording = ` FROM ai_llm_record r WHERE r.tenant_id=?::uuid AND r.owner_user_id=?
 AND (r.task_id IS NULL OR EXISTS(SELECT 1 FROM ai_task t WHERE t.id=r.task_id AND t.tenant_id=r.tenant_id AND t.owner_user_id=r.owner_user_id AND t.deleted_at IS NULL AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target x WHERE x.id=t.target_id AND x.deleted_at IS NULL))))
 AND (r.conversation_id IS NULL OR tianqiong_ai.finding_case_review_conversation_visible(r.conversation_id))
 AND (r.task_id IS NOT NULL OR r.conversation_id IS NULL OR EXISTS(SELECT 1 FROM ai_conversation c WHERE c.id=r.conversation_id AND c.tenant_id=r.tenant_id AND c.owner_user_id=r.owner_user_id))`
const llmRecordingColumns = `r.id::text,r.task_id,COALESCE((SELECT t.name FROM ai_task t WHERE t.id=r.task_id AND t.tenant_id=r.tenant_id AND t.owner_user_id=r.owner_user_id),''),COALESCE(r.conversation_id::text,''),r.session_id,r.worker,r.model,r.profile_name,r.duration_ms,r.input_tokens,r.output_tokens,r.cache_read_tokens,r.cache_write_tokens,r.status,r.truncated,r.created_at`

type llmRecordingScanner interface{ Scan(...any) error }

func scanLLMRecording(row llmRecordingScanner, includePayload bool) (settingsapp.LLMRecordingEntry, error) {
	var item settingsapp.LLMRecordingEntry
	dest := []any{&item.ID, &item.TaskID, &item.TaskName, &item.ConversationID, &item.SessionID, &item.Worker, &item.Model, &item.ProfileName, &item.DurationMS, &item.InputTokens, &item.OutputTokens, &item.CacheReadTokens, &item.CacheWriteTokens, &item.Status, &item.Truncated, &item.CreatedAt}
	if includePayload {
		dest = append(dest, &item.Sealed)
	}
	err := row.Scan(dest...)
	return item, err
}
func (s *LLMUsageStore) ListLLMRecordings(ctx context.Context, user int, filter settingsapp.LLMRecordingFilter) (settingsapp.LLMRecordingPage, error) {
	out := settingsapp.LLMRecordingPage{Items: []settingsapp.LLMRecordingEntry{}, Page: filter.Page, Size: filter.Size}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCatalogMember(ctx, tx, user); err != nil {
			return err
		}
		where := visibleLLMRecording + ` AND (?=0 OR r.task_id=?) AND (?='' OR strpos(lower(r.model),lower(?))>0) AND (?='' OR strpos(lower(r.session_id),lower(?))>0)`
		args := []any{assets.DefaultTenantID, user, filter.TaskID, filter.TaskID, filter.Model, filter.Model, filter.Session, filter.Session}
		if err := tx.Raw(`SELECT COUNT(*)`+where, args...).Row().Scan(&out.Total); err != nil {
			return err
		}
		queryArgs := append(append([]any{}, args...), filter.Size, filter.Page*filter.Size)
		rows, err := tx.Raw(`SELECT `+llmRecordingColumns+where+` ORDER BY r.created_at DESC,r.id DESC LIMIT ? OFFSET ?`, queryArgs...).Rows()
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanLLMRecording(rows, false)
			if err != nil {
				return err
			}
			out.Items = append(out.Items, item)
		}
		return rows.Err()
	})
	return out, err
}
func (s *LLMUsageStore) GetLLMRecording(ctx context.Context, user int, id string) (settingsapp.LLMRecordingEntry, error) {
	var item settingsapp.LLMRecordingEntry
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCatalogMember(ctx, tx, user); err != nil {
			return err
		}
		var err error
		item, err = scanLLMRecording(tx.Raw(`SELECT `+llmRecordingColumns+`,r.sealed_payload`+visibleLLMRecording+` AND r.id=?::uuid`, assets.DefaultTenantID, user, id).Row(), true)
		if errors.Is(err, sql.ErrNoRows) {
			return settingsapp.ErrUsageTaskNotFound
		}
		return err
	})
	return item, err
}
func (s *LLMUsageStore) LLMRecordingVisible(ctx context.Context, user int, id string) (bool, error) {
	var visible bool
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCatalogMember(ctx, tx, user); err != nil {
			return err
		}
		return tx.Raw(`SELECT EXISTS(SELECT 1`+visibleLLMRecording+` AND r.id=?::uuid)`, assets.DefaultTenantID, user, id).Scan(&visible).Error
	})
	return visible, err
}
func (s *LLMUsageStore) DeleteTaskLLMRecordings(ctx context.Context, user int, task int64) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCatalogMember(ctx, tx, user); err != nil {
			return err
		}
		var owned bool
		if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_task WHERE id=? AND tenant_id=?::uuid AND owner_user_id=?) OR EXISTS(SELECT 1 FROM ai_llm_record WHERE task_id=? AND tenant_id=?::uuid AND owner_user_id=?)`, task, assets.DefaultTenantID, user, task, assets.DefaultTenantID, user).Scan(&owned).Error; err != nil {
			return err
		}
		if !owned {
			return settingsapp.ErrUsageTaskNotFound
		}
		if err := tx.Exec(`INSERT INTO ai_llm_record_epoch(tenant_id,owner_user_id,task_id,generation) VALUES(?::uuid,?,?,1) ON CONFLICT(tenant_id,owner_user_id,task_id) DO UPDATE SET generation=ai_llm_record_epoch.generation+1`, assets.DefaultTenantID, user, task).Error; err != nil {
			return err
		}
		result := tx.Exec(`DELETE FROM ai_llm_record WHERE tenant_id=?::uuid AND owner_user_id=? AND task_id=?`, assets.DefaultTenantID, user, task)
		count = result.RowsAffected
		return result.Error
	})
	return count, err
}
func (s *LLMUsageStore) ListLLMRecordingTasks(ctx context.Context, user int, query string) (settingsapp.LLMRecordingTasks, error) {
	out := settingsapp.LLMRecordingTasks{Items: []settingsapp.LLMRecordingTask{}}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCatalogMember(ctx, tx, user); err != nil {
			return err
		}
		rows, err := tx.Raw(`SELECT q.task_id::text,q.task_name,COUNT(*) FROM (SELECT r.task_id,(SELECT t.name FROM ai_task t WHERE t.id=r.task_id AND t.tenant_id=r.tenant_id AND t.owner_user_id=r.owner_user_id) AS task_name`+visibleLLMRecording+` AND r.task_id IS NOT NULL) q WHERE (?='' OR strpos(lower(q.task_name),lower(?))>0) GROUP BY q.task_id,q.task_name ORDER BY lower(q.task_name),q.task_id LIMIT 101`, assets.DefaultTenantID, user, query, query).Rows()
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item settingsapp.LLMRecordingTask
			if err = rows.Scan(&item.ID, &item.Name, &item.Count); err != nil {
				return err
			}
			if len(out.Items) == 100 {
				out.HasMore = true
				break
			}
			out.Items = append(out.Items, item)
		}
		return rows.Err()
	})
	return out, err
}
func (s *LLMUsageStore) RecordLLMUsage(ctx context.Context, item airuntime.LLMUsageRecord) error {
	if s == nil || s.db == nil || item.UserID <= 0 || item.Status != "ok" && item.Status != "error" && item.Status != "interrupted" {
		return errors.New("invalid model usage")
	}
	for _, value := range []string{item.ProfileID, item.ConversationID, item.ExecutionID} {
		if value != "" {
			if id, err := uuid.Parse(value); err != nil || id.String() != value {
				return errors.New("invalid model usage identity")
			}
		}
	}
	return s.db.WithContext(ctx).Exec(`INSERT INTO ai_llm_usage(tenant_id,owner_user_id,task_id,exploration_id,conversation_id,execution_id,worker,model,profile_id,profile_name,duration_ms,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,status)
VALUES(?::uuid,?,COALESCE(NULLIF(?,0),(SELECT j.source_task_id FROM ai_agent_trigger_job j WHERE j.tenant_id=?::uuid AND j.owner_user_id=? AND j.conversation_id=NULLIF(?,'')::uuid AND j.run_id=NULLIF(?,'')::uuid LIMIT 1)),
COALESCE(NULLIF(?,0),(SELECT source.exploration_id FROM ai_task source WHERE source.id=NULLIF(?,0) AND source.tenant_id=?::uuid AND source.owner_user_id=?),(SELECT source.exploration_id FROM ai_agent_trigger_job j JOIN ai_task source ON source.id=j.source_task_id AND source.tenant_id=j.tenant_id AND source.owner_user_id=j.owner_user_id WHERE j.tenant_id=?::uuid AND j.owner_user_id=? AND j.conversation_id=NULLIF(?,'')::uuid AND j.run_id=NULLIF(?,'')::uuid LIMIT 1)),
NULLIF(?,'')::uuid,NULLIF(?,'')::uuid,?,?,NULLIF(?,'')::uuid,?,?,?,?,?,?,?)`,
		assets.DefaultTenantID, item.UserID, item.TaskID, assets.DefaultTenantID, item.UserID, item.ConversationID, item.ExecutionID,
		item.ExplorationID, item.TaskID, assets.DefaultTenantID, item.UserID, assets.DefaultTenantID, item.UserID, item.ConversationID, item.ExecutionID,
		item.ConversationID, item.ExecutionID, item.Worker, item.Model, item.ProfileID, item.ProfileName, max(int64(item.Duration.Milliseconds()), 0), max(item.Usage.InputTokens, 0), max(item.Usage.OutputTokens, 0), max(item.Usage.CacheReadTokens, 0), max(item.Usage.CacheWriteTokens, 0), item.Status).Error
}

const usageSums = `COUNT(*),COUNT(DISTINCT task_id),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(cache_read_tokens),0),COALESCE(SUM(cache_write_tokens),0)`

func (s *LLMUsageStore) UsageSummary(ctx context.Context, user, days int) (settingsapp.LLMUsageSummary, error) {
	out := settingsapp.LLMUsageSummary{Profiles: []settingsapp.LLMUsageProfile{}, Daily: []settingsapp.LLMUsageDay{}, Days: days}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCatalogMember(ctx, tx, user); err != nil {
			return err
		}
		totals := &out.Totals
		if err := tx.Raw(`SELECT `+usageSums+` FROM ai_llm_usage WHERE tenant_id=?::uuid AND owner_user_id=?`, assets.DefaultTenantID, user).Row().Scan(&totals.Calls, &totals.Tasks, &totals.InputTokens, &totals.OutputTokens, &totals.CacheReadTokens, &totals.CacheWriteTokens); err != nil {
			return err
		}
		rows, err := tx.Raw(`SELECT COALESCE(profile_id::text,''),profile_name,`+usageSums+` FROM ai_llm_usage WHERE tenant_id=?::uuid AND owner_user_id=? GROUP BY profile_id,profile_name ORDER BY SUM(input_tokens)+SUM(output_tokens) DESC,profile_name`, assets.DefaultTenantID, user).Rows()
		if err != nil {
			return err
		}
		for rows.Next() {
			var item settingsapp.LLMUsageProfile
			if err = rows.Scan(&item.ProfileID, &item.ProfileName, &item.Calls, &item.Tasks, &item.InputTokens, &item.OutputTokens, &item.CacheReadTokens, &item.CacheWriteTokens); err != nil {
				rows.Close()
				return err
			}
			out.Profiles = append(out.Profiles, item)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		rows, err = tx.Raw(`SELECT to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD'),COALESCE(profile_id::text,''),profile_name,`+usageSums+` FROM ai_llm_usage WHERE tenant_id=?::uuid AND owner_user_id=? AND created_at>=(((clock_timestamp() AT TIME ZONE 'UTC')::date-(?::integer-1))::timestamp AT TIME ZONE 'UTC') GROUP BY 1,profile_id,profile_name ORDER BY 1,profile_name`, assets.DefaultTenantID, user, days).Rows()
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item settingsapp.LLMUsageDay
			if err = rows.Scan(&item.Date, &item.ProfileID, &item.ProfileName, &item.Calls, &item.Tasks, &item.InputTokens, &item.OutputTokens, &item.CacheReadTokens, &item.CacheWriteTokens); err != nil {
				return err
			}
			out.Daily = append(out.Daily, item)
		}
		return rows.Err()
	})
	return out, err
}

func (s *LLMUsageStore) UsageByTaskModel(ctx context.Context, user int, task int64) ([]settingsapp.LLMModelUsage, error) {
	items := []settingsapp.LLMModelUsage{}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCatalogMember(ctx, tx, user); err != nil {
			return err
		}
		var exists bool
		if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_task t WHERE t.id=? AND t.tenant_id=?::uuid AND t.owner_user_id=? AND t.deleted_at IS NULL AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target x WHERE x.id=t.target_id AND x.deleted_at IS NULL)))`, task, assets.DefaultTenantID, user).Scan(&exists).Error; err != nil {
			return err
		}
		if !exists {
			return settingsapp.ErrUsageTaskNotFound
		}
		rows, err := tx.Raw(`SELECT COALESCE(NULLIF(model,''),'(unknown)'),`+usageSums+` FROM ai_llm_usage WHERE tenant_id=?::uuid AND owner_user_id=? AND task_id=? GROUP BY model ORDER BY SUM(input_tokens)+SUM(output_tokens) DESC,model`, assets.DefaultTenantID, user, task).Rows()
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item settingsapp.LLMModelUsage
			if err = rows.Scan(&item.Model, &item.Calls, &item.Tasks, &item.InputTokens, &item.OutputTokens, &item.CacheReadTokens, &item.CacheWriteTokens); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

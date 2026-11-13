package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/lib/pq"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

// All calendar configuration, manual overrides and reconciliation share this
// transaction-scoped lock. It prevents replicas from applying stale window unions.
const calendarLock int64 = 0x5449414e4149

type ScheduleStore struct{ db *gorm.DB }

func NewScheduleStore(db *gorm.DB) *ScheduleStore { return &ScheduleStore{db: db} }

func acquireCalendarLock(tx *gorm.DB, wait bool) (bool, error) {
	if wait {
		return true, tx.Exec(`SELECT pg_advisory_xact_lock(?)`, calendarLock).Error
	}
	var acquired bool
	err := tx.Raw(`SELECT pg_try_advisory_xact_lock(?)`, calendarLock).Scan(&acquired).Error
	return acquired, err
}
func scheduleWriteRole(tx *gorm.DB, user int) error {
	var role string
	err := tx.Raw(`SELECT m.role FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
 WHERE m.tenant_id=? AND m.user_id=? AND t.status='active' AND u.is_active=TRUE FOR SHARE OF m,u`, assetapp.DefaultTenantID, user).Row().Scan(&role)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && role != "admin" && role != "analyst") {
		return assetapp.ErrAssetWriteForbidden
	}
	return err
}

func loadSchedules(tx *gorm.DB, user int, id int64) ([]aiapp.ScheduleRecord, error) {
	rows, err := tx.Raw(`SELECT s.id,s.owner_user_id,s.tenant_id::text,s.name,s.remark,s.enabled,s.timezone_mode,s.schedule_type,
 COALESCE(to_char(s.run_date,'YYYY-MM-DD'),''),COALESCE(to_char(s.end_date,'YYYY-MM-DD'),''),s.weekdays::text,
		to_char(s.start_time,'HH24:MI:SS'),COALESCE(to_char(s.end_time,'HH24:MI:SS'),''),s.start_immediately,s.status,s.last_window_key,
		s.last_transition_at,s.last_error,s.created_at,s.updated_at,run_stats.run_count,run_stats.last_run_at
 FROM ai_schedule s JOIN tenant_membership m ON m.tenant_id=s.tenant_id AND m.user_id=s.owner_user_id
 JOIN tenant tenant ON tenant.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
		LEFT JOIN LATERAL (SELECT COUNT(*) run_count,MAX(h.created_at) last_run_at FROM ai_schedule_history h WHERE h.schedule_id=s.id AND h.task_id IS NULL AND h.action='running') run_stats ON TRUE
 WHERE s.tenant_id=? AND (?=0 OR s.owner_user_id=?) AND (?=0 OR s.id=?) AND tenant.status='active' AND u.is_active=TRUE ORDER BY s.id`,
		assetapp.DefaultTenantID, user, user, id, id).Rows()
	if err != nil {
		return nil, err
	}
	items := []aiapp.ScheduleRecord{}
	for rows.Next() {
		var record aiapp.ScheduleRecord
		var weekdays string
		c := &record.Item
		if err := rows.Scan(&c.ID, &record.OwnerUserID, &record.TenantID, &c.Name, &c.Remark, &c.Enabled, &c.TimezoneMode, &c.ScheduleType, &c.RunDate, &c.EndDate, &weekdays,
			&c.StartTime, &c.EndTime, &c.StartImmediately, &c.Status, &c.LastWindowKey, &c.LastTransitionAt, &c.LastError, &c.CreatedAt, &c.UpdatedAt, &record.RunCount, &record.LastRunAt); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(weekdays), &c.Weekdays); err != nil {
			rows.Close()
			return nil, err
		}
		c.TaskIDs = []int64{}
		items = append(items, record)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return items, nil
	}
	ids := make([]int64, len(items))
	positions := map[int64]int{}
	for i := range items {
		ids[i] = items[i].Item.ID
		positions[ids[i]] = i
	}
	var links []struct {
		ScheduleID int64
		TaskID     int64
	}
	if err := tx.Raw(`SELECT l.schedule_id,l.task_id FROM ai_schedule_task l JOIN ai_schedule s ON s.id=l.schedule_id
 JOIN ai_task t ON t.id=l.task_id AND t.tenant_id=s.tenant_id AND t.owner_user_id=s.owner_user_id AND t.deleted_at IS NULL
 WHERE l.schedule_id=ANY(?::bigint[]) ORDER BY l.schedule_id,l.task_id`, pq.Int64Array(ids)).Scan(&links).Error; err != nil {
		return nil, err
	}
	for _, link := range links {
		idx := positions[link.ScheduleID]
		items[idx].Item.TaskIDs = append(items[idx].Item.TaskIDs, link.TaskID)
	}
	return items, nil
}
func (s *ScheduleStore) ListSchedules(ctx context.Context, user int) ([]aiapp.ScheduleRecord, error) {
	return loadSchedules(s.db.WithContext(ctx), user, 0)
}
func (s *ScheduleStore) GetSchedule(ctx context.Context, user int, id int64) (*aiapp.ScheduleRecord, error) {
	items, err := loadSchedules(s.db.WithContext(ctx), user, id)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, aiapp.ErrScheduleNotFound
	}
	return &items[0], nil
}
func (s *ScheduleStore) SaveSchedule(ctx context.Context, id int64, input aiapp.ScheduleInput) (*aiapp.ScheduleRecord, error) {
	creating := id == 0
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := acquireCalendarLock(tx, true); err != nil {
			return err
		}
		if err := scheduleWriteRole(tx, input.UserID); err != nil {
			return err
		}
		if id > 0 {
			items, err := loadSchedules(tx, input.UserID, id)
			if err != nil {
				return err
			}
			if len(items) == 0 {
				return aiapp.ErrScheduleNotFound
			}
			if input.PreserveRemark {
				input.Remark = items[0].Item.Remark
			}
			if input.PreserveEnabled {
				input.Calendar.Enabled = items[0].Item.Enabled
			}
		}
		ids := append([]int64(nil), input.Calendar.TaskIDs...)
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, taskID := range ids {
			var found int64
			err := tx.Raw(`SELECT id FROM ai_task WHERE id=? AND tenant_id=? AND owner_user_id=? AND deleted_at IS NULL
    AND (original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE target.id=ai_task.target_id AND target.deleted_at IS NULL)) FOR UPDATE`, taskID, assetapp.DefaultTenantID, input.UserID).Row().Scan(&found)
			if errors.Is(err, sql.ErrNoRows) {
				return aiapp.ErrTaskNotFound
			}
			if err != nil {
				return err
			}
		}
		c := input.Calendar
		days, _ := json.Marshal(c.Weekdays)
		if string(days) == "null" {
			days = []byte("[]")
		}
		if id == 0 {
			err := tx.Raw(`INSERT INTO ai_schedule(tenant_id,owner_user_id,name,remark,enabled,timezone_mode,schedule_type,run_date,end_date,weekdays,start_time,end_time,start_immediately,status)
    VALUES(?,?,?,?,?,?,?,NULLIF(?,'')::date,NULLIF(?,'')::date,?::jsonb,?::time,NULLIF(?,'')::time,?,CASE WHEN ? THEN 'waiting' ELSE 'paused' END) RETURNING id`,
				assetapp.DefaultTenantID, input.UserID, c.Name, input.Remark, c.Enabled, c.TimezoneMode, c.ScheduleType, c.RunDate, c.EndDate, string(days), c.StartTime, c.EndTime, c.StartImmediately, c.Enabled).Row().Scan(&id)
			if err != nil {
				return err
			}
		} else {
			if err := tx.Exec(`UPDATE ai_schedule SET name=?,remark=?,enabled=?,timezone_mode=?,schedule_type=?,run_date=NULLIF(?,'')::date,end_date=NULLIF(?,'')::date,
    weekdays=?::jsonb,start_time=?::time,end_time=NULLIF(?,'')::time,start_immediately=?,status=CASE WHEN ? THEN 'waiting' ELSE 'paused' END,
    last_window_key='',last_error='',updated_at=clock_timestamp() WHERE id=?`, c.Name, input.Remark, c.Enabled, c.TimezoneMode, c.ScheduleType, c.RunDate, c.EndDate, string(days), c.StartTime, c.EndTime, c.StartImmediately, c.Enabled, id).Error; err != nil {
				return err
			}
			if err := tx.Exec(`DELETE FROM ai_schedule_task WHERE schedule_id=?`, id).Error; err != nil {
				return err
			}
		}
		for _, taskID := range ids {
			if err := tx.Exec(`INSERT INTO ai_schedule_task(schedule_id,task_id) VALUES(?,?)`, id, taskID).Error; err != nil {
				return err
			}
		}
		// Starting a newly-created schedule never overrides a human pause.
		if input.Calendar.StartImmediately && input.Calendar.Enabled && creating {
			if _, err := runScheduleTasksTx(tx, input.UserID, id, false, time.Now()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetSchedule(ctx, input.UserID, id)
}

func (s *ScheduleStore) UpdateScheduleMetadata(ctx context.Context, user int, id int64, name, remark *string) (*aiapp.ScheduleRecord, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := acquireCalendarLock(tx, true); err != nil {
			return err
		}
		if err := scheduleWriteRole(tx, user); err != nil {
			return err
		}
		result := tx.Exec(`UPDATE ai_schedule SET name=COALESCE(?,name),remark=COALESCE(?,remark) WHERE tenant_id=? AND owner_user_id=? AND id=?`, name, remark, assetapp.DefaultTenantID, user, id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return aiapp.ErrScheduleNotFound
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetSchedule(ctx, user, id)
}
func (s *ScheduleStore) DeleteSchedule(ctx context.Context, user int, id int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := acquireCalendarLock(tx, true); err != nil {
			return err
		}
		if err := scheduleWriteRole(tx, user); err != nil {
			return err
		}
		q := tx.Exec(`DELETE FROM ai_schedule WHERE id=? AND tenant_id=? AND owner_user_id=?`, id, assetapp.DefaultTenantID, user)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected == 0 {
			return aiapp.ErrScheduleNotFound
		}
		return nil
	})
}
func (s *ScheduleStore) SetScheduleEnabled(ctx context.Context, user int, id int64, enabled bool) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := acquireCalendarLock(tx, true); err != nil {
			return err
		}
		if err := scheduleWriteRole(tx, user); err != nil {
			return err
		}
		q := tx.Exec(`UPDATE ai_schedule SET enabled=?,status=CASE WHEN ? THEN 'waiting' ELSE 'paused' END,last_error='',last_window_key='',updated_at=clock_timestamp()
   WHERE id=? AND tenant_id=? AND owner_user_id=?`, enabled, enabled, id, assetapp.DefaultTenantID, user)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected == 0 {
			return aiapp.ErrScheduleNotFound
		}
		if enabled {
			items, err := loadSchedules(tx, user, id)
			if err != nil {
				return err
			}
			if len(items) > 0 && items[0].Item.StartImmediately {
				if _, err := runScheduleTasksTx(tx, user, id, false, time.Now()); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
func (s *ScheduleStore) ScheduleHistory(ctx context.Context, user int, id int64) ([]aiapp.ScheduleHistory, error) {
	if _, err := s.GetSchedule(ctx, user, id); err != nil {
		return nil, err
	}
	out := []aiapp.ScheduleHistory{}
	err := s.db.WithContext(ctx).Raw(`SELECT h.id,h.task_id,h.action,h.success,h.message,h.created_at FROM ai_schedule_history h
 JOIN ai_schedule s ON s.id=h.schedule_id WHERE s.id=? AND s.tenant_id=? AND s.owner_user_id=? ORDER BY h.id DESC LIMIT 100`, id, assetapp.DefaultTenantID, user).Scan(&out).Error
	return out, err
}

// manualScheduleEnd reproduces ARTEX's task-specific override until the next
// enabled window ends. Open-ended or disabled windows do not impose a deadline.
func manualScheduleEnd(tx *gorm.DB, user int, taskID int64, now time.Time) (*time.Time, error) {
	items, err := loadSchedules(tx, user, 0)
	if err != nil {
		return nil, err
	}
	var until *time.Time
	for _, record := range items {
		item := record.Item
		bound := false
		for _, id := range item.TaskIDs {
			if id == taskID {
				bound = true
				break
			}
		}
		if !bound {
			continue
		}
		if until == nil {
			unbounded := time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
			until = &unbounded
		}
		if item.Enabled && item.Status != "completed" {
			w := aiapp.EvaluateScheduleWindow(&item, now)
			if w.NextEnd != nil && w.NextEnd.After(now) && w.NextEnd.Before(*until) {
				end := *w.NextEnd
				until = &end
			}
		}
	}
	return until, nil
}

func runScheduleTasksTx(tx *gorm.DB, user int, id int64, force bool, now time.Time) (aiapp.ScheduleRunResult, error) {
	result := aiapp.ScheduleRunResult{Errors: []string{}}
	items, err := loadSchedules(tx, user, id)
	if err != nil {
		return result, err
	}
	if len(items) == 0 {
		return result, aiapp.ErrScheduleNotFound
	}
	for _, taskID := range items[0].Item.TaskIDs {
		var state struct {
			Status           string
			Queued           bool
			PausedBySchedule bool
		}
		q := tx.Raw(`SELECT status,queued,paused_by_schedule FROM ai_task WHERE id=? AND tenant_id=? AND owner_user_id=? AND deleted_at IS NULL FOR UPDATE`, taskID, assetapp.DefaultTenantID, user).Scan(&state)
		if q.Error != nil {
			return result, q.Error
		}
		if q.RowsAffected == 0 {
			result.Errors = append(result.Errors, fmt.Sprintf("任务 #%d 不存在或不可访问", taskID))
			continue
		}
		if state.Status == "running" || state.Queued {
			result.Started++
			continue
		}
		if state.Status != "paused" && state.Status != "created" {
			message := fmt.Sprintf("任务 #%d 已结束，无法立即运行", taskID)
			result.Errors = append(result.Errors, message)
			if err := tx.Exec(`INSERT INTO ai_schedule_history(schedule_id,task_id,action,success,message) VALUES(?,?,?,FALSE,?)`, id, taskID, "resume", message).Error; err != nil {
				return result, err
			}
			continue
		}
		if state.Status == "paused" && !state.PausedBySchedule && !force {
			continue
		}
		until, err := manualScheduleEnd(tx, user, taskID, now)
		if err != nil {
			return result, err
		}
		updated := tx.Exec(`UPDATE ai_task SET status='created',queued=TRUE,paused_by_schedule=FALSE,schedule_manual_until=?,error_message='',updated_at=clock_timestamp()
   WHERE id=? AND (original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=ai_task.target_id AND deleted_at IS NULL))`, until, taskID)
		if updated.Error != nil {
			return result, updated.Error
		}
		if updated.RowsAffected == 0 {
			message := fmt.Sprintf("任务 #%d 的关联目标不可用", taskID)
			result.Errors = append(result.Errors, message)
			if err := tx.Exec(`INSERT INTO ai_schedule_history(schedule_id,task_id,action,success,message) VALUES(?,?,?,FALSE,?)`, id, taskID, "resume", message).Error; err != nil {
				return result, err
			}
			continue
		}
		if err := tx.Exec(`INSERT INTO ai_schedule_history(schedule_id,task_id,action,message) VALUES(?,?,?,?)`, id, taskID, "resume", "立即运行").Error; err != nil {
			return result, err
		}
		result.Started++
	}
	return result, nil
}
func (s *ScheduleStore) RunScheduleNow(ctx context.Context, user int, id int64) (aiapp.ScheduleRunResult, error) {
	result := aiapp.ScheduleRunResult{Errors: []string{}}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := acquireCalendarLock(tx, true); err != nil {
			return err
		}
		if err := scheduleWriteRole(tx, user); err != nil {
			return err
		}
		var err error
		result, err = runScheduleTasksTx(tx, user, id, true, time.Now())
		if err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE ai_schedule SET status='running',last_window_key='manual',last_transition_at=clock_timestamp(),last_error='' WHERE id=?`, id).Error; err != nil {
			return err
		}
		return tx.Exec(`INSERT INTO ai_schedule_history(schedule_id,action,message) VALUES(?,'running','手动立即运行')`, id).Error
	})
	return result, err
}

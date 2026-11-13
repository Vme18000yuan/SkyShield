package repository

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

type scheduledTaskState struct {
	ID                  int64
	Status              string
	Queued              bool
	PausedBySchedule    bool
	ScheduleManualUntil *time.Time
}

// TickSchedules serializes the entire window-union decision and its task updates.
// Multiple server replicas may call this concurrently, but only one reconciles.
func (s *ScheduleStore) TickSchedules(ctx context.Context, now, startedAt time.Time) (aiapp.ScheduleTickResult, error) {
	result := aiapp.ScheduleTickResult{PausedTaskIDs: []int64{}}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		acquired, err := acquireCalendarLock(tx, false)
		if err != nil || !acquired {
			return err
		}
		items, err := loadSchedules(tx, 0, 0)
		if err != nil {
			return err
		}
		open := map[int64]bool{}
		scheduleForTask := map[int64]int64{}
		windows := map[int64]aiapp.ScheduleWindow{}
		for _, record := range items {
			item := record.Item
			w := aiapp.EvaluateRuntimeScheduleWindow(&item, now, startedAt)
			windows[item.ID] = w
			for _, taskID := range item.TaskIDs {
				if _, ok := scheduleForTask[taskID]; !ok {
					scheduleForTask[taskID] = item.ID
				}
				if w.Active {
					open[taskID] = true
				}
			}
		}
		ids := make([]int64, 0, len(scheduleForTask))
		for id := range scheduleForTask {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		states := []scheduledTaskState{}
		if len(ids) > 0 {
			if err := tx.Raw(`SELECT task.id,task.status,task.queued,task.paused_by_schedule,task.schedule_manual_until
    FROM ai_task task JOIN tenant_membership m ON m.tenant_id=task.tenant_id AND m.user_id=task.owner_user_id
    JOIN auth_user u ON u.id=m.user_id JOIN tenant tenant ON tenant.id=m.tenant_id
    WHERE task.id=ANY(?::bigint[]) AND task.tenant_id=? AND task.deleted_at IS NULL AND m.role IN ('admin','analyst')
    AND u.is_active=TRUE AND tenant.status='active'
    AND (task.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=task.target_id AND deleted_at IS NULL))
    ORDER BY task.id FOR UPDATE OF task`, pq.Int64Array(ids), assetapp.DefaultTenantID).Scan(&states).Error; err != nil {
				return err
			}
		}
		for _, state := range states {
			if state.ScheduleManualUntil != nil && state.ScheduleManualUntil.After(now) {
				open[state.ID] = true
			}
			action := ""
			if open[state.ID] && ((state.Status == "paused" && state.PausedBySchedule) || (state.Status == "created" && !state.Queued)) {
				action = "resume"
			}
			if !open[state.ID] && (state.Status == "running" || state.Status == "created") {
				action = "pause"
			}
			if action == "" {
				continue
			}
			if action == "resume" {
				err = tx.Exec(`UPDATE ai_task SET status='created',queued=TRUE,paused_by_schedule=FALSE,error_message='',updated_at=clock_timestamp() WHERE id=?`, state.ID).Error
			} else {
				err = tx.Exec(`UPDATE ai_task SET status='paused',queued=FALSE,paused_by_schedule=TRUE,lease_until=NULL,updated_at=clock_timestamp() WHERE id=?`, state.ID).Error
				result.PausedTaskIDs = append(result.PausedTaskIDs, state.ID)
			}
			if err != nil {
				return err
			}
			if err = tx.Exec(`INSERT INTO ai_schedule_history(schedule_id,task_id,action,message) VALUES(?,?,?,'计划窗口')`, scheduleForTask[state.ID], state.ID, action).Error; err != nil {
				return err
			}
			result.Changed = true
		}
		for _, record := range items {
			item := record.Item
			w := windows[item.ID]
			status := "outside"
			switch {
			case !item.Enabled:
				status = "paused"
			case w.Active:
				status = "running"
			case w.Complete:
				status = "completed"
			case item.LastWindowKey == "":
				status = "waiting"
			}
			key := item.LastWindowKey
			if w.Active || strings.HasPrefix(w.Key, "missed:") {
				key = w.Key
			}
			if status != item.Status || key != item.LastWindowKey {
				if err := tx.Exec(`UPDATE ai_schedule SET status=?,last_window_key=?,last_transition_at=clock_timestamp(),last_error='' WHERE id=?`, status, key, item.ID).Error; err != nil {
					return err
				}
				if err := tx.Exec(`INSERT INTO ai_schedule_history(schedule_id,action,message) VALUES(?,?,'计划状态变更')`, item.ID, status).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return aiapp.ScheduleTickResult{}, err
	}
	return result, nil
}

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

func (s *TaskStore) ListTaskGoals(ctx context.Context, user int, taskID int64) ([]aiapp.TaskGoal, error) {
	if _, err := s.GetTask(ctx, user, taskID); err != nil {
		return nil, err
	}
	items := []aiapp.TaskGoal{}
	err := s.db.WithContext(ctx).Raw(`SELECT n.id,COALESCE(n.payload->>'text','') AS text,COALESCE(n.payload->>'vulnclass','') AS vuln_class,n.state,COALESCE(n.origin,'') AS origin,n.created_at FROM tianqiong_ai.exploration_nodes n JOIN ai_task t ON t.exploration_id=n.exploration_id JOIN tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id JOIN tenant tenant ON tenant.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE t.tenant_id=? AND t.owner_user_id=? AND t.id=? AND t.deleted_at IS NULL AND tenant.status='active' AND u.is_active=TRUE AND n.kind='goal' ORDER BY n.id LIMIT 10000`, assets.DefaultTenantID, user, taskID).Scan(&items).Error
	return items, err
}

type humanGoalScope struct {
	ExplorationID int64
	Status        string
	Queued        bool
	LeaseUntil    *time.Time
	DeadlineAt    *time.Time
}

func lockHumanGoalScope(tx *gorm.DB, user int, taskID int64) (humanGoalScope, error) {
	var scope humanGoalScope
	if _, err := acquireCalendarLock(tx, true); err != nil {
		return scope, err
	}
	if err := scheduleWriteRole(tx, user); err != nil {
		return scope, err
	}
	err := tx.Raw(`SELECT exploration_id,status,queued,lease_until,deadline_at FROM ai_task WHERE tenant_id=? AND owner_user_id=? AND id=? AND deleted_at IS NULL AND (original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=ai_task.target_id AND deleted_at IS NULL)) FOR UPDATE`, assets.DefaultTenantID, user, taskID).Row().Scan(&scope.ExplorationID, &scope.Status, &scope.Queued, &scope.LeaseUntil, &scope.DeadlineAt)
	if errors.Is(err, sql.ErrNoRows) {
		return scope, aiapp.ErrTaskNotFound
	}
	if err != nil {
		return scope, err
	}
	err = tx.Exec(`SELECT set_config('tianqiong.task_id',?,TRUE),set_config('tianqiong.owner_user_id',?,TRUE),set_config('tianqiong.execution_mode','human_goal',TRUE)`, strconv.FormatInt(taskID, 10), strconv.Itoa(user)).Error
	return scope, err
}
func setHumanGoalID(tx *gorm.DB, id int64) error {
	return tx.Exec(`SELECT set_config('tianqiong.goal_id',?,TRUE)`, strconv.FormatInt(id, 10)).Error
}
func insertHumanGoalEvent(tx *gorm.DB, user int, taskID int64, kind string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return tx.Exec(`INSERT INTO ai_task_human_event(tenant_id,owner_user_id,task_id,kind,payload) VALUES(?,?,?,?,?::jsonb)`, assets.DefaultTenantID, user, taskID, kind, string(raw)).Error
}
func reviveForHumanGoal(tx *gorm.DB, user int, taskID int64, scope humanGoalScope) error {
	if scope.Queued || scope.Status == "running" && scope.LeaseUntil != nil && scope.LeaseUntil.After(time.Now()) {
		return nil
	}
	manualUntil, err := manualScheduleEnd(tx, user, taskID, time.Now())
	if err != nil {
		return err
	}
	return tx.Exec(`UPDATE ai_task SET status='created',queued=TRUE,completed_at=NULL,error_message='',paused_by_schedule=FALSE,schedule_manual_until=?,lease_until=NULL,first_run_at=CASE WHEN status='timeout' THEN NULL ELSE first_run_at END,deadline_at=CASE WHEN status='timeout' THEN NULL ELSE deadline_at END,updated_at=CURRENT_TIMESTAMP WHERE id=?`, manualUntil, taskID).Error
}
func (s *TaskStore) SaveTaskGoal(ctx context.Context, user int, taskID, id int64, text, vulnClass string) (*aiapp.TaskGoal, error) {
	var result aiapp.TaskGoal
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		scope, err := lockHumanGoalScope(tx, user, taskID)
		if err != nil {
			return err
		}
		payload := map[string]any{"text": text}
		if vulnClass != "" {
			payload["vulnclass"] = vulnClass
		}
		raw, _ := json.Marshal(payload)
		var previous string
		if id == 0 {
			if err = tx.Raw(`INSERT INTO tianqiong_ai.exploration_nodes(exploration_id,kind,payload,priority,state,origin) VALUES(?,'goal',?::jsonb,0,'open','human') RETURNING id`, scope.ExplorationID, string(raw)).Row().Scan(&id); err != nil {
				return err
			}
			if err = setHumanGoalID(tx, id); err != nil {
				return err
			}
			if err = tx.Exec(`INSERT INTO tianqiong_ai.exploration_edges(exploration_id,src_id,rel,dst_id) SELECT ? ,id,'spawns',? FROM tianqiong_ai.exploration_nodes WHERE exploration_id=? AND kind='fact' AND state='origin' ORDER BY id LIMIT 1 ON CONFLICT DO NOTHING`, scope.ExplorationID, id, scope.ExplorationID).Error; err != nil {
				return err
			}
			if err = insertHumanGoalEvent(tx, user, taskID, "goal", map[string]any{"goals": []string{text}}); err != nil {
				return err
			}
		} else {
			err = tx.Raw(`SELECT COALESCE(payload->>'text','') FROM tianqiong_ai.exploration_nodes WHERE id=? AND exploration_id=? AND kind='goal' FOR UPDATE`, id, scope.ExplorationID).Row().Scan(&previous)
			if errors.Is(err, sql.ErrNoRows) {
				return aiapp.ErrTaskNotFound
			}
			if err != nil {
				return err
			}
			if err = setHumanGoalID(tx, id); err != nil {
				return err
			}
			if err = tx.Exec(`UPDATE tianqiong_ai.exploration_nodes SET payload=?::jsonb WHERE id=? AND exploration_id=? AND kind='goal'`, string(raw), id, scope.ExplorationID).Error; err != nil {
				return err
			}
			if err = insertHumanGoalEvent(tx, user, taskID, "goal_edited", map[string]any{"old_goal": previous, "new_goal": text}); err != nil {
				return err
			}
		}
		if err = reviveForHumanGoal(tx, user, taskID, scope); err != nil {
			return err
		}
		return tx.Raw(`SELECT id,COALESCE(payload->>'text','') AS text,COALESCE(payload->>'vulnclass','') AS vuln_class,state,COALESCE(origin,'') AS origin,created_at FROM tianqiong_ai.exploration_nodes WHERE id=?`, id).Row().Scan(&result.ID, &result.Text, &result.VulnClass, &result.State, &result.Origin, &result.CreatedAt)
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}
func (s *TaskStore) DeleteTaskGoal(ctx context.Context, user int, taskID, id int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		scope, err := lockHumanGoalScope(tx, user, taskID)
		if err != nil {
			return err
		}
		var text string
		err = tx.Raw(`SELECT COALESCE(payload->>'text','') FROM tianqiong_ai.exploration_nodes WHERE id=? AND exploration_id=? AND kind='goal' FOR UPDATE`, id, scope.ExplorationID).Row().Scan(&text)
		if errors.Is(err, sql.ErrNoRows) {
			return aiapp.ErrTaskNotFound
		}
		if err != nil {
			return err
		}
		if err = setHumanGoalID(tx, id); err != nil {
			return err
		}
		if err = tx.Exec(`DELETE FROM tianqiong_ai.exploration_nodes WHERE id=? AND exploration_id=? AND kind='goal'`, id, scope.ExplorationID).Error; err != nil {
			return err
		}
		return insertHumanGoalEvent(tx, user, taskID, "goal_deleted", map[string]any{"detail": text})
	})
}

var _ aiapp.TaskGoalStore = (*TaskStore)(nil)

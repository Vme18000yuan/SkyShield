package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lib/pq"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

type TaskStore struct{ db *gorm.DB }

func NewTaskStore(db *gorm.DB) *TaskStore { return &TaskStore{db: db} }

const taskColumns = `t.id,t.tenant_id::text,t.owner_user_id,t.name,t.description,t.goal,t.remark,t.category_id,COALESCE((SELECT c.name FROM ai_task_category c WHERE c.id=t.category_id AND c.tenant_id=t.tenant_id AND c.owner_user_id=t.owner_user_id),'') AS category_name,t.pinned_at,t.exploration_id,t.target_id,t.status,t.queued,t.worker_count,t.max_turns,t.timeout_seconds,
	t.active_run::text,t.lease_owner::text,t.lease_epoch,t.lease_until,t.first_run_at,t.deadline_at,t.error_message,t.created_at,t.updated_at,t.completed_at,
	t.active_llm_profile_id::text AS active_llm_profile_id,t.llm_chain_revision,t.plan_heartbeat_seconds,t.bootstrap_complete,t.coverage_enabled`
const taskOwnerJoin = ` FROM ai_task t JOIN tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id
 JOIN tenant tenant ON tenant.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id `
const taskOwnerWhere = ` t.tenant_id=? AND t.owner_user_id=? AND t.deleted_at IS NULL AND tenant.status='active' AND u.is_active=TRUE `

func (s *TaskStore) CreateTask(ctx context.Context, input aiapp.CreateTaskInput) (*aiapp.Task, error) {
	var id int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var role string
		if err := tx.Raw(`SELECT m.role FROM tenant_membership m JOIN auth_user u ON u.id=m.user_id JOIN tenant t ON t.id=m.tenant_id
			WHERE m.tenant_id=? AND m.user_id=? AND u.is_active=TRUE AND t.status='active' FOR SHARE OF m,u`, assetapp.DefaultTenantID, input.UserID).Row().Scan(&role); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return aiapp.ErrTaskNotFound
			}
			return err
		}
		if role != "admin" && role != "analyst" {
			return assetapp.ErrAssetWriteForbidden
		}
		if input.ConversationRunID != "" {
			var activeRun string
			if err := tx.Raw(`SELECT r.id::text FROM ai_run r WHERE r.tenant_id=? AND r.owner_user_id=? AND r.id=? AND r.status='running' AND r.lease_until>clock_timestamp() FOR UPDATE`, assetapp.DefaultTenantID, input.UserID, input.ConversationRunID).Row().Scan(&activeRun); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return aiapp.ErrRunLeaseLost
				}
				return err
			}
			var existing struct {
				TaskID      sql.NullInt64
				RequestHash string
				Operation   string
			}
			prior := tx.Raw(`SELECT task_id,request_hash,operation FROM ai_auto_task_action WHERE tenant_id=? AND run_id=? AND tool_use_id=?`, assetapp.DefaultTenantID, input.ConversationRunID, input.ToolUseID).Scan(&existing)
			if prior.Error != nil {
				return prior.Error
			}
			if prior.RowsAffected > 0 {
				if existing.RequestHash != input.ToolInputHash || existing.Operation != "spawn" || !existing.TaskID.Valid {
					return aiapp.ErrInvalidInput
				}
				id = existing.TaskID.Int64
				return nil
			}
		}
		if err := lockCatalogCategory(tx, input.UserID, input.CategoryID); err != nil {
			return err
		}
		sources := append([]int64{}, input.SourceTaskIDs...)
		sort.Slice(sources, func(i, j int) bool { return sources[i] < sources[j] })
		for _, sourceID := range sources {
			var present int
			err := tx.Raw(`SELECT 1 FROM ai_task WHERE id=? AND tenant_id=? AND owner_user_id=? AND deleted_at IS NULL AND (original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=ai_task.target_id AND deleted_at IS NULL)) FOR SHARE`, sourceID, assetapp.DefaultTenantID, input.UserID).Row().Scan(&present)
			if errors.Is(err, sql.ErrNoRows) {
				return aiapp.ErrTaskNotFound
			}
			if err != nil {
				return err
			}
		}
		if len(input.LLMProfileIDs) > 0 {
			if err := lockTaskProfileTenant(tx); err != nil {
				return err
			}
		}
		if input.TargetID > 0 {
			var target int
			if err := tx.Raw(`SELECT id FROM target WHERE id=? AND deleted_at IS NULL FOR SHARE`, input.TargetID).Row().Scan(&target); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return assetapp.ErrAssetTargetNotFound
				}
				return err
			}
		}
		var exploration int64
		if err := tx.Raw(`INSERT INTO tianqiong_ai.explorations(description,goal) VALUES(?,?) RETURNING id`, input.Description, input.Goal).Row().Scan(&exploration); err != nil {
			return err
		}
		setup := uuid.NewString()
		coverage := true
		if input.CoverageEnabled != nil {
			coverage = *input.CoverageEnabled
		}
		if err := tx.Raw(`INSERT INTO ai_task(tenant_id,owner_user_id,name,description,goal,remark,category_id,exploration_id,target_id,original_target_id,queued,worker_count,max_turns,timeout_seconds,plan_heartbeat_seconds,active_run,coverage_enabled)
			VALUES(?,?,?,?,?,?,?,?,NULLIF(?,0),NULLIF(?,0),?,?,?,?,?,?,?) RETURNING id`, assetapp.DefaultTenantID, input.UserID, input.Name, input.Description, input.Goal, input.Remark, input.CategoryID, exploration, input.TargetID, input.TargetID, input.StartImmediately, input.WorkerCount, input.MaxTurns, input.TimeoutSeconds, input.PlanHeartbeatSeconds, setup, coverage).Row().Scan(&id); err != nil {
			return err
		}
		for _, rule := range input.InterceptRules {
			if err := tx.Exec(`INSERT INTO ai_asset_intercept_rule(tenant_id,task_id,owner_user_id,enabled,action,kind,pattern,note) VALUES(?,?,?,?,?,?,?,?)`, assetapp.DefaultTenantID, id, input.UserID, rule.Enabled, rule.Action, rule.Kind, rule.Pattern, rule.Note).Error; err != nil {
				return err
			}
		}
		for _, sourceID := range input.SourceTaskIDs {
			if err := tx.Exec(`INSERT INTO ai_task_relation(tenant_id,task_id,source_task_id) VALUES(?,?,?)`, assetapp.DefaultTenantID, id, sourceID).Error; err != nil {
				return err
			}
		}
		if len(input.LLMProfileIDs) > 0 {
			active := input.LLMProfileIDs[0]
			if err := insertTaskLLMChain(tx, id, input.LLMProfileIDs, &active); err != nil {
				return err
			}
		}
		if err := tx.Exec(`SELECT set_config('tianqiong.task_id',?,TRUE),set_config('tianqiong.owner_user_id',?,TRUE),set_config('tianqiong.execution_id',?,TRUE),set_config('tianqiong.execution_mode','setup',TRUE)`, strconv.FormatInt(id, 10), strconv.Itoa(input.UserID), setup).Error; err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"summary": input.Description, "goal": input.Goal})
		if err := tx.Exec(`INSERT INTO tianqiong_ai.exploration_nodes(exploration_id,kind,payload,state,origin) VALUES(?,'begin',?::jsonb,'open','human')`, exploration, string(payload)).Error; err != nil {
			return err
		}
		var origin int64
		if err := tx.Raw(`INSERT INTO tianqiong_ai.exploration_nodes(exploration_id,kind,payload,state,origin) VALUES(?,'fact',?::jsonb,'origin','human') RETURNING id`, exploration, string(payload)).Row().Scan(&origin); err != nil {
			return err
		}
		if input.SeedFirstIntent {
			seed, _ := json.Marshal(map[string]any{"summary": fmt.Sprintf("完成任务目标：%s（任务：%s）", input.Goal, input.Description)})
			var intent int64
			if err := tx.Raw(`INSERT INTO tianqiong_ai.exploration_nodes(exploration_id,kind,payload,priority,state,origin) VALUES(?,'intent',?::jsonb,8,'open','seed') RETURNING id`, exploration, string(seed)).Row().Scan(&intent); err != nil {
				return err
			}
			if err := tx.Exec(`INSERT INTO tianqiong_ai.exploration_edges(exploration_id,src_id,rel,dst_id) VALUES(?,?,'derived_from',?)`, exploration, origin, intent).Error; err != nil {
				return err
			}
		}
		for _, assetID := range input.AssetIDs {
			var exists bool
			if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM asset_entity WHERE tenant_id=? AND id=?)`, assetapp.DefaultTenantID, assetID).Scan(&exists).Error; err != nil {
				return err
			}
			if !exists {
				return assetapp.ErrInvalidAsset
			}
			if err := tx.Exec(`INSERT INTO ai_task_asset(tenant_id,task_id,asset_id) VALUES(?,?,?)`, assetapp.DefaultTenantID, id, assetID).Error; err != nil {
				return err
			}
			if err := tx.Exec(`INSERT INTO tianqiong_ai.exploration_anchors(node_id,asset_id) VALUES(?,?)`, origin, assetID).Error; err != nil {
				var pgErr *pgconn.PgError
				if errors.As(err, &pgErr) && pgErr.Message == "AI task asset is outside the selected target" {
					return assetapp.ErrAssetOutsideTarget
				}
				return err
			}
		}
		if input.ConversationRunID != "" {
			if err := tx.Exec(`INSERT INTO ai_auto_task_action(tenant_id,run_id,tool_use_id,request_hash,task_id,operation) VALUES(?,?,?,?,?,'spawn')`, assetapp.DefaultTenantID, input.ConversationRunID, input.ToolUseID, input.ToolInputHash, id).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetTask(ctx, input.UserID, id)
}

func (s *TaskStore) ListTasks(ctx context.Context, user int, before int64) ([]aiapp.Task, error) {
	return s.ListTaskPage(ctx, user, before, aiapp.TaskListFilter{})
}

func (s *TaskStore) GetTask(ctx context.Context, user int, id int64) (*aiapp.Task, error) {
	var task aiapp.Task
	result := s.db.WithContext(ctx).Raw(`SELECT `+taskColumns+taskOwnerJoin+` WHERE `+taskOwnerWhere+` AND t.id=?`, assetapp.DefaultTenantID, user, id).Scan(&task)
	if result.Error != nil {
		return nil, result.Error
	}
	if task.ID == 0 {
		return nil, aiapp.ErrTaskNotFound
	}
	items := []aiapp.Task{task}
	if err := s.hydrateTaskLLM(ctx, items); err != nil {
		return nil, err
	}
	if err := s.hydrateTaskSources(ctx, items); err != nil {
		return nil, err
	}
	task = items[0]
	return &task, nil
}

func (s *TaskStore) ControlTask(ctx context.Context, user int, id int64, action string, bySchedule bool) (*aiapp.Task, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := acquireCalendarLock(tx, true); err != nil {
			return err
		}
		mutation, guarded := aiapp.AutoTaskMutationFromContext(ctx)
		if guarded {
			if action != "pause" || bySchedule {
				return aiapp.ErrInvalidInput
			}
			var run string
			if err := tx.Raw(`SELECT id::text FROM ai_run WHERE tenant_id=? AND owner_user_id=? AND id=? AND status='running' AND lease_until>clock_timestamp() FOR UPDATE`, assetapp.DefaultTenantID, user, mutation.RunID).Row().Scan(&run); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return aiapp.ErrRunLeaseLost
				}
				return err
			}
			var prior struct {
				TaskID      sql.NullInt64
				RequestHash string
				Operation   string
			}
			query := tx.Raw(`SELECT task_id,request_hash,operation FROM ai_auto_task_action WHERE tenant_id=? AND run_id=? AND tool_use_id=?`, assetapp.DefaultTenantID, mutation.RunID, mutation.ToolUseID).Scan(&prior)
			if query.Error != nil {
				return query.Error
			}
			if query.RowsAffected > 0 {
				if prior.Operation != "pause" || prior.RequestHash != mutation.InputHash || !prior.TaskID.Valid || prior.TaskID.Int64 != id {
					return aiapp.ErrInvalidInput
				}
				if mutation.Replayed != nil {
					*mutation.Replayed = true
				}
				return nil
			}
		}
		var task aiapp.Task
		var role string
		query := tx.Raw(`SELECT `+taskColumns+taskOwnerJoin+` WHERE `+taskOwnerWhere+` AND t.id=? FOR UPDATE OF t`, assetapp.DefaultTenantID, user, id).Scan(&task)
		if query.Error != nil {
			return query.Error
		}
		if task.ID == 0 {
			return aiapp.ErrTaskNotFound
		}
		if err := tx.Raw(`SELECT role FROM tenant_membership WHERE tenant_id=? AND user_id=? FOR SHARE`, assetapp.DefaultTenantID, user).Row().Scan(&role); err != nil {
			return err
		}
		if role != "admin" && role != "analyst" {
			return assetapp.ErrAssetWriteForbidden
		}
		if action == "start" || action == "resume" {
			if task.Queued || (action == "start" && task.Status != "created") || (action == "resume" && task.Status != "paused") {
				return aiapp.ErrTaskControlConflict
			}
			var valid bool
			if err := tx.Raw(`SELECT original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=ai_task.target_id AND deleted_at IS NULL) FROM ai_task WHERE id=?`, id).Scan(&valid).Error; err != nil {
				return err
			}
			if !valid {
				return assetapp.ErrAssetTargetNotFound
			}
			var until *time.Time
			if !bySchedule {
				value, scheduleErr := manualScheduleEnd(tx, user, id, time.Now())
				if scheduleErr != nil {
					return scheduleErr
				}
				until = value
			}
			return tx.Exec(`UPDATE ai_task SET status='created',queued=TRUE,completed_at=NULL,error_message='',paused_by_schedule=FALSE,schedule_manual_until=?,lease_until=NULL,updated_at=CURRENT_TIMESTAMP WHERE id=?`, until, id).Error
		}
		status := "paused"
		if action == "stop" {
			status = "stopped"
		}
		if task.Status == "done" || task.Status == "failed" || task.Status == "timeout" || task.Status == "stopped" {
			return aiapp.ErrTaskControlConflict
		}
		if err := tx.Exec(`UPDATE ai_task SET status=?,queued=FALSE,lease_until=NULL,paused_by_schedule=?,schedule_manual_until=NULL,updated_at=CURRENT_TIMESTAMP WHERE id=?`, status, bySchedule, id).Error; err != nil {
			return err
		}
		// Explicit task pause wins over any wake produced by the interrupted chat.
		if err := tx.Exec(`UPDATE ai_task_main_chat_run SET status='stopping' WHERE task_id=? AND status='running'`, id).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DELETE FROM ai_task_main_chat_wake WHERE task_id=?`, id).Error; err != nil {
			return err
		}
		if guarded {
			return tx.Exec(`INSERT INTO ai_auto_task_action(tenant_id,run_id,tool_use_id,request_hash,task_id,operation) VALUES(?,?,?,?,?,'pause')`, assetapp.DefaultTenantID, mutation.RunID, mutation.ToolUseID, mutation.InputHash, id).Error
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetTask(ctx, user, id)
}

func (s *TaskStore) ClaimTask(ctx context.Context, instance string) (*aiapp.Task, error) {
	var claimed aiapp.Task
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := acquireCalendarLock(tx, true); err != nil {
			return err
		}
		var limitEnabled bool
		var concurrencyLimit int
		if err := tx.Raw(`SELECT COALESCE(o.options->>'task_concurrency_enabled','true')='true',CASE WHEN COALESCE(o.options->>'task_concurrency_limit','') ~ '^[0-9]{1,4}$' THEN GREATEST(1,(o.options->>'task_concurrency_limit')::integer) ELSE 5 END FROM (SELECT COALESCE((SELECT options FROM ai_runtime_setting WHERE tenant_id=?),'{}'::jsonb) options) o`, assetapp.DefaultTenantID).Row().Scan(&limitEnabled, &concurrencyLimit); err != nil {
			return err
		}
		if !limitEnabled {
			concurrencyLimit = 16
		}
		concurrencyLimit = min(concurrencyLimit, 16)
		{
			var running int64
			if err := tx.Raw(`SELECT COUNT(*) FROM ai_task WHERE tenant_id=? AND deleted_at IS NULL AND status='running' AND lease_until>clock_timestamp()`, assetapp.DefaultTenantID).Scan(&running).Error; err != nil {
				return err
			}
			if running >= int64(concurrencyLimit) {
				return nil
			}
		}
		for {
			var id int64
			var owner int
			var manualUntil sql.NullTime
			err := tx.Raw(`SELECT t.id,t.owner_user_id,t.schedule_manual_until`+taskOwnerJoin+` WHERE t.deleted_at IS NULL AND t.status='created' AND t.queued=TRUE
			AND tenant.status='active' AND u.is_active=TRUE AND m.role IN ('admin','analyst')
			AND NOT EXISTS(SELECT 1 FROM ai_task_intent_control ctl WHERE ctl.task_id=t.id AND ctl.action='cancel' AND ctl.status IN ('pending','paused'))
			AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=t.target_id AND deleted_at IS NULL))
			AND (NOT EXISTS(SELECT 1 FROM ai_schedule_task link WHERE link.task_id=t.id)
			 OR t.schedule_manual_until>clock_timestamp()
			 OR EXISTS(SELECT 1 FROM ai_schedule_task link JOIN ai_schedule schedule ON schedule.id=link.schedule_id
			   WHERE link.task_id=t.id AND schedule.enabled=TRUE AND schedule.status='running'))
			ORDER BY t.created_at,t.id FOR UPDATE OF t SKIP LOCKED LIMIT 1`).Row().Scan(&id, &owner, &manualUntil)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			schedules, err := loadSchedules(tx, owner, 0)
			if err != nil {
				return err
			}
			now := time.Now()
			bound, open := false, manualUntil.Valid && manualUntil.Time.After(now)
			var historySchedule int64
			for _, record := range schedules {
				item := record.Item
				for _, taskID := range item.TaskIDs {
					if taskID != id {
						continue
					}
					bound = true
					if historySchedule == 0 {
						historySchedule = item.ID
					}
					if item.Status == "running" && aiapp.EvaluateRuntimeScheduleWindow(&item, now, time.Time{}).Active {
						open = true
					}
				}
			}
			if bound && !open {
				if err := tx.Exec(`UPDATE ai_task SET status='paused',queued=FALSE,paused_by_schedule=TRUE,updated_at=clock_timestamp() WHERE id=?`, id).Error; err != nil {
					return err
				}
				if err := tx.Exec(`INSERT INTO ai_schedule_history(schedule_id,task_id,action,message) VALUES(?,?,'pause','计划窗口')`, historySchedule, id).Error; err != nil {
					return err
				}
				continue
			}
			run := uuid.NewString()
			return tx.Raw(`UPDATE ai_task t SET status='running',queued=FALSE,active_run=?,lease_owner=?,lease_epoch=lease_epoch+1,
			lease_until=clock_timestamp()+INTERVAL '45 seconds',updated_at=CURRENT_TIMESTAMP,error_message='' WHERE t.id=? RETURNING `+taskColumns, run, instance, id).Scan(&claimed).Error
		}
	})
	if err != nil {
		return nil, err
	}
	if claimed.ID == 0 {
		return nil, nil
	}
	items := []aiapp.Task{claimed}
	if err := s.hydrateTaskLLM(ctx, items); err != nil {
		return nil, err
	}
	claimed = items[0]
	return &claimed, nil
}

func (s *TaskStore) StampTaskStart(ctx context.Context, task *aiapp.Task) (*aiapp.Task, error) {
	var result aiapp.Task
	q := s.db.WithContext(ctx).Raw(`UPDATE ai_task t SET first_run_at=COALESCE(first_run_at,clock_timestamp()),
		deadline_at=CASE WHEN first_run_at IS NOT NULL THEN deadline_at WHEN timeout_seconds>0 THEN clock_timestamp()+timeout_seconds*INTERVAL '1 second' ELSE NULL END,updated_at=CURRENT_TIMESTAMP
		WHERE id=? AND active_run=? AND lease_owner=? AND status='running' AND lease_until>clock_timestamp() RETURNING `+taskColumns, task.ID, task.ActiveRun, task.LeaseOwner).Scan(&result)
	if q.Error != nil {
		return nil, q.Error
	}
	if result.ID == 0 {
		return nil, ErrTaskLeaseLost
	}
	items := []aiapp.Task{result}
	if err := s.hydrateTaskLLM(ctx, items); err != nil {
		return nil, err
	}
	result = items[0]
	return &result, nil
}

var ErrTaskLeaseLost = errors.New("task execution lease lost")

func (s *TaskStore) HeartbeatTask(ctx context.Context, task *aiapp.Task) (bool, error) {
	q := s.db.WithContext(ctx).Exec(`UPDATE ai_task t SET lease_until=clock_timestamp()+INTERVAL '45 seconds'
		WHERE id=? AND active_run=? AND lease_owner=? AND lease_epoch=? AND status='running' AND lease_until>clock_timestamp()
		AND EXISTS(SELECT 1 FROM tenant_membership m JOIN tenant tenant ON tenant.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
		 WHERE m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id AND tenant.status='active' AND u.is_active=TRUE AND m.role IN ('admin','analyst'))
		AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=t.target_id AND deleted_at IS NULL))`, task.ID, task.ActiveRun, task.LeaseOwner, task.LeaseEpoch)
	return q.RowsAffected == 1, q.Error
}

func (s *TaskStore) FinishTask(ctx context.Context, task *aiapp.Task, status, reason string) error {
	switch status {
	case "done", "failed", "timeout", "paused":
	default:
		return aiapp.ErrInvalidInput
	}
	return s.db.WithContext(ctx).Exec(`UPDATE ai_task SET status=CASE WHEN ? IN ('done','timeout') AND (EXISTS(SELECT 1 FROM ai_task_human_event e WHERE e.task_id=ai_task.id AND e.processed_at IS NULL) OR EXISTS(SELECT 1 FROM ai_task_intent_message m WHERE m.task_id=ai_task.id AND m.status='pending')) THEN 'created' ELSE ? END,queued=CASE WHEN ? IN ('done','timeout') AND (EXISTS(SELECT 1 FROM ai_task_human_event e WHERE e.task_id=ai_task.id AND e.processed_at IS NULL) OR EXISTS(SELECT 1 FROM ai_task_intent_message m WHERE m.task_id=ai_task.id AND m.status='pending')) THEN TRUE ELSE FALSE END,error_message=?,completed_at=CASE WHEN ? IN ('done','failed','timeout') AND NOT (? IN ('done','timeout') AND (EXISTS(SELECT 1 FROM ai_task_human_event e WHERE e.task_id=ai_task.id AND e.processed_at IS NULL) OR EXISTS(SELECT 1 FROM ai_task_intent_message m WHERE m.task_id=ai_task.id AND m.status='pending'))) THEN clock_timestamp() ELSE NULL END,
		first_run_at=CASE WHEN ?='timeout' AND (EXISTS(SELECT 1 FROM ai_task_human_event e WHERE e.task_id=ai_task.id AND e.processed_at IS NULL) OR EXISTS(SELECT 1 FROM ai_task_intent_message m WHERE m.task_id=ai_task.id AND m.status='pending')) THEN NULL ELSE first_run_at END,
		deadline_at=CASE WHEN ?='timeout' AND (EXISTS(SELECT 1 FROM ai_task_human_event e WHERE e.task_id=ai_task.id AND e.processed_at IS NULL) OR EXISTS(SELECT 1 FROM ai_task_intent_message m WHERE m.task_id=ai_task.id AND m.status='pending')) THEN NULL ELSE deadline_at END,
		lease_until=NULL,updated_at=CURRENT_TIMESTAMP WHERE id=? AND active_run=? AND lease_owner=? AND status='running' AND lease_until>clock_timestamp()`, status, status, status, reason, status, status, status, status, task.ID, task.ActiveRun, task.LeaseOwner).Error
}

func (s *TaskStore) RecoverExpiredTasks(ctx context.Context) (int64, error) {
	q := s.db.WithContext(ctx).Exec(`UPDATE ai_task SET status='paused',queued=FALSE,lease_until=NULL,error_message='上次执行已中断，请恢复任务以继续。',updated_at=CURRENT_TIMESTAMP
		WHERE status='running' AND deleted_at IS NULL AND (lease_until IS NULL OR lease_until<=clock_timestamp())`)
	return q.RowsAffected, q.Error
}

func (s *TaskStore) ReadTaskGraph(ctx context.Context, user int, id int64) (*aiapp.TaskGraph, error) {
	task, err := s.GetTask(ctx, user, id)
	if err != nil {
		return nil, err
	}
	graph := &aiapp.TaskGraph{Nodes: []aiapp.TaskGraphNode{}, Edges: []aiapp.TaskGraphEdge{}, Assets: []aiapp.TaskGraphAsset{}}
	if err := s.db.WithContext(ctx).Raw(`WITH scopes AS (
		SELECT ?::bigint AS exploration_id,0::bigint AS source_task_id,FALSE AS inherited
		UNION ALL SELECT source.exploration_id,source.id,TRUE FROM ai_task_relation r JOIN ai_task source ON source.id=r.source_task_id AND source.tenant_id=r.tenant_id WHERE r.task_id=? AND r.tenant_id=? AND source.owner_user_id=? AND source.deleted_at IS NULL AND (source.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=source.target_id AND deleted_at IS NULL))
	) SELECT n.id,n.kind,n.state,COALESCE(n.delete_reason,'') AS delete_reason,COALESCE((SELECT CASE WHEN c.status IN ('pending','paused') THEN 'pending' WHEN c.status='conflict' AND n.state<>'deleted' THEN 'conflict' ELSE '' END FROM ai_task_intent_control c WHERE c.task_id=? AND c.intent_id=n.id AND c.action='cancel' ORDER BY c.created_at DESC,c.id DESC LIMIT 1),'') AS deletion_state,n.priority,n.payload,n.created_at,scopes.source_task_id,scopes.inherited,COALESCE(f.id,0) AS finding_id,(SELECT COUNT(*) FROM tianqiong_ai.finding_traffic_bindings b WHERE b.finding_id=f.id) AS traffic_count FROM scopes JOIN tianqiong_ai.exploration_nodes n ON n.exploration_id=scopes.exploration_id LEFT JOIN tianqiong_ai.findings f ON f.node_id=n.id WHERE NOT scopes.inherited OR n.kind<>'intent' OR n.state IN ('done','blocked','exhausted','stopped') ORDER BY scopes.inherited,n.id LIMIT 2001`, task.ExplorationID, task.ID, task.TenantID, user, task.ID).Scan(&graph.Nodes).Error; err != nil {
		return nil, err
	}
	if len(graph.Nodes) > 2000 {
		graph.Nodes = graph.Nodes[:2000]
		graph.Truncated = true
	}
	ids := make([]int64, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		ids = append(ids, node.ID)
	}
	if len(ids) > 0 {
		if err := s.db.WithContext(ctx).Raw(`SELECT e.src_id AS source_id,e.dst_id AS target_id,e.rel AS relation FROM tianqiong_ai.exploration_edges e WHERE (e.exploration_id=? OR EXISTS(SELECT 1 FROM ai_task_relation r JOIN ai_task source ON source.id=r.source_task_id AND source.tenant_id=r.tenant_id WHERE r.task_id=? AND r.tenant_id=? AND source.owner_user_id=? AND source.deleted_at IS NULL AND source.exploration_id=e.exploration_id AND (source.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=source.target_id AND deleted_at IS NULL)))) AND e.src_id=ANY(?::bigint[]) AND e.dst_id=ANY(?::bigint[]) ORDER BY e.src_id,e.dst_id,e.rel`, task.ExplorationID, task.ID, task.TenantID, user, pq.Int64Array(ids), pq.Int64Array(ids)).Scan(&graph.Edges).Error; err != nil {
			return nil, err
		}
		if err := s.db.WithContext(ctx).Raw(`SELECT anchor.node_id,asset.id AS asset_id,asset.asset_type,asset.label
			FROM tianqiong_ai.exploration_anchors anchor JOIN asset_entity asset ON asset.id=anchor.asset_id
			WHERE anchor.node_id=ANY(?::bigint[]) AND asset.tenant_id=? ORDER BY anchor.node_id,asset.id`, pq.Int64Array(ids), task.TenantID).Scan(&graph.Assets).Error; err != nil {
			return nil, err
		}
	}
	return graph, nil
}

func (s *TaskStore) ReadTaskNode(ctx context.Context, user int, taskID, nodeID int64) (*aiapp.TaskGraphNode, error) {
	var node aiapp.TaskGraphNode
	result := s.db.WithContext(ctx).Raw(`SELECT n.id,n.kind,n.state,COALESCE(n.delete_reason,'') AS delete_reason,COALESCE((SELECT CASE WHEN c.status IN ('pending','paused') THEN 'pending' WHEN c.status='conflict' AND n.state<>'deleted' THEN 'conflict' ELSE '' END FROM ai_task_intent_control c WHERE c.task_id=t.id AND c.intent_id=n.id AND c.action='cancel' ORDER BY c.created_at DESC,c.id DESC LIMIT 1),'') AS deletion_state,n.priority,n.payload,n.created_at,COALESCE(f.id,0) AS finding_id,(SELECT COUNT(*) FROM tianqiong_ai.finding_traffic_bindings b WHERE b.finding_id=f.id) AS traffic_count,(n.exploration_id<>t.exploration_id) AS inherited,CASE WHEN n.exploration_id<>t.exploration_id THEN source.id ELSE 0 END AS source_task_id
		FROM public.ai_task t JOIN tianqiong_ai.exploration_nodes n ON n.id=? LEFT JOIN public.ai_task source ON source.exploration_id=n.exploration_id AND source.tenant_id=t.tenant_id AND source.owner_user_id=t.owner_user_id AND source.deleted_at IS NULL LEFT JOIN tianqiong_ai.findings f ON f.node_id=n.id
		JOIN public.tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id
		JOIN public.tenant tenant ON tenant.id=m.tenant_id JOIN public.auth_user u ON u.id=m.user_id
		WHERE t.tenant_id=? AND t.owner_user_id=? AND t.id=? AND t.deleted_at IS NULL AND tenant.status='active' AND u.is_active=TRUE AND (n.exploration_id=t.exploration_id OR (source.id IS NOT NULL AND (source.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=source.target_id AND deleted_at IS NULL)) AND EXISTS(SELECT 1 FROM ai_task_relation r WHERE r.task_id=t.id AND r.source_task_id=source.id AND r.tenant_id=t.tenant_id) AND (n.kind<>'intent' OR n.state IN ('done','blocked','exhausted','stopped'))))`, nodeID, assetapp.DefaultTenantID, user, taskID).Scan(&node)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, aiapp.ErrTaskNotFound
	}
	return &node, nil
}

func (s *TaskStore) ReadTaskTrace(ctx context.Context, user int, taskID, intentID int64, stepIDs []int64) ([]aiapp.TaskActivity, error) {
	items := []aiapp.TaskActivity{}
	if stepIDs == nil {
		stepIDs = []int64{}
	}
	err := s.db.WithContext(ctx).Raw(`SELECT a.id,a.node_id,COALESCE(a.worker,'') AS worker,COALESCE(a.kind,'') AS kind,COALESCE(a.tool,'') AS tool,
		COALESCE(a.tool_use_id,'') AS tool_use_id,a.is_error,COALESCE(a.summary,'') AS summary,COALESCE(a.detail,'') AS detail,a.created_at
		FROM public.ai_task t JOIN tianqiong_ai.exploration_nodes n ON n.id=? AND n.kind='intent' JOIN tianqiong_ai.activity a ON a.node_id=n.id AND a.exploration_id=n.exploration_id LEFT JOIN public.ai_task source ON source.exploration_id=n.exploration_id AND source.tenant_id=t.tenant_id AND source.owner_user_id=t.owner_user_id AND source.deleted_at IS NULL
		JOIN public.tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id
		JOIN public.tenant tenant ON tenant.id=m.tenant_id JOIN public.auth_user u ON u.id=m.user_id
		WHERE t.tenant_id=? AND t.owner_user_id=? AND t.id=? AND t.deleted_at IS NULL AND tenant.status='active' AND u.is_active=TRUE
		AND (n.exploration_id=t.exploration_id OR (source.id IS NOT NULL AND n.state IN ('done','blocked','exhausted','stopped') AND (source.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=source.target_id AND deleted_at IS NULL)) AND EXISTS(SELECT 1 FROM ai_task_relation r WHERE r.task_id=t.id AND r.source_task_id=source.id AND r.tenant_id=t.tenant_id))) AND COALESCE(a.kind,'') NOT IN ('thinking','usage') AND (cardinality(?::bigint[])=0 OR a.id=ANY(?::bigint[])) ORDER BY a.id LIMIT 201`,
		intentID, assetapp.DefaultTenantID, user, taskID, pq.Int64Array(stepIDs), pq.Int64Array(stepIDs)).Scan(&items).Error
	return items, err
}

func (s *TaskStore) ReadTaskActivities(ctx context.Context, user int, id, after int64) ([]aiapp.TaskActivity, error) {
	if _, err := s.TrafficTaskTenant(ctx, user, id); err != nil {
		return nil, err
	}
	task, err := s.GetTask(ctx, user, id)
	if err != nil {
		return nil, err
	}
	items := []aiapp.TaskActivity{}
	err = s.db.WithContext(ctx).Raw(`SELECT id,node_id,COALESCE(worker,'') AS worker,COALESCE(kind,'') AS kind,COALESCE(tool,'') AS tool,
		COALESCE(tool_use_id,'') AS tool_use_id,is_error,COALESCE(summary,'') AS summary,COALESCE(detail,'') AS detail,main_seg,created_at
		FROM tianqiong_ai.activity WHERE exploration_id=? AND id>? ORDER BY id LIMIT 200`, task.ExplorationID, after).Scan(&items).Error
	if err != nil {
		return nil, err
	}
	if _, err = s.TrafficTaskTenant(ctx, user, id); err != nil {
		return nil, err
	}
	return items, nil
}

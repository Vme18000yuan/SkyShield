package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	app "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

type Store struct{ db *gorm.DB }

func NewStore(db *gorm.DB) *Store { return &Store{db: db} }
func member(tx *gorm.DB, user int, admin, lock bool) error {
	var role string
	query := `SELECT m.role FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE m.tenant_id=? AND m.user_id=? AND t.status='active' AND u.is_active=TRUE`
	if lock {
		query += ` FOR SHARE OF m,u`
	}
	err := tx.Raw(query, assets.DefaultTenantID, user).Row().Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return app.ErrForbidden
	}
	if err != nil {
		return err
	}
	if admin && role != "admin" {
		return app.ErrForbidden
	}
	return nil
}
func writer(tx *gorm.DB, user int) error {
	var role string
	err := tx.Raw(`SELECT m.role FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE m.tenant_id=? AND m.user_id=? AND t.status='active' AND u.is_active=TRUE FOR SHARE OF m,u`, assets.DefaultTenantID, user).Row().Scan(&role)
	if errors.Is(err, sql.ErrNoRows) || err == nil && role != "admin" && role != "analyst" {
		return app.ErrForbidden
	}
	return err
}
func (s *Store) Seed(ctx context.Context, rules []app.Rule) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended('artex-intercept-defaults',0))`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO ai_intercept_settings(tenant_id) VALUES(?) ON CONFLICT DO NOTHING`, assets.DefaultTenantID).Error; err != nil {
			return err
		}
		var done bool
		if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_runtime_seed WHERE tenant_id=? AND seed_key='artex_intercept_defaults_v1')`, assets.DefaultTenantID).Scan(&done).Error; err != nil {
			return err
		}
		if done {
			return nil
		}
		for _, r := range rules {
			if err := tx.Exec(`INSERT INTO ai_intercept_rule(tenant_id,name,enabled,priority,match_target,match_type,pattern,action,message,timeout_enabled,timeout_seconds,timeout_action) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(tenant_id,name) DO NOTHING`, assets.DefaultTenantID, r.Name, r.Enabled, r.Priority, r.MatchTarget, r.MatchType, r.Pattern, r.Action, r.Message, r.TimeoutEnabled, r.TimeoutSeconds, r.TimeoutAction).Error; err != nil {
				return err
			}
		}
		return tx.Exec(`INSERT INTO ai_runtime_seed(tenant_id,seed_key) VALUES(?,'artex_intercept_defaults_v1')`, assets.DefaultTenantID).Error
	})
}

const ruleColumns = `id,name,enabled,priority,match_target,match_type,pattern,action,message,timeout_enabled,timeout_seconds,timeout_action,created_at,updated_at`

func (s *Store) ListRules(ctx context.Context, user int, admin bool) ([]app.Rule, error) {
	if err := member(s.db.WithContext(ctx), user, admin, false); err != nil {
		return nil, err
	}
	items := []app.Rule{}
	err := s.db.WithContext(ctx).Raw(`SELECT `+ruleColumns+` FROM ai_intercept_rule WHERE tenant_id=? ORDER BY priority DESC,id`, assets.DefaultTenantID).Scan(&items).Error
	return items, err
}
func (s *Store) SaveRule(ctx context.Context, user int, r app.Rule, create bool) (app.Rule, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := member(tx, user, true, true); err != nil {
			return err
		}
		var row *sql.Row
		if create {
			row = tx.Raw(`INSERT INTO ai_intercept_rule(tenant_id,name,enabled,priority,match_target,match_type,pattern,action,message,timeout_enabled,timeout_seconds,timeout_action) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) RETURNING `+ruleColumns, assets.DefaultTenantID, r.Name, r.Enabled, r.Priority, r.MatchTarget, r.MatchType, r.Pattern, r.Action, r.Message, r.TimeoutEnabled, r.TimeoutSeconds, r.TimeoutAction).Row()
		} else {
			row = tx.Raw(`UPDATE ai_intercept_rule SET name=?,enabled=?,priority=?,match_target=?,match_type=?,pattern=?,action=?,message=?,timeout_enabled=?,timeout_seconds=?,timeout_action=?,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=? AND id=? RETURNING `+ruleColumns, r.Name, r.Enabled, r.Priority, r.MatchTarget, r.MatchType, r.Pattern, r.Action, r.Message, r.TimeoutEnabled, r.TimeoutSeconds, r.TimeoutAction, assets.DefaultTenantID, r.ID).Row()
		}
		err := row.Scan(&r.ID, &r.Name, &r.Enabled, &r.Priority, &r.MatchTarget, &r.MatchType, &r.Pattern, &r.Action, &r.Message, &r.TimeoutEnabled, &r.TimeoutSeconds, &r.TimeoutAction, &r.CreatedAt, &r.UpdatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return app.ErrNotFound
		}
		if err != nil && strings.Contains(err.Error(), "23505") {
			return app.ErrInvalidInput
		}
		return err
	})
	return r, err
}
func (s *Store) DeleteRule(ctx context.Context, user int, id int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := member(tx, user, true, true); err != nil {
			return err
		}
		q := tx.Exec(`DELETE FROM ai_intercept_rule WHERE tenant_id=? AND id=?`, assets.DefaultTenantID, id)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return app.ErrNotFound
		}
		return nil
	})
}
func (s *Store) ToggleRule(ctx context.Context, user int, id int64, enabled bool) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := member(tx, user, true, true); err != nil {
			return err
		}
		q := tx.Exec(`UPDATE ai_intercept_rule SET enabled=?,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=? AND id=?`, enabled, assets.DefaultTenantID, id)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return app.ErrNotFound
		}
		return nil
	})
}
func (s *Store) EnabledTools(ctx context.Context, user int, admin bool) ([]string, error) {
	if err := member(s.db.WithContext(ctx), user, admin, false); err != nil {
		return nil, err
	}
	var raw []byte
	err := s.db.WithContext(ctx).Raw(`SELECT enabled_tools::text FROM ai_intercept_settings WHERE tenant_id=?`, assets.DefaultTenantID).Row().Scan(&raw)
	if err != nil {
		return nil, err
	}
	items := []string{}
	err = json.Unmarshal(raw, &items)
	return items, err
}
func (s *Store) SetEnabledTools(ctx context.Context, user int, items []string) error {
	raw, _ := json.Marshal(items)
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := member(tx, user, true, true); err != nil {
			return err
		}
		return tx.Exec(`UPDATE ai_intercept_settings SET enabled_tools=?::jsonb,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=?`, string(raw), assets.DefaultTenantID).Error
	})
}

func activeRun(tx *gorm.DB, call app.Call) error {
	if err := member(tx, call.UserID, false, true); err != nil {
		return err
	}
	var found string
	var err error
	if call.TaskID > 0 {
		err = tx.Raw(`SELECT ?::text FROM ai_task WHERE tenant_id=? AND owner_user_id=? AND id=? AND tianqiong_ai.task_run_live(id,owner_user_id,?::uuid) FOR SHARE`, call.RunID, assets.DefaultTenantID, call.UserID, call.TaskID, call.RunID).Row().Scan(&found)
	} else {
		err = tx.Raw(`SELECT id::text FROM ai_run WHERE tenant_id=? AND owner_user_id=? AND id=? AND conversation_id=? AND status='running' AND lease_until>clock_timestamp() FOR SHARE`, assets.DefaultTenantID, call.UserID, call.RunID, call.ConversationID).Row().Scan(&found)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return app.ErrRunInactive
	}
	return err
}
func (s *Store) CreateApproval(ctx context.Context, call app.Call, rule app.Rule, action string) (*app.Approval, error) {
	var id int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := activeRun(tx, call); err != nil {
			return err
		}
		raw := call.Input
		if len(raw) == 0 {
			raw = []byte(`{}`)
		}
		if !json.Valid(raw) {
			raw, _ = json.Marshal(string(raw))
		}
		status, effective, execution := "pending", "", "pending"
		if action == "allow" {
			status, effective = "allowed", "allow"
		} else if action == "deny" {
			status, effective, execution = "denied", "deny", "not_executed"
		}
		reason := rule.Message
		if reason == "" {
			if action == "deny" {
				reason = "拦截规则 [" + rule.Name + "] 禁止执行此工具"
			} else if action == "ask" {
				reason = "拦截规则 [" + rule.Name + "] 要求用户审批，请等待"
			}
		}
		digest := sha256.Sum256(raw)
		ruleJSON, _ := json.Marshal(rule)
		configHash := sha256.Sum256(ruleJSON)
		correlation := "unavailable"
		var err error
		if call.ToolUseID != "" {
			var count int64
			if call.TaskID > 0 {
				err = tx.Raw(`SELECT count(*) FROM tianqiong_ai.activity a JOIN ai_task t ON t.exploration_id=a.exploration_id WHERE t.id=? AND t.owner_user_id=? AND a.execution_id=? AND a.tool_use_id=? AND a.kind='tool_use' AND a.tool=?`, call.TaskID, call.UserID, call.RunID, call.ToolUseID, call.ToolName).Row().Scan(&count)
			} else {
				err = tx.Raw(`SELECT count(*) FROM ai_conversation_activity WHERE tenant_id=? AND owner_user_id=? AND conversation_id=? AND run_id=? AND tool_use_id=? AND kind='tool_use' AND tool=?`, assets.DefaultTenantID, call.UserID, call.ConversationID, call.RunID, call.ToolUseID, call.ToolName).Row().Scan(&count)
			}
			if err != nil {
				return err
			}
			if count == 1 {
				correlation = "exact"
			} else if count > 1 {
				correlation = "ambiguous"
			}
		}
		configDigest := hex.EncodeToString(configHash[:])
		if rule.ConfigDigest != "" {
			configDigest = rule.ConfigDigest
		}
		source := rule.DecisionSource
		if source == "" {
			source = "rule"
		}
		auditMap := map[string]any{}
		if action != "allow" && len(call.TraceAudit) > 0 {
			_ = json.Unmarshal(call.TraceAudit, &auditMap)
		}
		auditMap["run_id"], auditMap["tool_use_id"], auditMap["correlation"] = call.RunID, call.ToolUseID, correlation
		if _, ok := auditMap["input_digest"]; !ok {
			auditMap["input_digest"] = hex.EncodeToString(digest[:])
		}
		if _, ok := auditMap["user_message"]; !ok {
			auditMap["user_message"] = ""
		}
		if _, ok := auditMap["context"]; !ok {
			auditMap["context"] = nil
		}
		if _, ok := auditMap["captured_at"]; !ok {
			auditMap["captured_at"] = time.Now().UTC()
		}
		auditMap["initial_action"], auditMap["initial_reason"], auditMap["effective_action"] = action, reason, effective
		auditMap["rule_name"], auditMap["config_digest"] = rule.Name, configDigest
		auditMap["execution_status"] = map[bool]string{true: "not_executed", false: "not_started"}[action == "deny"]
		if len(rule.ModelInput) > 0 {
			auditMap["model_input"] = json.RawMessage(rule.ModelInput)
			auditMap["model_input_digest"] = rule.ModelInputDigest
		}
		if rule.ModelFallback {
			auditMap["model_fallback"] = true
		}
		if rule.JudgeProfileID != nil {
			auditMap["profile_id"] = *rule.JudgeProfileID
			var profileName string
			profileErr := tx.Raw(`SELECT name FROM ai_llm_profile WHERE tenant_id=? AND id=?`, assets.DefaultTenantID, *rule.JudgeProfileID).Row().Scan(&profileName)
			if profileErr == nil {
				auditMap["profile_name"] = profileName
			} else if !errors.Is(profileErr, sql.ErrNoRows) {
				return profileErr
			}
		}
		audit, _ := json.Marshal(auditMap)
		return tx.Raw(`INSERT INTO ai_approval(tenant_id,owner_user_id,run_id,conversation_id,task_id,intent_id,agent_name,tool_name,tool_use_id,tool_input,rule_id,rule_name,decision_source,status,effective_action,reason,timeout_at,timeout_action,execution_status,decided_at,audit) VALUES(?,?,?,NULLIF(?,'')::uuid,NULLIF(?::bigint,0),NULLIF(?::bigint,0),?,?,?,?::jsonb,NULLIF(?::bigint,0),?,?,?,?,?,CASE WHEN ? AND ?='ask' THEN clock_timestamp()+make_interval(secs=>?) END,?,?,CASE WHEN ?<>'pending' THEN clock_timestamp() END,?::jsonb) RETURNING id`, assets.DefaultTenantID, call.UserID, call.RunID, call.ConversationID, call.TaskID, call.IntentID, call.AgentName, call.ToolName, call.ToolUseID, string(raw), rule.ID, rule.Name, source, status, effective, reason, rule.TimeoutEnabled, action, rule.TimeoutSeconds, rule.TimeoutAction, execution, status, string(audit)).Row().Scan(&id)
	})
	if err != nil {
		return nil, err
	}
	item, readErr := s.ReadApproval(ctx, call.UserID, id)
	if readErr != nil {
		return &app.Approval{ID: id}, readErr
	}
	return item, nil
}

const approvalColumns = `a.id,a.rule_id,a.conversation_id::text,a.task_id::text,a.agent_name,a.tool_name,a.tool_input::text,a.status,a.decision_source,a.reason,a.decided_at,a.created_at,COALESCE(c.title,'') AS conv_title,COALESCE(c.agent_key,'') AS conv_agent_key,COALESCE(task.name,'') AS task_name,a.rule_name,a.effective_action,a.execution_status,a.execution_output,a.tool_use_id,a.run_id::text,a.audit::text,a.intent_id`

func reconcile(ctx context.Context, db *gorm.DB, user int) error {
	inactive := ` NOT EXISTS(SELECT 1 FROM ai_run r WHERE r.id=a.run_id AND r.tenant_id=a.tenant_id AND r.owner_user_id=a.owner_user_id AND r.status='running' AND r.lease_until>clock_timestamp()) AND NOT COALESCE(tianqiong_ai.task_run_live(a.task_id,a.owner_user_id,a.run_id),FALSE)`
	if err := db.WithContext(ctx).Exec(`UPDATE ai_approval a SET status='denied',effective_action='deny',reason=CASE WHEN reason='' THEN '运行已停止或租约过期' ELSE reason||'；运行已停止或租约过期' END,decided_at=CURRENT_TIMESTAMP,execution_status='not_executed',finished_at=CURRENT_TIMESTAMP,audit=audit||jsonb_build_object('effective_action','deny','decision_reason','运行已停止或租约过期','execution_status','not_executed') WHERE a.tenant_id=? AND a.owner_user_id=? AND a.status='pending' AND `+inactive, assets.DefaultTenantID, user).Error; err != nil {
		return err
	}
	return db.WithContext(ctx).Exec(`UPDATE ai_approval a SET execution_status='unknown',finished_at=CURRENT_TIMESTAMP,audit=audit||jsonb_build_object('execution_status','unknown','decision_reason','运行中断，无法确认工具是否执行') WHERE a.tenant_id=? AND a.owner_user_id=? AND a.effective_action='allow' AND a.execution_status='pending' AND `+inactive, assets.DefaultTenantID, user).Error
}

func scanApproval(row interface{ Scan(...any) error }) (*app.Approval, error) {
	var a app.Approval
	var input, audit []byte
	err := row.Scan(&a.ID, &a.RuleID, &a.ConversationID, &a.TaskID, &a.AgentName, &a.ToolName, &input, &a.Status, &a.DecisionSource, &a.Reason, &a.DecidedAt, &a.CreatedAt, &a.ConvTitle, &a.ConvAgentKey, &a.TaskName, &a.RuleName, &a.EffectiveAction, &a.ExecutionStatus, &a.ExecutionOutput, &a.ToolUseID, &a.RunID, &audit, &a.IntentID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, app.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.ToolInput = json.RawMessage(input)
	a.Audit = json.RawMessage(audit)
	return &a, nil
}
func (s *Store) ReadApproval(ctx context.Context, user int, id int64) (*app.Approval, error) {
	if err := member(s.db.WithContext(ctx), user, false, false); err != nil {
		return nil, err
	}
	if err := reconcile(ctx, s.db, user); err != nil {
		return nil, err
	}
	return scanApproval(s.db.WithContext(ctx).Raw(`SELECT `+approvalColumns+` FROM ai_approval a LEFT JOIN ai_conversation c ON c.id=a.conversation_id AND c.tenant_id=a.tenant_id LEFT JOIN ai_task task ON task.id=a.task_id AND task.tenant_id=a.tenant_id WHERE a.tenant_id=? AND a.owner_user_id=? AND a.id=?`, assets.DefaultTenantID, user, id).Row())
}
func (s *Store) ReadExecution(ctx context.Context, user int, id int64) (*app.Execution, error) {
	approval, err := s.ReadApproval(ctx, user, id)
	if err != nil {
		return nil, err
	}
	if err = s.executionSourceVisible(ctx, user, approval); err != nil {
		return nil, err
	}
	if approval.ToolUseID == "" {
		return nil, app.ErrExecutionUnavailable
	}
	var rows *sql.Rows
	if approval.ConversationID != nil {
		rows, err = s.db.WithContext(ctx).Raw(`SELECT a.id,NULL::bigint,c.agent_key,a.created_at,a.kind,a.tool,a.tool_use_id,a.is_error,a.summary,NULL::integer FROM ai_conversation_activity a JOIN ai_conversation c ON c.id=a.conversation_id WHERE a.tenant_id=? AND a.owner_user_id=? AND a.conversation_id=? AND a.run_id=? AND a.tool_use_id=? AND a.kind IN ('tool_use','tool_result') ORDER BY a.id LIMIT 3`, assets.DefaultTenantID, user, *approval.ConversationID, approval.RunID, approval.ToolUseID).Rows()
	} else if approval.TaskID != nil {
		taskID, e := strconv.ParseInt(*approval.TaskID, 10, 64)
		if e != nil {
			return nil, app.ErrExecutionUnavailable
		}
		rows, err = s.db.WithContext(ctx).Raw(`SELECT a.id,a.node_id,COALESCE(a.worker,''),a.created_at,a.kind,COALESCE(a.tool,''),COALESCE(a.tool_use_id,''),a.is_error,COALESCE(a.summary,''),a.main_seg FROM tianqiong_ai.activity a JOIN ai_task task ON task.exploration_id=a.exploration_id WHERE task.tenant_id=? AND task.owner_user_id=? AND task.id=? AND task.deleted_at IS NULL AND a.execution_id=? AND a.node_id IS NOT DISTINCT FROM ?::bigint AND a.tool_use_id=? AND a.kind IN ('tool_use','tool_result') ORDER BY a.id LIMIT 3`, assets.DefaultTenantID, user, taskID, approval.RunID, approval.IntentID, approval.ToolUseID).Rows()
	} else {
		return nil, app.ErrExecutionUnavailable
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []app.ExecutionActivity{}
	for rows.Next() {
		var item app.ExecutionActivity
		var node *int64
		if err = rows.Scan(&item.Seq, &node, &item.Worker, &item.TS, &item.Kind, &item.Tool, &item.ToolUseID, &item.IsError, &item.Summary, &item.MainSeg); err != nil {
			return nil, err
		}
		if node != nil {
			v := strconv.FormatInt(*node, 10)
			item.IntentID = &v
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if err = s.executionSourceVisible(ctx, user, approval); err != nil {
		return nil, err
	}
	if len(items) == 0 || len(items) > 2 || items[0].Kind != "tool_use" || items[0].Tool != approval.ToolName {
		return nil, app.ErrExecutionUnavailable
	}
	if len(items) == 2 && (items[1].Kind != "tool_result" || items[1].Worker != items[0].Worker || items[1].Tool != "" && items[1].Tool != items[0].Tool) {
		return nil, app.ErrExecutionUnavailable
	}
	result := &app.Execution{ConversationID: approval.ConversationID, TaskID: approval.TaskID, Seq: items[0].Seq, Items: items}
	if approval.TaskID != nil {
		first := items[0]
		if first.Worker == "mainagent" {
			segment := 0
			if first.MainSeg != nil {
				segment = *first.MainSeg
			}
			result.Session = fmt.Sprintf("main:%d", segment)
		} else if first.Worker == "planner" {
			result.Session = "plan"
		} else if first.IntentID != nil {
			result.Session = "intent:" + *first.IntentID
		} else {
			return nil, app.ErrExecutionUnavailable
		}
	}
	return result, nil
}
func (s *Store) ListApprovals(ctx context.Context, user int, filter app.Filter) ([]app.Approval, int64, error) {
	if err := member(s.db.WithContext(ctx), user, false, false); err != nil {
		return nil, 0, err
	}
	if err := reconcile(ctx, s.db, user); err != nil {
		return nil, 0, err
	}
	where := ` FROM ai_approval a LEFT JOIN ai_conversation c ON c.id=a.conversation_id AND c.tenant_id=a.tenant_id LEFT JOIN ai_task task ON task.id=a.task_id AND task.tenant_id=a.tenant_id WHERE a.tenant_id=? AND a.owner_user_id=? AND (?='' OR a.status=?) AND (?='' OR a.decision_source=?) AND (?='' OR a.id::text=trim(leading '#' from ?) OR strpos(lower(concat_ws(' ',a.tool_name,a.rule_name,a.reason,a.tool_input::text,c.title,task.name,task.description)),lower(?))>0) AND (?=0 OR a.task_id=?) AND (?='' OR a.created_at>=(NULLIF(?,'')::date::timestamp AT TIME ZONE 'UTC')) AND (?='' OR a.created_at<(NULLIF(?,'')::date::timestamp AT TIME ZONE 'UTC') + INTERVAL '1 day')`
	args := []any{assets.DefaultTenantID, user, filter.Status, filter.Status, filter.Source, filter.Source, filter.Query, filter.Query, filter.Query, filter.TaskID, filter.TaskID, filter.From, filter.From, filter.To, filter.To}
	var total int64
	if err := s.db.WithContext(ctx).Raw(`SELECT count(*)`+where, args...).Row().Scan(&total); err != nil {
		return nil, 0, err
	}
	// History rows stay compact; full judge context and output are loaded by detail.
	listColumns := strings.Replace(approvalColumns, "a.audit::text", "'null'::text", 1)
	listColumns = strings.Replace(listColumns, "a.execution_output", "''::text", 1)
	rows, err := s.db.WithContext(ctx).Raw(`SELECT `+listColumns+where+` ORDER BY a.id DESC LIMIT ? OFFSET ?`, append(args, filter.Size, (filter.Page-1)*filter.Size)...).Rows()
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []app.Approval{}
	for rows.Next() {
		item, err := scanApproval(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *item)
	}
	return items, total, rows.Err()
}
func (s *Store) DecideApproval(ctx context.Context, user int, id int64, decision string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := writer(tx, user); err != nil {
			return err
		}
		var runID string
		var conversation *string
		var taskID *int64
		err := tx.Raw(`SELECT run_id::text,conversation_id::text,task_id FROM ai_approval WHERE tenant_id=? AND owner_user_id=? AND id=? AND status='pending' AND (timeout_at IS NULL OR timeout_at>clock_timestamp()) FOR UPDATE`, assets.DefaultTenantID, user, id).Row().Scan(&runID, &conversation, &taskID)
		if errors.Is(err, sql.ErrNoRows) {
			return app.ErrAlreadyDecided
		}
		if err != nil {
			return err
		}
		call := app.Call{UserID: user, RunID: runID}
		if conversation != nil {
			call.ConversationID = *conversation
		}
		if taskID != nil {
			call.TaskID = *taskID
		}
		if err = activeRun(tx, call); errors.Is(err, app.ErrRunInactive) {
			return app.ErrAlreadyDecided
		} else if err != nil {
			return err
		}
		action := "deny"
		if decision == "allowed" {
			action = "allow"
		}
		result := tx.Exec(`UPDATE ai_approval a SET status=?,effective_action=?,decision_source='manual',decided_at=clock_timestamp(),audit=audit||jsonb_build_object('effective_action',?::text,'decision_reason',?::text,'execution_status',CASE WHEN ?='denied' THEN 'not_executed' ELSE 'awaiting_result' END),execution_status=CASE WHEN ?='denied' THEN 'not_executed' ELSE execution_status END WHERE a.tenant_id=? AND a.owner_user_id=? AND a.id=? AND a.status='pending' AND (a.timeout_at IS NULL OR a.timeout_at>clock_timestamp()) AND (EXISTS(SELECT 1 FROM ai_run r WHERE r.id=a.run_id AND r.tenant_id=a.tenant_id AND r.owner_user_id=a.owner_user_id AND r.status='running' AND r.lease_until>clock_timestamp()) OR COALESCE(tianqiong_ai.task_run_live(a.task_id,a.owner_user_id,a.run_id),FALSE))`, decision, action, action, decision, decision, decision, assets.DefaultTenantID, user, id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return app.ErrAlreadyDecided
		}
		return nil
	})
}
func (s *Store) ResolveTimeout(ctx context.Context, call app.Call, id int64) (string, error) {
	var action string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := activeRun(tx, call); err != nil {
			return err
		}
		err := tx.Raw(`UPDATE ai_approval SET status='timeout',effective_action=timeout_action,decided_at=clock_timestamp(),audit=audit||jsonb_build_object('effective_action',timeout_action,'decision_reason','审批超时','execution_status',CASE WHEN timeout_action='deny' THEN 'not_executed' ELSE 'awaiting_result' END),execution_status=CASE WHEN timeout_action='deny' THEN 'not_executed' ELSE execution_status END WHERE tenant_id=? AND owner_user_id=? AND run_id=? AND id=? AND status='pending' AND timeout_at<=clock_timestamp() RETURNING effective_action`, assets.DefaultTenantID, call.UserID, call.RunID, id).Row().Scan(&action)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil {
		return "", err
	}
	if action != "" {
		return action, nil
	}
	row, err := s.ReadApproval(ctx, call.UserID, id)
	if err != nil {
		return "", err
	}
	if row.ExecutionStatus == "pending" && (row.Status == "allowed" || row.Status == "timeout" && row.EffectiveAction == "allow") {
		return "allow", nil
	}
	if row.Status == "pending" {
		return "pending", nil
	}
	return "deny", nil
}
func (s *Store) CancelApproval(ctx context.Context, call app.Call, id int64) error {
	return s.db.WithContext(ctx).Exec(`UPDATE ai_approval SET status=CASE WHEN status='pending' THEN 'denied' ELSE status END,effective_action=CASE WHEN status='pending' THEN 'deny' ELSE effective_action END,reason=CASE WHEN reason='' THEN '运行已取消' ELSE reason||'；运行已取消' END,decided_at=COALESCE(decided_at,clock_timestamp()),audit=audit||jsonb_build_object('effective_action',CASE WHEN status='pending' THEN 'deny' ELSE effective_action END,'decision_reason','运行已取消','execution_status','not_executed'),execution_status='not_executed',finished_at=clock_timestamp() WHERE tenant_id=? AND owner_user_id=? AND run_id=? AND id=? AND execution_status='pending' AND (status='pending' OR (status IN ('allowed','timeout') AND effective_action='allow'))`, assets.DefaultTenantID, call.UserID, call.RunID, id).Error
}

func (s *Store) FinishApproval(ctx context.Context, call app.Call, id int64, ok bool, output string, truncated bool) error {
	status := "failed"
	if ok {
		status = "succeeded"
	}
	result := s.db.WithContext(ctx).Exec(`UPDATE ai_approval SET execution_status=?,execution_output=?,finished_at=clock_timestamp(),audit=audit||jsonb_build_object('execution_status',?::text,'output',?::text,'output_truncated',?::boolean,'execution_ended_at',clock_timestamp()) WHERE tenant_id=? AND owner_user_id=? AND run_id=? AND id=? AND effective_action='allow' AND execution_status='pending'`, status, output, status, output, truncated, assets.DefaultTenantID, call.UserID, call.RunID, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("approval execution scope rejected")
	}
	return nil
}

var _ app.Store = (*Store)(nil)

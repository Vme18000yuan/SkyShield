package repository

import (
	"context"
	"encoding/json"
	"fmt"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
	"strings"
	"time"
	"unicode/utf8"
)

type AgentTriggerStore struct{ db *gorm.DB }

func NewAgentTriggerStore(db *gorm.DB) *AgentTriggerStore { return &AgentTriggerStore{db: db} }
func (s *AgentTriggerStore) InitializeAgentEvents(ctx context.Context) error {
	return s.db.WithContext(ctx).Exec(`INSERT INTO ai_agent_event_cursor(tenant_id,event_id) SELECT ?,COALESCE(MAX(id),0) FROM ai_agent_event WHERE tenant_id=? ON CONFLICT(tenant_id) DO NOTHING`, assets.DefaultTenantID, assets.DefaultTenantID).Error
}

type triggerRow struct {
	ID                              int64
	OwnerUserID                     int
	AgentKey, Name, Config, Options string
	CreatedAt                       time.Time
	LastFire                        *time.Time
}
type triggerEvent struct {
	ID            int64
	OwnerUserID   int
	TaskID        int64
	Kind, Payload string
	CreatedAt     time.Time
}

func (s *AgentTriggerStore) CollectAgentEvents(ctx context.Context, now time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked bool
		if err := tx.Raw(`SELECT pg_try_advisory_xact_lock(?,?)`, 0x4147454e, 0x45564e54).Scan(&locked).Error; err != nil {
			return err
		}
		if !locked {
			return nil
		}
		var lastID int64
		if err := tx.Raw(`SELECT event_id FROM ai_agent_event_cursor WHERE tenant_id=? FOR UPDATE`, assets.DefaultTenantID).Row().Scan(&lastID); err != nil {
			return err
		}
		var triggers []triggerRow
		if err := tx.Raw(`SELECT t.id,t.owner_user_id,t.agent_key,a.display_name AS name,t.config::text,COALESCE(o.options::text,'{}') options,t.created_at,t.last_fire FROM ai_agent_trigger t JOIN ai_custom_agent a ON a.tenant_id=t.tenant_id AND a.agent_key=t.agent_key LEFT JOIN ai_agent_option o ON o.tenant_id=t.tenant_id AND o.agent_key=t.agent_key WHERE t.tenant_id=? AND t.enabled=TRUE AND a.enabled=TRUE ORDER BY t.id`, assets.DefaultTenantID).Scan(&triggers).Error; err != nil {
			return err
		}
		var events []triggerEvent
		if err := tx.Raw(`SELECT id,owner_user_id,task_id,kind,payload::text,created_at FROM ai_agent_event WHERE tenant_id=? AND id>? ORDER BY id LIMIT 100`, assets.DefaultTenantID, lastID).Scan(&events).Error; err != nil {
			return err
		}
		for _, row := range triggers {
			var trigger artexdb.AgentTrigger
			if err := json.Unmarshal([]byte(row.Config), &trigger); err != nil {
				return err
			}
			options := airuntime.DefaultAgentOptions(row.AgentKey)
			if err := json.Unmarshal([]byte(row.Options), &options); err != nil {
				return err
			}
			if trigger.IntervalSec > 0 && (row.LastFire == nil || !now.Before(row.LastFire.Add(time.Duration(trigger.IntervalSec)*time.Second))) {
				key := fmt.Sprintf("%d/interval/%d", row.ID, now.UnixNano())
				if err := enqueueAgentJob(tx, row, options, row.OwnerUserID, 0, key, row.Name+" · 定时触发", trigger.IntervalMessage, false); err != nil {
					return err
				}
				if err := tx.Exec(`UPDATE ai_agent_trigger SET last_fire=? WHERE tenant_id=? AND id=?`, now, assets.DefaultTenantID, row.ID).Error; err != nil {
					return err
				}
			}
			for _, event := range events {
				if event.CreatedAt.Before(row.CreatedAt) {
					continue
				}
				var body map[string]any
				if err := json.Unmarshal([]byte(event.Payload), &body); err != nil {
					return err
				}
				message, matched := triggerMessage(trigger, event.Kind, body)
				if !matched {
					continue
				}
				data, _ := json.MarshalIndent(body, "", "  ")
				message += "\n\n【触发上下文】\n" + string(data)
				key := fmt.Sprintf("%d/event/%d", row.ID, event.ID)
				if err := enqueueAgentJob(tx, row, options, event.OwnerUserID, event.TaskID, key, row.Name+" · "+event.Kind, message, true); err != nil {
					return err
				}
			}
		}
		if len(events) > 0 {
			return tx.Exec(`UPDATE ai_agent_event_cursor SET event_id=? WHERE tenant_id=?`, events[len(events)-1].ID, assets.DefaultTenantID).Error
		}
		return nil
	})
}
func triggerMessage(trigger artexdb.AgentTrigger, kind string, body map[string]any) (string, bool) {
	switch kind {
	case "finding":
		return trigger.FindingMessage, trigger.OnFinding
	case "goal_met":
		return trigger.GoalMessage, trigger.OnGoalMet
	case "task_timeout":
		return trigger.TaskTimeoutMessage, trigger.OnTaskTimeout
	case "task_create":
		return trigger.TaskCreateMessage, trigger.OnTaskCreate
	case "tool_call":
		name, _ := body["tool"].(string)
		// A failed finding registration did not create anything to report.
		// Other failed tool calls remain available to custom triggers.
		failed, _ := body["is_error"].(bool)
		if failed && strings.EqualFold(name, "report_finding") {
			return "", false
		}
		for _, tool := range trigger.ToolNames {
			if trigger.OnToolCall && strings.EqualFold(name, tool) {
				return trigger.ToolCallMessage, true
			}
		}
	}
	return "", false
}
func boundedTriggerMessage(message string) string {
	if strings.TrimSpace(message) == "" {
		message = "请根据 Agent 职责执行本次触发任务。"
	}
	if len(message) <= 60000 {
		return message
	}
	message = message[:59000]
	for !utf8.ValidString(message) {
		message = message[:len(message)-1]
	}
	return message + "\n（触发上下文过长已截断，请用任务查询工具读取完整证据。）"
}
func enqueueAgentJob(tx *gorm.DB, row triggerRow, options airuntime.AgentOptions, user int, task int64, key, title, message string, mergeable bool) error {
	message = boundedTriggerMessage(message)
	if mergeable && options.TriggerRunMode == "serial" && options.TriggerMergeMode != "none" {
		var existing struct {
			ID      int64
			Message string
		}
		query := tx.Raw(`SELECT id,message FROM ai_agent_trigger_job WHERE tenant_id=? AND agent_key=? AND owner_user_id=? AND status='queued' AND event_key NOT LIKE 'finding-case-review:%' AND event_key NOT LIKE 'retest:%' AND (?='all' OR source_task_id=?) ORDER BY id FOR UPDATE LIMIT 1`, assets.DefaultTenantID, row.AgentKey, user, options.TriggerMergeMode, task).Scan(&existing)
		if query.Error != nil {
			return query.Error
		}
		if existing.ID > 0 && len(existing.Message)+len(message)+32 <= 60000 {
			return tx.Exec(`UPDATE ai_agent_trigger_job SET message=message||E'\n\n【合并触发】\n'||?,updated_at=CURRENT_TIMESTAMP WHERE id=?`, message, existing.ID).Error
		}
	}
	return tx.Exec(`INSERT INTO ai_agent_trigger_job(tenant_id,trigger_id,agent_key,owner_user_id,source_task_id,event_key,title,message) VALUES(?,?,?,?,NULLIF(?,0),?,?,?) ON CONFLICT(tenant_id,event_key) DO NOTHING`, assets.DefaultTenantID, row.ID, row.AgentKey, user, task, key, title, message).Error
}

func (s *AgentTriggerStore) ReconcileAgentJobs(ctx context.Context) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Historical finding reviews are retired. Serialize with claiming so a
		// waiting review cannot start while its job is being cancelled.
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(?,?)`, 0x4147454e, 0x4a4f4253).Error; err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE ai_run r SET status='stopping',terminal_reason='user_stop' FROM ai_agent_trigger_job j WHERE j.tenant_id=? AND j.agent_key='reporter' AND j.event_key LIKE 'finding-case-review:%' AND j.status='running' AND j.run_id=r.id AND r.tenant_id=j.tenant_id AND r.owner_user_id=j.owner_user_id AND r.status='running'`, assets.DefaultTenantID).Error; err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE ai_agent_trigger_job SET status='cancelled',error_message='AI 漏洞审查功能已停用',updated_at=CURRENT_TIMESTAMP WHERE tenant_id=? AND agent_key='reporter' AND event_key LIKE 'finding-case-review:%' AND status IN ('queued','starting')`, assets.DefaultTenantID).Error; err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE ai_run r SET status='interrupted',terminal_reason='lease_expired',finished_at=CURRENT_TIMESTAMP,error_message='触发运行租约已过期' FROM ai_agent_trigger_job j WHERE j.tenant_id=? AND j.run_id=r.id AND j.status='running' AND r.status IN ('running','stopping') AND r.lease_until<=CURRENT_TIMESTAMP`, assets.DefaultTenantID).Error; err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE ai_agent_trigger_job j SET status=CASE WHEN r.status='completed' THEN 'completed' WHEN r.status='stopped' THEN 'cancelled' ELSE 'failed' END,error_message=r.error_message,updated_at=CURRENT_TIMESTAMP FROM ai_run r WHERE j.tenant_id=? AND j.run_id=r.id AND j.status='running' AND r.status NOT IN ('running','stopping')`, assets.DefaultTenantID).Error; err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE ai_agent_trigger_job SET status='queued',updated_at=CURRENT_TIMESTAMP WHERE tenant_id=? AND status='starting' AND run_id IS NULL AND updated_at<CURRENT_TIMESTAMP-INTERVAL '30 seconds'`, assets.DefaultTenantID).Error; err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE ai_agent_trigger_job j SET status='cancelled',error_message='触发来源或账号已不可用',updated_at=CURRENT_TIMESTAMP WHERE j.tenant_id=? AND j.status IN ('queued','starting') AND (NOT EXISTS(SELECT 1 FROM tenant_membership m JOIN auth_user u ON u.id=m.user_id JOIN tenant t ON t.id=m.tenant_id WHERE m.tenant_id=j.tenant_id AND m.user_id=j.owner_user_id AND m.role IN ('admin','analyst') AND u.is_active=TRUE AND t.status='active') OR j.source_task_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM ai_task task LEFT JOIN target target ON target.id=task.target_id WHERE task.id=j.source_task_id AND task.owner_user_id=j.owner_user_id AND task.deleted_at IS NULL AND (task.original_target_id IS NULL OR target.id IS NOT NULL AND target.deleted_at IS NULL)))`, assets.DefaultTenantID).Error; err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE ai_agent_trigger_job j SET status='cancelled',error_message='复测 Agent 已删除',updated_at=CURRENT_TIMESTAMP WHERE j.tenant_id=? AND j.event_key LIKE 'retest:%' AND j.status IN ('queued','starting') AND NOT EXISTS(SELECT 1 FROM ai_custom_agent a WHERE a.tenant_id=j.tenant_id AND a.agent_key=j.agent_key)`, assets.DefaultTenantID).Error; err != nil {
			return err
		}
		return tx.Exec(`UPDATE ai_finding_retest r SET status='failed',error=j.error_message,finished_at=CURRENT_TIMESTAMP FROM ai_agent_trigger_job j WHERE r.tenant_id=? AND r.status='pending' AND j.tenant_id=r.tenant_id AND j.conversation_id=r.conversation_id AND j.event_key='retest:'||r.id::text AND j.status IN ('failed','cancelled')`, assets.DefaultTenantID).Error
	})
}
func (s *AgentTriggerStore) ClaimAgentJob(ctx context.Context) (*aiapp.AgentTriggerJob, error) {
	var job *aiapp.AgentTriggerJob
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked bool
		if err := tx.Raw(`SELECT pg_try_advisory_xact_lock(?,?)`, 0x4147454e, 0x4a4f4253).Scan(&locked).Error; err != nil {
			return err
		}
		if !locked {
			return nil
		}
		var candidates []struct {
			ID                                          int64
			OwnerUserID                                 int
			AgentKey, Message, Title, Options, EventKey string
			ConversationID                              *string
		}
		if err := tx.Raw(`SELECT j.id,j.owner_user_id,j.agent_key,j.message,j.title,j.event_key,COALESCE(o.options::text,'{}') options,j.conversation_id::text FROM ai_agent_trigger_job j JOIN ai_custom_agent a ON a.tenant_id=j.tenant_id AND a.agent_key=j.agent_key LEFT JOIN ai_agent_option o ON o.tenant_id=j.tenant_id AND o.agent_key=j.agent_key JOIN tenant_membership m ON m.tenant_id=j.tenant_id AND m.user_id=j.owner_user_id JOIN auth_user u ON u.id=m.user_id WHERE j.tenant_id=? AND j.status='queued' AND NOT (j.agent_key='reporter' AND j.event_key LIKE 'finding-case-review:%') AND a.enabled=TRUE AND m.role IN ('admin','analyst') AND u.is_active=TRUE AND NOT EXISTS(SELECT 1 FROM ai_run r WHERE r.conversation_id=j.conversation_id AND r.status='running' AND r.lease_until>CURRENT_TIMESTAMP) ORDER BY j.id FOR UPDATE OF j SKIP LOCKED LIMIT 64`, assets.DefaultTenantID).Scan(&candidates).Error; err != nil {
			return err
		}
		for _, candidate := range candidates {
			options := airuntime.DefaultAgentOptions(candidate.AgentKey)
			if err := json.Unmarshal([]byte(candidate.Options), &options); err != nil {
				return err
			}
			var active int64
			if err := tx.Raw(`SELECT count(*) FROM ai_agent_trigger_job WHERE tenant_id=? AND agent_key=? AND status IN ('starting','running')`, assets.DefaultTenantID, candidate.AgentKey).Scan(&active).Error; err != nil {
				return err
			}
			limit := 1
			if options.TriggerRunMode == "parallel" {
				limit = options.TriggerMaxParallel
			}
			if !strings.HasPrefix(candidate.EventKey, "retest:") && limit > 0 && active >= int64(limit) {
				continue
			}
			conversation := uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("skyshield/agent-trigger/%s/%d", assets.DefaultTenantID, candidate.ID))).String()
			if candidate.ConversationID != nil {
				conversation = *candidate.ConversationID
			}
			if err := tx.Exec(`INSERT INTO ai_conversation(id,tenant_id,owner_user_id,title,agent_key) VALUES(?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, conversation, assets.DefaultTenantID, candidate.OwnerUserID, candidate.Title, candidate.AgentKey).Error; err != nil {
				return err
			}
			if err := tx.Exec(`UPDATE ai_agent_trigger_job SET status='starting',conversation_id=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`, conversation, candidate.ID).Error; err != nil {
				return err
			}
			job = &aiapp.AgentTriggerJob{ID: candidate.ID, OwnerUserID: candidate.OwnerUserID, AgentKey: candidate.AgentKey, ConversationID: conversation, Message: candidate.Message}
			return nil
		}
		return nil
	})
	return job, err
}
func (s *AgentTriggerStore) ReleaseAgentJob(ctx context.Context, id int64, waiting bool, message string) error {
	status := "failed"
	if waiting {
		status = "queued"
	}
	return s.db.WithContext(ctx).Exec(`UPDATE ai_agent_trigger_job SET status=?,error_message=?,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=? AND id=? AND status='starting'`, status, message, assets.DefaultTenantID, id).Error
}

var _ aiapp.AgentTriggerJobStore = (*AgentTriggerStore)(nil)

package repository

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

type ConversationStore struct{ db *gorm.DB }

func NewConversationStore(db *gorm.DB) *ConversationStore { return &ConversationStore{db: db} }

func (s *ConversationStore) CreateConversation(ctx context.Context, user int, id, title string) (*aiapp.Conversation, error) {
	return s.CreateAgentConversation(ctx, user, id, title, "assistant")
}

func (s *ConversationStore) CreateAgentConversation(ctx context.Context, user int, id, title, agentKey string) (*aiapp.Conversation, error) {
	var result aiapp.Conversation
	err := s.db.WithContext(ctx).Raw(`INSERT INTO ai_conversation(id,tenant_id,owner_user_id,title,agent_key)
		SELECT ?,m.tenant_id,m.user_id,?,? FROM tenant_membership m
		JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
		WHERE m.tenant_id=? AND m.user_id=? AND t.status='active' AND u.is_active=TRUE
		RETURNING id::text,title,agent_key,created_at,updated_at,FALSE AS running`, id, title, agentKey, assetapp.DefaultTenantID, user).
		Row().Scan(&result.ID, &result.Title, &result.AgentKey, &result.CreatedAt, &result.UpdatedAt, &result.Running)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, aiapp.ErrConversationNotFound
	}
	return &result, err
}

func (s *ConversationStore) ConversationAgent(ctx context.Context, user int, conversation string) (string, error) {
	var agentKey string
	err := s.db.WithContext(ctx).Raw(`SELECT c.agent_key FROM ai_conversation c
		JOIN tenant_membership m ON m.tenant_id=c.tenant_id AND m.user_id=c.owner_user_id
		JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
		WHERE c.tenant_id=? AND c.owner_user_id=? AND c.id=? AND t.status='active' AND u.is_active=TRUE AND tianqiong_ai.finding_case_review_conversation_visible(c.id)`,
		assetapp.DefaultTenantID, user, conversation).Row().Scan(&agentKey)
	if errors.Is(err, sql.ErrNoRows) {
		return "", aiapp.ErrConversationNotFound
	}
	return agentKey, err
}

func (s *ConversationStore) ListConversations(ctx context.Context, user int) ([]aiapp.Conversation, error) {
	var items []aiapp.Conversation
	err := s.db.WithContext(ctx).Raw(`SELECT c.id::text,c.title,c.agent_key,b.profile_id::text AS llm_profile_id,c.pinned,c.created_at,c.updated_at,
		EXISTS(SELECT 1 FROM ai_run r WHERE r.conversation_id=c.id AND r.status IN ('running','stopping') AND r.lease_until>clock_timestamp()) AS running,
		EXISTS(SELECT 1 FROM ai_finding_case_review review WHERE review.conversation_id=c.id AND review.tenant_id=c.tenant_id AND review.owner_user_id=c.owner_user_id) AS review_read_only
		FROM ai_conversation c LEFT JOIN ai_conversation_llm_binding b ON b.tenant_id=c.tenant_id AND b.conversation_id=c.id
		JOIN tenant_membership m ON m.tenant_id=c.tenant_id AND m.user_id=c.owner_user_id
		JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
		WHERE c.tenant_id=? AND c.owner_user_id=? AND t.status='active' AND u.is_active=TRUE AND tianqiong_ai.finding_case_review_conversation_visible(c.id)
		ORDER BY c.pinned DESC,c.updated_at DESC,c.id LIMIT 200`, assetapp.DefaultTenantID, user).Scan(&items).Error
	return items, err
}

func (s *ConversationStore) UpdateConversationMetadata(ctx context.Context, user int, conversation string, title *string, pinned *bool) error {
	result := s.db.WithContext(ctx).Exec(`UPDATE ai_conversation c SET title=COALESCE(?,c.title),pinned=COALESCE(?,c.pinned),updated_at=CURRENT_TIMESTAMP
		WHERE c.tenant_id=? AND c.owner_user_id=? AND c.id=? AND tianqiong_ai.finding_case_review_conversation_visible(c.id) AND EXISTS(
		SELECT 1 FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
		WHERE m.tenant_id=c.tenant_id AND m.user_id=c.owner_user_id AND t.status='active' AND u.is_active=TRUE)`,
		title, pinned, assetapp.DefaultTenantID, user, conversation)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return aiapp.ErrConversationNotFound
	}
	return nil
}

func (s *ConversationStore) ConversationProfile(ctx context.Context, user int, conversation string) (*string, error) {
	var profile sql.NullString
	err := s.db.WithContext(ctx).Raw(`SELECT b.profile_id::text FROM ai_conversation c
		LEFT JOIN ai_conversation_llm_binding b ON b.tenant_id=c.tenant_id AND b.conversation_id=c.id
		JOIN tenant_membership m ON m.tenant_id=c.tenant_id AND m.user_id=c.owner_user_id
		JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
		WHERE c.tenant_id=? AND c.owner_user_id=? AND c.id=? AND t.status='active' AND u.is_active=TRUE AND tianqiong_ai.finding_case_review_conversation_visible(c.id)`,
		assetapp.DefaultTenantID, user, conversation).Row().Scan(&profile)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, aiapp.ErrConversationNotFound
	}
	if err != nil || !profile.Valid {
		return nil, err
	}
	return &profile.String, nil
}

func (s *ConversationStore) SetConversationProfile(ctx context.Context, user int, conversation string, profile *string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockTaskProfileTenant(tx); err != nil {
			return err
		}
		var id string
		err := tx.Raw(`SELECT c.id::text FROM ai_conversation c
			JOIN tenant_membership m ON m.tenant_id=c.tenant_id AND m.user_id=c.owner_user_id
			JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
			WHERE c.tenant_id=? AND c.owner_user_id=? AND c.id=? AND t.status='active' AND u.is_active=TRUE AND tianqiong_ai.finding_case_review_conversation_visible(c.id) FOR UPDATE OF c`, assetapp.DefaultTenantID, user, conversation).Row().Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return aiapp.ErrConversationNotFound
		}
		if err != nil {
			return err
		}
		if profile == nil {
			return tx.Exec(`DELETE FROM ai_conversation_llm_binding WHERE tenant_id=? AND conversation_id=?`, assetapp.DefaultTenantID, conversation).Error
		}
		var exists bool
		if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_llm_profile WHERE tenant_id=? AND id=?)`, assetapp.DefaultTenantID, *profile).Scan(&exists).Error; err != nil {
			return err
		}
		if !exists {
			return aiapp.ErrInvalidInput
		}
		return tx.Exec(`INSERT INTO ai_conversation_llm_binding(tenant_id,conversation_id,owner_user_id,profile_id) VALUES(?,?,?,?)
			ON CONFLICT(tenant_id,conversation_id) DO UPDATE SET profile_id=EXCLUDED.profile_id`, assetapp.DefaultTenantID, conversation, user, *profile).Error
	})
}

func (s *ConversationStore) BeginRun(ctx context.Context, user int, conversation, run, instance, message string) error {
	return s.BeginRunWithActivity(ctx, user, conversation, run, instance, airuntime.Activity{Kind: "user", Summary: message, Detail: message})
}
func (s *ConversationStore) BeginRunWithActivity(ctx context.Context, user int, conversation, run, instance string, activity airuntime.Activity) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var id string
		err := tx.Raw(`SELECT c.id::text FROM ai_conversation c
			JOIN tenant_membership m ON m.tenant_id=c.tenant_id AND m.user_id=c.owner_user_id
			JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
			WHERE c.tenant_id=? AND c.owner_user_id=? AND c.id=? AND t.status='active' AND u.is_active=TRUE AND tianqiong_ai.finding_case_review_conversation_visible(c.id) FOR UPDATE OF c`,
			assetapp.DefaultTenantID, user, conversation).Row().Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return aiapp.ErrConversationNotFound
		}
		if err != nil {
			return err
		}
		jobID := aiapp.AgentTriggerJobFromContext(ctx)
		var review struct {
			JobID *int64
			State string
		}
		reviewQuery := tx.Raw(`SELECT job_id,state FROM ai_finding_case_review WHERE tenant_id=?::uuid AND owner_user_id=? AND conversation_id=?::uuid`, assetapp.DefaultTenantID, user, conversation).Scan(&review)
		if reviewQuery.Error != nil {
			return reviewQuery.Error
		}
		if reviewQuery.RowsAffected > 0 && (jobID <= 0 || review.JobID == nil || *review.JobID != jobID || review.State != "queued") {
			return aiapp.ErrConversationBusy
		}
		// A queued retest must be started by its persisted dispatcher job;
		// otherwise an ordinary message could consume its one-shot session.
		if jobID == 0 {
			var pendingRetest bool
			if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_finding_retest WHERE tenant_id=? AND owner_user_id=? AND conversation_id=? AND status='pending')`, assetapp.DefaultTenantID, user, conversation).Scan(&pendingRetest).Error; err != nil {
				return err
			}
			if pendingRetest {
				return aiapp.ErrConversationBusy
			}
		}
		if err := tx.Exec(`UPDATE ai_run SET status='interrupted',finished_at=CURRENT_TIMESTAMP,error_message='运行租约已过期'
			WHERE conversation_id=? AND status IN ('running','stopping') AND lease_until<=clock_timestamp()`, conversation).Error; err != nil {
			return err
		}
		var active bool
		if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_run WHERE conversation_id=? AND status IN ('running','stopping'))`, conversation).Scan(&active).Error; err != nil {
			return err
		}
		if active {
			return aiapp.ErrConversationBusy
		}
		if jobID > 0 {
			if err := tx.Exec(`SELECT set_config('tianqiong.trigger_job_id',?,TRUE)`, strconv.FormatInt(jobID, 10)).Error; err != nil {
				return err
			}
		}
		if err := tx.Exec(`INSERT INTO ai_run(id,tenant_id,conversation_id,owner_user_id,status,lease_owner,lease_until)
			VALUES(?,?,?,?,'running',?,CURRENT_TIMESTAMP + INTERVAL '45 seconds')`, run, assetapp.DefaultTenantID, conversation, user, instance).Error; err != nil {
			return err
		}
		if jobID > 0 {
			result := tx.Exec(`UPDATE ai_agent_trigger_job j SET status='running',run_id=?,error_message='',updated_at=CURRENT_TIMESTAMP WHERE j.id=? AND j.tenant_id=? AND j.owner_user_id=? AND j.conversation_id=? AND j.status='starting' AND (j.source_task_id IS NULL OR EXISTS(SELECT 1 FROM ai_task task LEFT JOIN target target ON target.id=task.target_id WHERE task.id=j.source_task_id AND task.owner_user_id=j.owner_user_id AND task.deleted_at IS NULL AND (task.original_target_id IS NULL OR target.id IS NOT NULL AND target.deleted_at IS NULL)))`, run, jobID, assetapp.DefaultTenantID, user, conversation)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return aiapp.ErrConversationBusy
			}
		}
		if err := tx.Exec(`INSERT INTO ai_conversation_activity(tenant_id,conversation_id,owner_user_id,run_id,kind,summary,detail)
			VALUES(?,?,?,?,'user',?,?)`, assetapp.DefaultTenantID, conversation, user, run, conversationPreview(activity.Summary, 200), printableActivityText(activity.Detail)).Error; err != nil {
			return err
		}
		return tx.Exec(`UPDATE ai_conversation SET updated_at=CURRENT_TIMESTAMP,title=CASE WHEN title IN ('新对话','New conversation','') AND (SELECT COUNT(*) FROM ai_conversation_activity WHERE conversation_id=? AND kind='user')=1 THEN ? ELSE title END WHERE id=?`, conversation, conversationPreview(activity.Summary, 40), conversation).Error
	})
}

func conversationPreview(value string, limit int) string {
	value = strings.TrimSpace(printableActivityText(value))
	if i := strings.IndexAny(value, "\r\n"); i >= 0 {
		value = value[:i]
	}
	if len([]rune(value)) > limit {
		return string([]rune(value)[:limit]) + "…"
	}
	return value
}

func (s *ConversationStore) AppendActivity(ctx context.Context, user int, conversation, run, instance string, event airuntime.Activity) error {
	event.Summary = printableActivityText(event.Summary)
	event.Detail = printableActivityText(event.Detail)
	event.Tool = printableActivityText(event.Tool)
	event.ToolUseID = printableActivityText(event.ToolUseID)
	if len(event.Detail) > 1024*1024 {
		event.Detail = string([]rune(event.Detail)[:min(len([]rune(event.Detail)), 250000)])
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		liveText, liveKind := "", ""
		if event.Kind == "stream" {
			liveText, liveKind = event.Detail, event.Tool
			text := []rune(liveText)
			if len(text) > 8000 {
				liveText = string(text[len(text)-8000:])
			}
		}
		// Lock before allocating the event sequence: concurrent tool callbacks
		// must commit in cursor order, so a poll cannot skip a late smaller ID.
		result := tx.Exec(`UPDATE ai_run r SET last_progress_at=clock_timestamp(),
			live_text=CASE WHEN ?='usage' THEN live_text ELSE ? END,
			live_kind=CASE WHEN ?='usage' THEN live_kind ELSE ? END
			WHERE r.tenant_id=? AND r.owner_user_id=? AND r.conversation_id=? AND r.id=? AND r.lease_owner=?
			AND r.status='running' AND r.lease_until>clock_timestamp() AND EXISTS(
			SELECT 1 FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
			WHERE m.tenant_id=r.tenant_id AND m.user_id=r.owner_user_id AND t.status='active' AND u.is_active=TRUE)`,
			event.Kind, liveText, event.Kind, liveKind, assetapp.DefaultTenantID, user, conversation, run, instance)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return aiapp.ErrRunLeaseLost
		}
		if event.Kind == "stream" {
			return nil
		}
		return tx.Exec(`INSERT INTO ai_conversation_activity
			(tenant_id,conversation_id,owner_user_id,run_id,kind,tool,tool_use_id,is_error,summary,detail,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, assetapp.DefaultTenantID, conversation, user, run, event.Kind, event.Tool, event.ToolUseID, event.IsError, event.Summary, event.Detail,
			event.InputTokens, event.OutputTokens, event.CacheReadTokens, event.CacheWriteTokens).Error
	})
}

func (s *ConversationStore) Heartbeat(ctx context.Context, user int, conversation, run, instance string) (bool, error) {
	result := s.db.WithContext(ctx).Exec(`UPDATE ai_run r SET lease_until=CURRENT_TIMESTAMP + INTERVAL '45 seconds'
		WHERE r.tenant_id=? AND r.owner_user_id=? AND r.conversation_id=? AND r.id=? AND r.lease_owner=?
		AND r.status='running' AND r.lease_until>CURRENT_TIMESTAMP AND EXISTS (
			SELECT 1 FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
			WHERE m.tenant_id=r.tenant_id AND m.user_id=r.owner_user_id AND t.status='active' AND u.is_active=TRUE)`,
		assetapp.DefaultTenantID, user, conversation, run, instance)
	return result.RowsAffected == 1, result.Error
}

func (s *ConversationStore) FinishRun(ctx context.Context, user int, conversation, run, instance, status string) error {
	message := ""
	if status == "failed" {
		message = "模型调用或活动记录失败，请检查服务日志和模型配置。"
	}
	return s.FinishRunOutcome(ctx, user, conversation, run, instance, status, "", message)
}

func (s *ConversationStore) FinishRunOutcome(ctx context.Context, user int, conversation, run, instance, status, reason, message string) error {
	result := s.db.WithContext(ctx).Exec(`UPDATE ai_run SET
		status=CASE WHEN status='stopping' THEN 'stopped' ELSE ? END,
		terminal_reason=CASE WHEN status='stopping' THEN 'user_stop' ELSE ? END,
		error_message=CASE WHEN status='stopping' THEN '' ELSE ? END,live_text='',live_kind='',finished_at=clock_timestamp()
		WHERE tenant_id=? AND owner_user_id=? AND conversation_id=? AND id=? AND lease_owner=?
		AND status IN ('running','stopping') AND lease_until>clock_timestamp()`, status, reason, message, assetapp.DefaultTenantID, user, conversation, run, instance)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return aiapp.ErrRunLeaseLost
	}
	return nil
}

func (s *ConversationStore) StopRun(ctx context.Context, user int, conversation string) (string, error) {
	var run string
	err := s.db.WithContext(ctx).Raw(`UPDATE ai_run r SET status='stopping',terminal_reason='user_stop'
		WHERE r.tenant_id=? AND r.owner_user_id=? AND r.conversation_id=? AND r.status IN ('running','stopping')
		AND EXISTS(SELECT 1 FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
			WHERE m.tenant_id=r.tenant_id AND m.user_id=r.owner_user_id AND t.status='active' AND u.is_active=TRUE)
		RETURNING r.id::text`, assetapp.DefaultTenantID, user, conversation).Row().Scan(&run)
	if errors.Is(err, sql.ErrNoRows) {
		return "", aiapp.ErrConversationNotFound
	}
	return run, err
}

func (s *ConversationStore) ListActivities(ctx context.Context, user int, conversation string, after int64) (*aiapp.ActivityPage, error) {
	return s.listActivities(ctx, user, conversation, after, true)
}

func (s *ConversationStore) ListActivityMetadata(ctx context.Context, user int, conversation string, after int64) (*aiapp.ActivityPage, error) {
	return s.listActivities(ctx, user, conversation, after, false)
}

func (s *ConversationStore) listActivities(ctx context.Context, user int, conversation string, after int64, detail bool) (*aiapp.ActivityPage, error) {
	var found bool
	err := s.db.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM ai_conversation c
		JOIN tenant_membership m ON m.tenant_id=c.tenant_id AND m.user_id=c.owner_user_id
		JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
		WHERE c.tenant_id=? AND c.owner_user_id=? AND c.id=? AND t.status='active' AND u.is_active=TRUE AND tianqiong_ai.finding_case_review_conversation_visible(c.id))`,
		assetapp.DefaultTenantID, user, conversation).Scan(&found).Error
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, aiapp.ErrConversationNotFound
	}
	page := &aiapp.ActivityPage{Items: []aiapp.ConversationActivity{}}
	detailColumn := "''"
	if detail {
		detailColumn = "a.detail"
	}
	if err := s.db.WithContext(ctx).Raw(`SELECT a.id,a.run_id::text,c.agent_key AS worker,a.kind,a.tool,a.tool_use_id,a.is_error,a.summary,`+detailColumn+` AS detail,a.created_at,a.input_tokens,a.output_tokens,a.cache_read_tokens,a.cache_write_tokens
		FROM ai_conversation_activity a JOIN ai_conversation c ON c.id=a.conversation_id WHERE a.tenant_id=? AND a.owner_user_id=? AND a.conversation_id=? AND a.id>? AND tianqiong_ai.finding_case_review_conversation_visible(c.id)
		ORDER BY a.id LIMIT 201`, assetapp.DefaultTenantID, user, conversation, after).Scan(&page.Items).Error; err != nil {
		return nil, err
	}
	page.HasMore = len(page.Items) > 200
	if page.HasMore {
		page.Items = page.Items[:200]
	}
	if err = s.readConversationRun(ctx, user, conversation, page); err != nil {
		return nil, err
	}

	if _, verifyErr := s.TrafficConversationTenant(ctx, user, conversation); verifyErr != nil {
		if errors.Is(verifyErr, aiapp.ErrTaskNotFound) {
			return nil, aiapp.ErrConversationNotFound
		}
		return nil, verifyErr
	}
	page.Running = page.RunStatus == "running" || page.RunStatus == "stopping"
	return page, nil
}

func (s *ConversationStore) ListToolExecutions(ctx context.Context, user int, query string, before int64, limit int, detail bool) ([]aiapp.ToolExecution, error) {
	var items []aiapp.ToolExecution
	comparison := "(? = 0 OR u.id < ?)"
	inputExpr, outputExpr := "left(u.detail,500)", "left(COALESCE(result.detail,''),500)"
	if detail {
		comparison = "(? > 0 AND u.id = ?)"
		inputExpr, outputExpr = "u.detail", "COALESCE(result.detail,'')"
	}
	// The matching result must occur after this use and before any later use of
	// the same tool-call ID in the same run. This prevents cross-run pairing.
	statement := `SELECT u.id,u.conversation_id::text,c.title AS conversation,u.run_id::text,u.tool,` + inputExpr + ` AS input,` + outputExpr + ` AS output,
		CASE WHEN result.id IS NULL THEN CASE WHEN r.status='running' AND r.lease_until>CURRENT_TIMESTAMP THEN 'running' ELSE 'unknown' END
			WHEN result.is_error THEN 'failed' ELSE 'succeeded' END AS status,u.created_at
		FROM ai_conversation_activity u JOIN ai_conversation c ON c.id=u.conversation_id
		JOIN ai_run r ON r.id=u.run_id JOIN tenant_membership m ON m.tenant_id=u.tenant_id AND m.user_id=u.owner_user_id
		JOIN tenant t ON t.id=m.tenant_id JOIN auth_user a ON a.id=m.user_id
		LEFT JOIN LATERAL (
			SELECT x.id,x.detail,x.is_error FROM ai_conversation_activity x
			WHERE x.tenant_id=u.tenant_id AND x.conversation_id=u.conversation_id AND x.run_id=u.run_id
			AND x.tool_use_id=u.tool_use_id AND x.kind='tool_result' AND x.id>u.id
			AND NOT EXISTS (SELECT 1 FROM ai_conversation_activity later WHERE later.run_id=u.run_id
				AND later.conversation_id=u.conversation_id AND later.tool_use_id=u.tool_use_id AND later.kind='tool_use'
				AND later.id>u.id AND later.id<x.id)
			ORDER BY x.id LIMIT 1
		) result ON TRUE
		WHERE u.tenant_id=? AND u.owner_user_id=? AND u.kind='tool_use' AND t.status='active' AND a.is_active=TRUE AND tianqiong_ai.finding_case_review_conversation_visible(c.id)
		AND (?='' OR u.tool ILIKE ? ESCAPE '\' OR u.detail ILIKE ? ESCAPE '\') AND ` + comparison + `
		ORDER BY u.id DESC LIMIT ?`
	pattern := "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(query) + "%"
	err := s.db.WithContext(ctx).Raw(statement, assetapp.DefaultTenantID, user, query, pattern, pattern, before, before, limit).Scan(&items).Error
	return items, err
}

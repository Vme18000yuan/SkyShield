package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

const retestColumns = `r.id,r.finding_id,r.conversation_id::text AS conversation_id,r.status,r.verdict,r.notes,r.summary,r.evidence,r.error,r.created_at,r.started_at,r.finished_at`

func (s *TaskStore) CreateFindingRetest(ctx context.Context, user int, findingID int64, notes string) (*aiapp.FindingRetest, bool, error) {
	var result aiapp.FindingRetest
	created := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := scheduleWriteRole(tx, user); err != nil {
			return err
		}
		var taskID int64
		var title string
		var snapshot []byte
		err := tx.Raw(`SELECT task.id,COALESCE(NULLIF(f.name,''),NULLIF(f.vulnclass,''),'未分类'),jsonb_build_object('finding',to_jsonb(f),'task',jsonb_build_object('id',task.id::text,'name',task.name,'goal',task.goal,'description',task.description,'target_id',task.target_id),'assets',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',a.id::text,'type',a.asset_type,'label',a.label,'attributes',a.attributes) ORDER BY a.id) FROM tianqiong_ai.exploration_anchors anchor JOIN asset_entity a ON a.id=anchor.asset_id AND a.tenant_id=task.tenant_id WHERE anchor.node_id=f.node_id),'[]'::jsonb),'constraints',COALESCE((SELECT jsonb_agg(to_jsonb(c) ORDER BY c.id) FROM tianqiong_ai.task_constraints c WHERE c.exploration_id=task.exploration_id),'[]'::jsonb)) FROM tianqiong_ai.findings f JOIN ai_task task ON task.id=f.task_id WHERE f.id=? AND task.tenant_id=? AND task.owner_user_id=? AND task.deleted_at IS NULL AND (task.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=task.target_id AND deleted_at IS NULL)) FOR UPDATE OF f FOR SHARE OF task`, findingID, assets.DefaultTenantID, user).Row().Scan(&taskID, &title, &snapshot)
		if errors.Is(err, sql.ErrNoRows) {
			return aiapp.ErrTaskNotFound
		}
		if err != nil {
			return err
		}
		row := tx.Raw(`SELECT `+retestColumns+` FROM ai_finding_retest r WHERE r.finding_id=? AND r.tenant_id=? AND r.owner_user_id=? AND r.status IN ('pending','running')`, findingID, assets.DefaultTenantID, user).Scan(&result)
		if row.Error != nil {
			return row.Error
		}
		if row.RowsAffected > 0 {
			return nil
		}
		var ready bool
		if err = tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_custom_agent WHERE tenant_id=? AND agent_key='retester' AND enabled=TRUE) AND (SELECT count(*) FROM ai_tool_catalog WHERE tenant_id=? AND key IN ('get_finding_retest_context','record_finding_retest_result') AND enabled=TRUE AND agents @> '["retester"]'::jsonb)=2`, assets.DefaultTenantID, assets.DefaultTenantID).Scan(&ready).Error; err != nil {
			return err
		}
		if !ready {
			return aiapp.ErrRetestUnavailable
		}
		titleRunes := []rune(title)
		if len(titleRunes) > 100 {
			title = string(titleRunes[:100])
		}
		conversationID := uuid.NewString()
		conversationTitle := fmt.Sprintf("复测 #%d · %s", findingID, title)
		if err = tx.Exec(`INSERT INTO ai_conversation(id,tenant_id,owner_user_id,title,agent_key) VALUES(?,?,?,?,'retester')`, conversationID, assets.DefaultTenantID, user, conversationTitle).Error; err != nil {
			return err
		}
		if err = tx.Raw(`INSERT INTO ai_finding_retest(tenant_id,owner_user_id,finding_id,conversation_id,notes,snapshot) VALUES(?,?,?,?,?,?::jsonb) RETURNING id`, assets.DefaultTenantID, user, findingID, conversationID, notes, string(snapshot)).Row().Scan(&result.ID); err != nil {
			return err
		}
		message := fmt.Sprintf("请复测漏洞 #%d。先调用 get_finding_retest_context 读取本会话关联的原始证据与约束，再执行针对性验证，最后调用 record_finding_retest_result 保存结论。", findingID)
		if notes != "" {
			message += "\n\n本次复测补充说明：\n" + notes
		}
		if err = tx.Exec(`INSERT INTO ai_agent_trigger_job(tenant_id,agent_key,owner_user_id,source_task_id,event_key,title,message,conversation_id) VALUES(?,'retester',?,?,?,?,?,?)`, assets.DefaultTenantID, user, taskID, fmt.Sprintf("retest:%d", result.ID), conversationTitle, message, conversationID).Error; err != nil {
			return err
		}
		row = tx.Raw(`SELECT `+retestColumns+` FROM ai_finding_retest r WHERE r.id=?`, result.ID).Scan(&result)
		if row.Error != nil {
			return row.Error
		}
		if row.RowsAffected != 1 {
			return aiapp.ErrRetestUnavailable
		}
		created = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &result, created, nil
}
func (s *TaskStore) ListFindingRetests(ctx context.Context, user int, findingID int64) ([]aiapp.FindingRetest, error) {
	var allowed bool
	if err := s.db.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM tianqiong_ai.findings f JOIN ai_task t ON t.id=f.task_id JOIN tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id JOIN tenant tenant ON tenant.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE f.id=? AND t.tenant_id=? AND t.owner_user_id=? AND t.deleted_at IS NULL AND tenant.status='active' AND u.is_active=TRUE)`, findingID, assets.DefaultTenantID, user).Scan(&allowed).Error; err != nil {
		return nil, err
	}
	if !allowed {
		return nil, aiapp.ErrTaskNotFound
	}
	items := []aiapp.FindingRetest{}
	err := s.db.WithContext(ctx).Raw(`SELECT `+retestColumns+` FROM ai_finding_retest r WHERE r.finding_id=? AND r.tenant_id=? AND r.owner_user_id=? ORDER BY r.id DESC LIMIT 200`, findingID, assets.DefaultTenantID, user).Scan(&items).Error
	return items, err
}
func (s *TaskStore) ListActiveFindingRetests(ctx context.Context, user int) ([]aiapp.FindingRetest, error) {
	items := []aiapp.FindingRetest{}
	err := s.db.WithContext(ctx).Raw(`SELECT `+retestColumns+` FROM ai_finding_retest r JOIN ai_task t ON t.id=(SELECT f.task_id FROM tianqiong_ai.findings f WHERE f.id=r.finding_id) JOIN tenant_membership m ON m.tenant_id=r.tenant_id AND m.user_id=r.owner_user_id JOIN tenant tenant ON tenant.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE r.tenant_id=? AND r.owner_user_id=? AND t.owner_user_id=r.owner_user_id AND t.deleted_at IS NULL AND r.status IN ('pending','running') AND tenant.status='active' AND u.is_active=TRUE ORDER BY r.id DESC LIMIT 200`, assets.DefaultTenantID, user).Scan(&items).Error
	return items, err
}

func (s *TaskStore) ListVulnerabilityFindings(ctx context.Context, user int, id int64) ([]aiapp.RetestFinding, error) {
	items := []aiapp.RetestFinding{}
	err := s.db.WithContext(ctx).Raw(`SELECT f.id,f.node_id,t.id AS task_id,COALESCE(NULLIF(f.name,''),NULLIF(f.vulnclass,''),'未分类') AS name,f.status FROM ai_finding_risk_link link JOIN vulnerability v ON v.id=link.vulnerability_id JOIN target target ON target.id=v.target_id AND target.deleted_at IS NULL JOIN tianqiong_ai.findings f ON f.id=link.finding_id JOIN ai_task t ON t.id=link.task_id JOIN tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id JOIN tenant tenant ON tenant.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE v.id::bigint=?::bigint AND t.tenant_id=? AND t.owner_user_id=? AND t.deleted_at IS NULL AND tenant.status='active' AND u.is_active=TRUE AND f.node_id IS NOT NULL ORDER BY f.id DESC LIMIT 200`, id, assets.DefaultTenantID, user).Scan(&items).Error
	return items, err
}
func (s *TaskStore) ReadRetestContext(ctx context.Context, user int, runID string) (*aiapp.FindingRetestContext, error) {
	var out aiapp.FindingRetestContext
	var snapshot, constraints []byte
	row := s.db.WithContext(ctx).Raw(`SELECT `+retestColumns+`,r.snapshot::text,COALESCE((SELECT jsonb_agg(to_jsonb(c) ORDER BY c.id) FROM tianqiong_ai.task_constraints c WHERE c.exploration_id=t.exploration_id),'[]'::jsonb)::text FROM ai_finding_retest r JOIN ai_run run ON run.id=r.run_id JOIN tianqiong_ai.findings f ON f.id=r.finding_id JOIN ai_task t ON t.id=f.task_id AND t.tenant_id=r.tenant_id AND t.owner_user_id=r.owner_user_id JOIN tenant_membership m ON m.tenant_id=r.tenant_id AND m.user_id=r.owner_user_id JOIN tenant tenant ON tenant.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE r.tenant_id=? AND r.owner_user_id=? AND r.run_id=? AND r.status='running' AND run.status='running' AND run.lease_until>clock_timestamp() AND t.deleted_at IS NULL AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=t.target_id AND deleted_at IS NULL)) AND tenant.status='active' AND u.is_active=TRUE`, assets.DefaultTenantID, user, runID).Row()
	err := row.Scan(&out.Retest.ID, &out.Retest.FindingID, &out.Retest.ConversationID, &out.Retest.Status, &out.Retest.Verdict, &out.Retest.Notes, &out.Retest.Summary, &out.Retest.Evidence, &out.Retest.Error, &out.Retest.CreatedAt, &out.Retest.StartedAt, &out.Retest.FinishedAt, &snapshot, &constraints)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, aiapp.ErrRetestNotRunning
	}
	if err != nil {
		return nil, err
	}
	out.Snapshot = json.RawMessage(snapshot)
	out.CurrentConstraints = json.RawMessage(constraints)
	return &out, nil
}
func (s *TaskStore) RecordRetestResult(ctx context.Context, user int, runID, verdict, summary, evidence string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := scheduleWriteRole(tx, user); err != nil {
			return err
		}
		var found string
		err := tx.Raw(`SELECT id::text FROM ai_run WHERE tenant_id=? AND owner_user_id=? AND id=? AND status='running' AND lease_until>clock_timestamp() FOR SHARE`, assets.DefaultTenantID, user, runID).Row().Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return aiapp.ErrRetestNotRunning
		}
		if err != nil {
			return err
		}
		result := tx.Exec(`UPDATE ai_finding_retest SET verdict=?,summary=?,evidence=? WHERE tenant_id=? AND owner_user_id=? AND run_id=? AND status='running' AND (verdict='' OR (verdict=? AND summary=? AND evidence=?))`, verdict, summary, evidence, assets.DefaultTenantID, user, runID, verdict, summary, evidence)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return aiapp.ErrRetestNotRunning
		}
		return nil
	})
}

var _ aiapp.FindingRetestStore = (*TaskStore)(nil)

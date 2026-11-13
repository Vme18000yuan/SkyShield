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

	"github.com/google/uuid"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

const findingCaseReviewMessage = "人工选择整理（只读原始记录）：本任务所选 finding_ids=%s。逐条读取已有报告与证据，仅判断是否为同一实际根因；根因明确相同才归并。只有存在具体共同点但缺少关键根因证据时才提交待确认建议。不得修改原始报告、评级、状态或证据，不得处理未选记录，不得重新探测目标，遵守人工否决。已有或新建归并组可按读取到的版本更新统一报告。结束前必须调用 complete_finding_case_review，为每条所选发现提交判断、理由和根因证据，并提供总结；验收失败不能声称完成。"

func (s *TaskStore) CreateFindingCaseReview(ctx context.Context, user int, taskID int64, ids []int64) (*aiapp.FindingCaseReview, error) {
	items, err := s.CreateFindingCaseReviewBatch(ctx, user, []aiapp.FindingCaseReviewBatch{{TaskID: taskID, FindingIDs: ids}})
	if err != nil {
		return nil, err
	}
	if len(items) != 1 {
		return nil, aiapp.ErrInvalidInput
	}
	return &items[0], nil
}

// Reserve all source tasks in ID order before creating any review, so a
// conflict or invalid finding cannot leave a partially enqueued batch.
func (s *TaskStore) CreateFindingCaseReviewBatch(ctx context.Context, user int, batches []aiapp.FindingCaseReviewBatch) ([]aiapp.FindingCaseReview, error) {
	if len(batches) < 1 || len(batches) > 20 {
		return nil, aiapp.ErrInvalidInput
	}
	seen, previousTask := map[int64]bool{}, int64(0)
	for _, batch := range batches {
		if batch.TaskID <= previousTask || len(batch.FindingIDs) < 1 || len(batch.FindingIDs) > 200 {
			return nil, aiapp.ErrInvalidInput
		}
		previousTask = batch.TaskID
		previousFinding := int64(0)
		for _, id := range batch.FindingIDs {
			if id <= previousFinding || seen[id] {
				return nil, aiapp.ErrInvalidInput
			}
			previousFinding, seen[id] = id, true
		}
	}
	if len(seen) > 200 {
		return nil, aiapp.ErrInvalidInput
	}
	out := make([]aiapp.FindingCaseReview, 0, len(batches))
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ready bool
		if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_custom_agent WHERE tenant_id=?::uuid AND agent_key='reporter' AND enabled)
 AND (SELECT count(*) FROM ai_tool_catalog WHERE tenant_id=?::uuid AND key IN ('search_finding_duplicates','get_finding_record','get_finding_case','update_finding_case_report','merge_finding_records','suggest_finding_merge','complete_finding_case_review') AND system AND kind='builtin' AND enabled AND agents @> '["reporter"]'::jsonb)=7`, assets.DefaultTenantID, assets.DefaultTenantID).Scan(&ready).Error; err != nil {
			return err
		}
		if !ready {
			return aiapp.ErrTaskControlConflict
		}
		for _, batch := range batches {
			if _, err := lockHumanGoalScope(tx, user, batch.TaskID); err != nil {
				return err
			}
			if err := lockCaseFindings(tx, batch.TaskID, batch.FindingIDs); err != nil {
				return err
			}
			var partialGroup bool
			raw := findingIDsJSON(batch.FindingIDs)
			if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM tianqiong_ai.finding_case_members selected JOIN tianqiong_ai.finding_cases c ON c.id=selected.case_id AND c.active AND c.task_id=?
 JOIN tianqiong_ai.finding_case_members other ON other.case_id=c.id
 WHERE selected.finding_id IN (SELECT value::bigint FROM jsonb_array_elements_text(?::jsonb))
 AND other.finding_id NOT IN (SELECT value::bigint FROM jsonb_array_elements_text(?::jsonb)))`, batch.TaskID, raw, raw).Scan(&partialGroup).Error; err != nil {
				return err
			}
			if partialGroup {
				return aiapp.ErrTaskControlConflict
			}
			var busy bool
			if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_finding_case_review WHERE task_id=? AND state IN ('queued','running'))`, batch.TaskID).Scan(&busy).Error; err != nil {
				return err
			}
			if busy {
				return aiapp.ErrTaskControlConflict
			}
		}
		for _, batch := range batches {
			if err := tx.Exec(`SELECT set_config('tianqiong.task_id',?,TRUE),set_config('tianqiong.owner_user_id',?,TRUE),set_config('tianqiong.execution_mode','human_finding_case_review',TRUE)`, strconv.FormatInt(batch.TaskID, 10), strconv.Itoa(user)).Error; err != nil {
				return err
			}
			item, err := createFindingCaseReviewTx(tx, user, batch.TaskID, batch.FindingIDs)
			if err != nil {
				return err
			}
			out = append(out, item)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func createFindingCaseReviewTx(tx *gorm.DB, user int, taskID int64, ids []int64) (aiapp.FindingCaseReview, error) {
	var out aiapp.FindingCaseReview
	var taskName string
	if err := tx.Raw(`SELECT name FROM ai_task WHERE id=?`, taskID).Row().Scan(&taskName); err != nil {
		return out, err
	}
	taskName = strings.TrimSpace(taskName)
	if taskName == "" {
		taskName = "任务"
	}
	if runes := []rune(taskName); len(runes) > 100 {
		taskName = string(runes[:100])
	}
	conversationID := uuid.NewString()
	title := "漏洞整理 · " + taskName
	if err := tx.Exec(`INSERT INTO ai_conversation(id,tenant_id,owner_user_id,title,agent_key) VALUES(?::uuid,?,?,?,'reporter')`, conversationID, assets.DefaultTenantID, user, title).Error; err != nil {
		return out, err
	}
	var jobID int64
	raw := findingIDsJSON(ids)
	message := fmt.Sprintf(findingCaseReviewMessage, raw)
	if err := tx.Raw(`INSERT INTO ai_agent_trigger_job(tenant_id,agent_key,owner_user_id,source_task_id,event_key,title,message,conversation_id)
 VALUES(?::uuid,'reporter',?,?,?,?,?,?::uuid) RETURNING id`, assets.DefaultTenantID, user, taskID, "finding-case-review:"+conversationID, title, message, conversationID).Row().Scan(&jobID); err != nil {
		return out, err
	}
	if err := tx.Raw(`INSERT INTO ai_finding_case_review(tenant_id,owner_user_id,task_id,origin_task_id,conversation_id,job_id,finding_ids)
 VALUES(?::uuid,?,?,?,?::uuid,?,?::jsonb) RETURNING id,task_id,conversation_id::text,state,error,jsonb_array_length(finding_ids),created_at`, assets.DefaultTenantID, user, taskID, taskID, conversationID, jobID, raw).Row().Scan(&out.ID, &out.TaskID, &out.ConversationID, &out.State, &out.Error, &out.FindingCount, &out.CreatedAt); err != nil {
		return out, err
	}
	out.FindingIDs = make([]string, 0, len(ids))
	for _, id := range ids {
		out.FindingIDs = append(out.FindingIDs, strconv.FormatInt(id, 10))
	}
	return out, nil
}

const findingCaseReviewScope = ` FROM ai_finding_case_review r JOIN ai_task t ON t.id=r.task_id AND t.tenant_id=r.tenant_id AND t.owner_user_id=r.owner_user_id
 JOIN tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id JOIN tenant tenant ON tenant.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
 WHERE r.tenant_id=?::uuid AND r.owner_user_id=? AND r.task_id=? AND t.deleted_at IS NULL AND tenant.status='active' AND u.is_active AND m.role IN ('admin','analyst')
 AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target x WHERE x.id=t.target_id AND x.deleted_at IS NULL)) AND (?=0 OR r.id=?)`

func (s *TaskStore) ListFindingCaseReviews(ctx context.Context, user int, taskID, reviewID int64, page, size int) (aiapp.FindingCaseReviewPage, error) {
	out := aiapp.FindingCaseReviewPage{Items: []aiapp.FindingCaseReview{}, Page: page, Size: size}
	tenant, err := s.TrafficTaskTenant(ctx, user, taskID)
	if err != nil {
		return out, err
	}
	args := []any{tenant, user, taskID, reviewID, reviewID}
	if err = s.db.WithContext(ctx).Raw(`SELECT count(*)`+findingCaseReviewScope, args...).Row().Scan(&out.Total); err != nil {
		return out, err
	}
	values := append([]any{reviewID > 0, reviewID > 0}, args...)
	values = append(values, size, page*size)
	rows, err := s.db.WithContext(ctx).Raw(`SELECT r.id,r.task_id,r.conversation_id::text,r.state,r.error,jsonb_array_length(r.finding_ids),
 CASE WHEN ? THEN r.finding_ids ELSE '[]'::jsonb END,CASE WHEN ? THEN r.conclusion ELSE '{}'::jsonb END,r.created_at,r.started_at,r.finished_at`+findingCaseReviewScope+` ORDER BY r.id DESC LIMIT ? OFFSET ?`, values...).Rows()
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var item aiapp.FindingCaseReview
		var ids, conclusion []byte
		if err = rows.Scan(&item.ID, &item.TaskID, &item.ConversationID, &item.State, &item.Error, &item.FindingCount, &ids, &conclusion, &item.CreatedAt, &item.StartedAt, &item.FinishedAt); err != nil {
			rows.Close()
			return out, err
		}
		var rawIDs []int64
		if err = json.Unmarshal(ids, &rawIDs); err != nil {
			rows.Close()
			return out, err
		}
		for _, id := range rawIDs {
			item.FindingIDs = append(item.FindingIDs, strconv.FormatInt(id, 10))
		}
		if reviewID > 0 {
			item.Conclusion = append([]byte(nil), conclusion...)
		}
		out.Items = append(out.Items, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	if reviewID > 0 && len(out.Items) == 1 {
		item := &out.Items[0]
		item.Findings = []aiapp.TaskFindingCaseMember{}
		findingRows, e := s.db.WithContext(ctx).Raw(`SELECT f.id,f.name,f.vulnclass,f.severity,f.status,f.summary FROM tianqiong_ai.findings f JOIN ai_finding_case_review r ON r.id=? AND r.task_id=f.task_id WHERE f.id IN (SELECT value::bigint FROM jsonb_array_elements_text(r.finding_ids)) ORDER BY f.id`, reviewID).Rows()
		if e != nil {
			return out, e
		}
		for findingRows.Next() {
			var finding aiapp.TaskFindingCaseMember
			if e = findingRows.Scan(&finding.ID, &finding.Name, &finding.VulnClass, &finding.Severity, &finding.Status, &finding.Summary); e != nil {
				findingRows.Close()
				return out, e
			}
			item.Findings = append(item.Findings, finding)
		}
		if e = findingRows.Err(); e != nil {
			findingRows.Close()
			return out, e
		}
		if e = findingRows.Close(); e != nil {
			return out, e
		}
	}
	current, err := s.TrafficTaskTenant(ctx, user, taskID)
	if err != nil {
		return aiapp.FindingCaseReviewPage{}, err
	}
	if current != tenant {
		return aiapp.FindingCaseReviewPage{}, aiapp.ErrTaskNotFound
	}
	return out, nil
}

func (s *TaskStore) CancelQueuedFindingCaseReview(ctx context.Context, user int, taskID, reviewID int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockHumanGoalScope(tx, user, taskID); err != nil {
			return err
		}
		var conversation string
		err := tx.Raw(`SELECT conversation_id::text FROM ai_finding_case_review WHERE id=? AND task_id=? AND owner_user_id=? AND conversation_id IS NOT NULL`, reviewID, taskID, user).Row().Scan(&conversation)
		if errors.Is(err, sql.ErrNoRows) {
			return aiapp.ErrTaskNotFound
		}
		if err != nil {
			return err
		}
		var locked string
		if err = tx.Raw(`SELECT id::text FROM ai_conversation WHERE id=?::uuid AND owner_user_id=? FOR UPDATE`, conversation, user).Row().Scan(&locked); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return aiapp.ErrTaskNotFound
			}
			return err
		}
		var jobID *int64
		var state string
		if err = tx.Raw(`SELECT job_id,state FROM ai_finding_case_review WHERE id=? AND task_id=? AND owner_user_id=? FOR UPDATE`, reviewID, taskID, user).Row().Scan(&jobID, &state); err != nil {
			return err
		}
		if state != "queued" || jobID == nil {
			return aiapp.ErrTaskControlConflict
		}
		result := tx.Exec(`UPDATE ai_agent_trigger_job SET status='cancelled',error_message='用户取消',updated_at=now() WHERE id=? AND owner_user_id=? AND source_task_id=? AND status='queued'`, *jobID, user, taskID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return aiapp.ErrTaskControlConflict
		}
		return nil
	})
}

func (s *TaskStore) CompleteFindingCaseReview(ctx context.Context, user int, run, toolUseID string, conclusion aiapp.FindingCaseReviewConclusion) (bool, error) {
	selection, review, err := s.ReporterReviewSelection(ctx, user, run)
	if err != nil {
		return false, err
	}
	if !review || len(selection) == 0 {
		return false, aiapp.ErrRunLeaseLost
	}
	taskID, err := s.ReporterCaseScope(ctx, user, run, selection[0], 0)
	if err != nil {
		return false, err
	}
	raw, err := json.Marshal(conclusion)
	if err != nil {
		return false, err
	}
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	replayed := false
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?,0))`, run+"/"+toolUseID).Error; err != nil {
			return err
		}
		if _, err := lockHumanGoalScope(tx, user, taskID); err != nil {
			return err
		}
		if err := tx.Exec(`SELECT set_config('tianqiong.execution_mode','reporter_finding_case',TRUE),set_config('tianqiong.execution_id',?,TRUE)`, run).Error; err != nil {
			return err
		}
		var checked int64
		if err := tx.Raw(`SELECT (tianqiong_ai.assert_finding_case_scope()).id`).Row().Scan(&checked); err != nil || checked != taskID {
			return aiapp.ErrRunLeaseLost
		}
		var previous struct{ Operation, RequestHash string }
		query := tx.Raw(`SELECT operation,request_hash FROM ai_tool_mutation_receipt WHERE tenant_id=?::uuid AND execution_id=?::uuid AND tool_use_id=?`, assets.DefaultTenantID, run, toolUseID).Scan(&previous)
		if query.Error != nil {
			return query.Error
		}
		if query.RowsAffected > 0 {
			if previous.Operation != "finding_case_review_conclusion" || previous.RequestHash != digest {
				return aiapp.ErrInvalidInput
			}
			replayed = true
			return nil
		}
		var reviewID int64
		var state string
		var existing []byte
		err := tx.Raw(`SELECT id,state,conclusion FROM ai_finding_case_review WHERE tenant_id=?::uuid AND owner_user_id=? AND task_id=? AND run_id=?::uuid FOR UPDATE`, assets.DefaultTenantID, user, taskID, run).Row().Scan(&reviewID, &state, &existing)
		if errors.Is(err, sql.ErrNoRows) {
			return aiapp.ErrRunLeaseLost
		}
		if err != nil {
			return err
		}
		if state != "running" || string(existing) != "{}" {
			return aiapp.ErrFindingCaseReviewConclusion
		}
		if err = tx.Exec(`UPDATE ai_finding_case_review SET conclusion=?::jsonb WHERE id=?`, string(raw), reviewID).Error; err != nil {
			return err
		}
		var valid bool
		if err = tx.Raw(`SELECT tianqiong_ai.finding_case_review_valid(?)`, reviewID).Scan(&valid).Error; err != nil {
			return err
		}
		if !valid {
			return aiapp.ErrFindingCaseReviewConclusion
		}
		if err = tx.Raw(`SELECT (tianqiong_ai.assert_finding_case_scope()).id`).Row().Scan(&checked); err != nil || checked != taskID {
			return aiapp.ErrRunLeaseLost
		}
		return tx.Exec(`INSERT INTO ai_tool_mutation_receipt(tenant_id,owner_user_id,execution_id,tool_use_id,operation,request_hash,result) VALUES(?,?,?::uuid,?,'finding_case_review_conclusion',?,'{}'::jsonb)`, assets.DefaultTenantID, user, run, toolUseID, digest).Error
	})
	return replayed, err
}

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strconv"

	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

func (s *TaskStore) ReporterCaseScope(ctx context.Context, user int, run string, findingID, caseID int64) (int64, error) {
	var taskID int64
	err := s.db.WithContext(ctx).Raw(`SELECT t.id FROM ai_agent_trigger_job j
 JOIN ai_run r ON r.id=j.run_id AND r.tenant_id=j.tenant_id AND r.owner_user_id=j.owner_user_id AND r.conversation_id=j.conversation_id
 JOIN ai_task t ON t.id=j.source_task_id AND t.tenant_id=j.tenant_id AND t.owner_user_id=j.owner_user_id
 JOIN tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id JOIN auth_user u ON u.id=m.user_id JOIN tenant tenant ON tenant.id=m.tenant_id
 WHERE j.tenant_id=?::uuid AND j.owner_user_id=? AND j.run_id=?::uuid AND j.agent_key='reporter' AND j.status='running'
 AND r.status='running' AND r.lease_until>clock_timestamp() AND t.deleted_at IS NULL AND tenant.status='active' AND u.is_active AND m.role IN ('admin','analyst')
 AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target x WHERE x.id=t.target_id AND x.deleted_at IS NULL))
 AND NOT EXISTS(SELECT 1 FROM ai_task_archive a WHERE a.task_id=t.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting'))
 AND ((?::bigint>0 AND EXISTS(SELECT 1 FROM tianqiong_ai.findings f WHERE f.id=? AND f.task_id=t.id))
   OR (?::bigint>0 AND EXISTS(SELECT 1 FROM tianqiong_ai.finding_cases c WHERE c.id=? AND c.task_id=t.id AND c.active)))
 AND NOT EXISTS(SELECT 1 FROM ai_finding_case_review review WHERE review.run_id=r.id AND (
  review.state<>'running' OR (?::bigint>0 AND NOT (review.finding_ids @> jsonb_build_array(?::bigint))) OR
  (?::bigint>0 AND EXISTS(SELECT 1 FROM tianqiong_ai.finding_case_members member WHERE member.case_id=? AND NOT (review.finding_ids @> jsonb_build_array(member.finding_id))))))
 LIMIT 1`, assets.DefaultTenantID, user, run, findingID, findingID, caseID, caseID, findingID, findingID, caseID, caseID).Row().Scan(&taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, aiapp.ErrRunLeaseLost
	}
	return taskID, err
}

func (s *TaskStore) ReporterReviewSelection(ctx context.Context, user int, run string) ([]int64, bool, error) {
	var raw []byte
	var state string
	err := s.db.WithContext(ctx).Raw(`SELECT finding_ids,state FROM ai_finding_case_review WHERE tenant_id=?::uuid AND owner_user_id=? AND run_id=?::uuid`, assets.DefaultTenantID, user, run).Row().Scan(&raw, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if state != "running" {
		return nil, true, aiapp.ErrRunLeaseLost
	}
	var ids []int64
	if err = json.Unmarshal(raw, &ids); err != nil {
		return nil, true, err
	}
	return ids, true, nil
}

func (s *TaskStore) MarkReporterReviewRead(ctx context.Context, user int, run string, findingID int64, fingerprint string) error {
	ids, review, err := s.ReporterReviewSelection(ctx, user, run)
	if err != nil || !review {
		return err
	}
	if !slices.Contains(ids, findingID) {
		return aiapp.ErrRunLeaseLost
	}
	decoded, err := hex.DecodeString(fingerprint)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != fingerprint {
		return aiapp.ErrInvalidInput
	}
	taskID, err := s.ReporterCaseScope(ctx, user, run, findingID, 0)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
		result := tx.Exec(`UPDATE ai_finding_case_review SET reviewed_ids=CASE WHEN reviewed_ids @> jsonb_build_array(?::bigint) THEN reviewed_ids ELSE reviewed_ids||jsonb_build_array(?::bigint) END,
 reviewed_fingerprints=jsonb_set(reviewed_fingerprints,ARRAY[?::text],to_jsonb(?::text),TRUE)
 WHERE tenant_id=?::uuid AND owner_user_id=? AND task_id=? AND run_id=?::uuid AND state='running' AND finding_ids @> jsonb_build_array(?::bigint)
 AND EXISTS(SELECT 1 FROM tianqiong_ai.findings f WHERE f.id=? AND f.task_id=? AND encode(sha256(convert_to(to_jsonb(f)::text,'UTF8')),'hex')=?)`, findingID, findingID, strconv.FormatInt(findingID, 10), fingerprint, assets.DefaultTenantID, user, taskID, run, findingID, findingID, taskID, fingerprint)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return aiapp.ErrRunLeaseLost
		}
		return nil
	})
}

func ensureReporterCaseWritable(tx *gorm.DB, run string) error {
	var blocked bool
	if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_finding_case_review WHERE run_id=?::uuid AND (state<>'running' OR conclusion<>'{}'::jsonb))`, run).Scan(&blocked).Error; err != nil {
		return err
	}
	if blocked {
		return aiapp.ErrFindingCaseReviewConclusion
	}
	return nil
}

func (s *TaskStore) UpdateReporterFindingCaseReport(ctx context.Context, user int, run, toolUseID string, caseID int64, input aiapp.FindingCaseReport) (bool, error) {
	taskID, err := s.ReporterCaseScope(ctx, user, run, 0, caseID)
	if err != nil {
		return false, err
	}
	request, _ := json.Marshal(struct {
		CaseID int64
		Report aiapp.FindingCaseReport
	}{caseID, input})
	hash := sha256.Sum256(request)
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
		if err := ensureReporterCaseWritable(tx, run); err != nil {
			return err
		}
		var previous struct{ Operation, RequestHash string }
		query := tx.Raw(`SELECT operation,request_hash FROM ai_tool_mutation_receipt WHERE tenant_id=?::uuid AND execution_id=?::uuid AND tool_use_id=?`, assets.DefaultTenantID, run, toolUseID).Scan(&previous)
		if query.Error != nil {
			return query.Error
		}
		if query.RowsAffected > 0 {
			if previous.Operation != "finding_case_report" || previous.RequestHash != digest {
				return aiapp.ErrInvalidInput
			}
			replayed = true
			return nil
		}
		if err := updateFindingCaseReportTx(tx, taskID, caseID, input, "reporter"); err != nil {
			return err
		}
		if err := tx.Raw(`SELECT (tianqiong_ai.assert_finding_case_scope()).id`).Row().Scan(&checked); err != nil || checked != taskID {
			return aiapp.ErrRunLeaseLost
		}
		result, _ := json.Marshal(map[string]string{"case_id": strconv.FormatInt(caseID, 10)})
		return tx.Exec(`INSERT INTO ai_tool_mutation_receipt(tenant_id,owner_user_id,execution_id,tool_use_id,operation,request_hash,result) VALUES(?, ?, ?::uuid, ?, 'finding_case_report', ?, ?::jsonb)`, assets.DefaultTenantID, user, run, toolUseID, digest, string(result)).Error
	})
	return replayed, err
}

func (s *TaskStore) MergeReporterFindingCase(ctx context.Context, user int, run, toolUseID string, requestedTask int64, ids []int64, title, reason string) (int64, bool, error) {
	if len(ids) < 2 || len(ids) > 100 {
		return 0, false, aiapp.ErrInvalidInput
	}
	taskID, err := s.ReporterCaseScope(ctx, user, run, ids[0], 0)
	if err != nil {
		return 0, false, err
	}
	if taskID != requestedTask {
		return 0, false, aiapp.ErrRunLeaseLost
	}
	raw, _ := json.Marshal(struct {
		TaskID        int64
		IDs           []int64
		Title, Reason string
	}{taskID, ids, title, reason})
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	var caseID int64
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
		if err := ensureReporterCaseWritable(tx, run); err != nil {
			return err
		}
		var previous struct {
			Operation, RequestHash string
			Result                 []byte
		}
		query := tx.Raw(`SELECT operation,request_hash,result FROM ai_tool_mutation_receipt WHERE tenant_id=?::uuid AND execution_id=?::uuid AND tool_use_id=?`, assets.DefaultTenantID, run, toolUseID).Scan(&previous)
		if query.Error != nil {
			return query.Error
		}
		if query.RowsAffected > 0 {
			if previous.Operation != "finding_case_merge" || previous.RequestHash != digest {
				return aiapp.ErrInvalidInput
			}
			var result struct {
				CaseID string `json:"case_id"`
			}
			if err := json.Unmarshal(previous.Result, &result); err != nil {
				return err
			}
			caseID, err = strconv.ParseInt(result.CaseID, 10, 64)
			if err != nil || caseID <= 0 {
				return aiapp.ErrInvalidInput
			}
			replayed = true
			return nil
		}
		var e error
		caseID, e = mergeFindingCaseTx(tx, taskID, ids, title, reason, "reporter")
		if e != nil {
			return e
		}
		if e = sweepFindingCaseSuggestions(tx, taskID); e != nil {
			return e
		}
		if e = tx.Raw(`SELECT (tianqiong_ai.assert_finding_case_scope()).id`).Row().Scan(&checked); e != nil || checked != taskID {
			return aiapp.ErrRunLeaseLost
		}
		result, _ := json.Marshal(map[string]string{"case_id": strconv.FormatInt(caseID, 10)})
		return tx.Exec(`INSERT INTO ai_tool_mutation_receipt(tenant_id,owner_user_id,execution_id,tool_use_id,operation,request_hash,result) VALUES(?,?,?::uuid,?,'finding_case_merge',?,?::jsonb)`, assets.DefaultTenantID, user, run, toolUseID, digest, string(result)).Error
	})
	return caseID, replayed, err
}

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

// Suggestions may contain free-text evidence about every original member.
// Show them only in their own task; inherited task contexts cannot safely
// expose their history or resolve them on behalf of the source task.
const visibleCaseSuggestions = taskFindingScope + `,visible_suggestions AS (
 SELECT s.* FROM tianqiong_ai.finding_case_suggestions s JOIN current_task c ON c.id=s.task_id AND c.tenant_id=s.tenant_id AND c.owner_user_id=s.owner_user_id
 WHERE s.state='pending' AND (SELECT count(*) FROM tianqiong_ai.finding_case_suggestion_members m WHERE m.suggestion_id=s.id)>=2
 AND NOT EXISTS(SELECT 1 FROM tianqiong_ai.finding_case_suggestion_members m WHERE m.suggestion_id=s.id AND NOT EXISTS(SELECT 1 FROM eligible e WHERE e.id=m.finding_id))
) `

func (s *TaskStore) ListFindingCaseSuggestions(ctx context.Context, user int, taskID int64, page, size int) (aiapp.FindingCaseSuggestionPage, error) {
	out := aiapp.FindingCaseSuggestionPage{Items: []aiapp.FindingCaseSuggestion{}, Page: page, Size: size}
	tenant, err := s.TrafficTaskTenant(ctx, user, taskID)
	if err != nil {
		return out, err
	}
	args := []any{taskID, tenant, user, true, 0, 0, "", "", "", ""}
	if err = s.db.WithContext(ctx).Raw(visibleCaseSuggestions+`SELECT count(*) FROM visible_suggestions`, args...).Row().Scan(&out.Total); err != nil {
		return out, err
	}
	values := append(append([]any{}, args...), size, page*size)
	rows, err := s.db.WithContext(ctx).Raw(visibleCaseSuggestions+`SELECT s.id,s.task_id,COALESCE((SELECT name FROM public.ai_task WHERE id=s.task_id),''),s.title,s.reason,s.created_at FROM visible_suggestions s ORDER BY s.id DESC LIMIT ? OFFSET ?`, values...).Rows()
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var item aiapp.FindingCaseSuggestion
		if err = rows.Scan(&item.ID, &item.TaskID, &item.TaskName, &item.Title, &item.Reason, &item.CreatedAt); err != nil {
			rows.Close()
			return out, err
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
	for i := range out.Items {
		item := &out.Items[i]
		item.Members = []aiapp.TaskFindingCaseMember{}
		memberArgs := append(append([]any{}, args...), item.ID)
		memberRows, e := s.db.WithContext(ctx).Raw(visibleCaseSuggestions+`SELECT e.id,e.name,e.vulnclass,e.severity,e.status,e.summary
 FROM eligible e JOIN tianqiong_ai.finding_case_suggestion_members m ON m.finding_id=e.id JOIN visible_suggestions s ON s.id=m.suggestion_id
 WHERE s.id=? ORDER BY e.id`, memberArgs...).Rows()
		if e != nil {
			return out, e
		}
		for memberRows.Next() {
			var member aiapp.TaskFindingCaseMember
			if e = memberRows.Scan(&member.ID, &member.Name, &member.VulnClass, &member.Severity, &member.Status, &member.Summary); e != nil {
				memberRows.Close()
				return out, e
			}
			item.Members = append(item.Members, member)
		}
		if e = memberRows.Err(); e != nil {
			memberRows.Close()
			return out, e
		}
		if e = memberRows.Close(); e != nil {
			return out, e
		}
		var current []int64
		if e = s.db.WithContext(ctx).Raw(visibleCaseSuggestions+`SELECT m.finding_id FROM tianqiong_ai.finding_case_suggestion_members m JOIN visible_suggestions s ON s.id=m.suggestion_id WHERE s.id=? ORDER BY m.finding_id`, memberArgs...).Scan(&current).Error; e != nil {
			return out, e
		}
		ids := make([]int64, 0, len(item.Members))
		for _, member := range item.Members {
			ids = append(ids, member.ID)
		}
		if len(ids) < 2 || !slices.Equal(ids, current) {
			return aiapp.FindingCaseSuggestionPage{}, aiapp.ErrTaskControlConflict
		}
	}
	current, err := s.TrafficTaskTenant(ctx, user, taskID)
	if err != nil {
		return aiapp.FindingCaseSuggestionPage{}, err
	}
	if current != tenant {
		return aiapp.FindingCaseSuggestionPage{}, aiapp.ErrTaskNotFound
	}
	return out, nil
}

// Reconcile pending suggestions after a manual/Reporter merge or human veto.
// A blocked pair is obsolete even if malformed historical data grouped it.
func sweepFindingCaseSuggestions(tx *gorm.DB, taskID int64) error {
	if err := tx.Exec(`UPDATE tianqiong_ai.finding_case_suggestions s SET state='obsolete',updated_at=now()
 WHERE s.task_id=? AND s.state='pending' AND tianqiong_ai.finding_case_review_suggestion_allowed(s.id) AND ((SELECT count(*) FROM tianqiong_ai.finding_case_suggestion_members m WHERE m.suggestion_id=s.id)<2 OR EXISTS(
  SELECT 1 FROM tianqiong_ai.finding_case_suggestion_members a JOIN tianqiong_ai.finding_case_suggestion_members b ON b.suggestion_id=a.suggestion_id AND a.finding_id<b.finding_id
  JOIN tianqiong_ai.finding_case_blocks block ON block.left_id=a.finding_id AND block.right_id=b.finding_id WHERE a.suggestion_id=s.id))`, taskID).Error; err != nil {
		return err
	}
	return tx.Exec(`UPDATE tianqiong_ai.finding_case_suggestions s SET state='accepted',updated_at=now()
 WHERE s.task_id=? AND s.state='pending' AND tianqiong_ai.finding_case_review_suggestion_allowed(s.id) AND (SELECT count(*) FROM tianqiong_ai.finding_case_suggestion_members m WHERE m.suggestion_id=s.id)>=2
 AND NOT EXISTS(SELECT 1 FROM tianqiong_ai.finding_case_suggestion_members m LEFT JOIN tianqiong_ai.finding_case_members cm ON cm.finding_id=m.finding_id
  LEFT JOIN tianqiong_ai.finding_cases c ON c.id=cm.case_id AND c.active AND c.task_id=s.task_id WHERE m.suggestion_id=s.id AND c.id IS NULL)
 AND (SELECT count(DISTINCT cm.case_id) FROM tianqiong_ai.finding_case_suggestion_members m JOIN tianqiong_ai.finding_case_members cm ON cm.finding_id=m.finding_id WHERE m.suggestion_id=s.id)=1`, taskID).Error
}

func caseSuggestionMembers(tx *gorm.DB, suggestionID, taskID int64) ([]int64, error) {
	ids := []int64{}
	err := tx.Raw(`SELECT m.finding_id FROM tianqiong_ai.finding_case_suggestion_members m JOIN tianqiong_ai.findings f ON f.id=m.finding_id AND f.task_id=? WHERE m.suggestion_id=? ORDER BY m.finding_id`, taskID, suggestionID).Scan(&ids).Error
	return ids, err
}

func allMembersInSameCase(tx *gorm.DB, taskID int64, ids []int64) (bool, error) {
	var total, grouped, groups int
	err := tx.Raw(`SELECT count(*),count(c.id),count(DISTINCT c.id) FROM tianqiong_ai.findings f
 LEFT JOIN tianqiong_ai.finding_case_members m ON m.finding_id=f.id LEFT JOIN tianqiong_ai.finding_cases c ON c.id=m.case_id AND c.active AND c.task_id=f.task_id
 WHERE f.task_id=? AND f.id IN (SELECT value::bigint FROM jsonb_array_elements_text(?::jsonb))`, taskID, findingIDsJSON(ids)).Row().Scan(&total, &grouped, &groups)
	return total == len(ids) && grouped == len(ids) && groups == 1, err
}

func anyFindingCaseBlock(tx *gorm.DB, ids []int64) (bool, error) {
	raw := findingIDsJSON(ids)
	var blocked bool
	err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM tianqiong_ai.finding_case_blocks WHERE left_id IN (SELECT value::bigint FROM jsonb_array_elements_text(?::jsonb)) AND right_id IN (SELECT value::bigint FROM jsonb_array_elements_text(?::jsonb)))`, raw, raw).Scan(&blocked).Error
	return blocked, err
}

func containsFindingIDs(superset, subset []int64) bool {
	for _, id := range subset {
		if !slices.Contains(superset, id) {
			return false
		}
	}
	return true
}

func createCaseSuggestionTx(tx *gorm.DB, taskID int64, ids []int64, title, reason string) (int64, error) {
	if err := lockCaseFindings(tx, taskID, ids); err != nil {
		return 0, err
	}
	same, err := allMembersInSameCase(tx, taskID, ids)
	if err != nil {
		return 0, err
	}
	blocked, err := anyFindingCaseBlock(tx, ids)
	if err != nil {
		return 0, err
	}
	if same || blocked {
		return 0, aiapp.ErrTaskControlConflict
	}
	if err := sweepFindingCaseSuggestions(tx, taskID); err != nil {
		return 0, err
	}
	var pending []int64
	if err = tx.Raw(`SELECT id FROM tianqiong_ai.finding_case_suggestions WHERE task_id=? AND state='pending' AND tianqiong_ai.finding_case_review_suggestion_allowed(id) ORDER BY id`, taskID).Scan(&pending).Error; err != nil {
		return 0, err
	}
	var exact, superset int64
	supersede := []int64{}
	for _, id := range pending {
		members, e := caseSuggestionMembers(tx, id, taskID)
		if e != nil {
			return 0, e
		}
		switch {
		case slices.Equal(members, ids):
			if exact == 0 {
				exact = id
			} else {
				supersede = append(supersede, id)
			}
		case len(members) > len(ids) && containsFindingIDs(members, ids):
			if superset == 0 {
				superset = id
			}
		case len(members) < len(ids) && containsFindingIDs(ids, members):
			supersede = append(supersede, id)
		}
	}
	for _, id := range supersede {
		if err = tx.Exec(`UPDATE tianqiong_ai.finding_case_suggestions SET state='superseded',updated_at=now() WHERE id=? AND state='pending'`, id).Error; err != nil {
			return 0, err
		}
	}
	if exact > 0 {
		if err = tx.Exec(`UPDATE tianqiong_ai.finding_case_suggestions SET title=?,reason=?,updated_at=now() WHERE id=? AND state='pending'`, title, reason, exact).Error; err != nil {
			return 0, err
		}
		return exact, nil
	}
	if superset > 0 {
		return superset, nil
	}
	if len(pending)-len(supersede) >= 1000 {
		return 0, aiapp.ErrTaskControlConflict
	}
	var suggestionID int64
	if err = tx.Raw(`INSERT INTO tianqiong_ai.finding_case_suggestions(task_id,tenant_id,owner_user_id,title,reason)
 SELECT id,tenant_id,owner_user_id,?,? FROM public.ai_task WHERE id=? RETURNING id`, title, reason, taskID).Row().Scan(&suggestionID); err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err = tx.Exec(`INSERT INTO tianqiong_ai.finding_case_suggestion_members(suggestion_id,finding_id) VALUES(?,?)`, suggestionID, id).Error; err != nil {
			return 0, err
		}
	}
	return suggestionID, nil
}

func (s *TaskStore) CreateReporterFindingCaseSuggestion(ctx context.Context, user int, run, toolUseID string, ids []int64, title, reason string) (int64, bool, error) {
	if len(ids) < 2 || len(ids) > 20 {
		return 0, false, aiapp.ErrInvalidInput
	}
	taskID, err := s.ReporterCaseScope(ctx, user, run, ids[0], 0)
	if err != nil {
		return 0, false, err
	}
	raw, _ := json.Marshal(struct {
		IDs           []int64
		Title, Reason string
	}{ids, title, reason})
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	var suggestionID int64
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
			if previous.Operation != "finding_case_suggestion" || previous.RequestHash != digest {
				return aiapp.ErrInvalidInput
			}
			var result struct {
				SuggestionID string `json:"suggestion_id"`
			}
			if err := json.Unmarshal(previous.Result, &result); err != nil {
				return err
			}
			suggestionID, err = strconv.ParseInt(result.SuggestionID, 10, 64)
			if err != nil || suggestionID <= 0 {
				return aiapp.ErrInvalidInput
			}
			replayed = true
			return nil
		}
		var e error
		suggestionID, e = createCaseSuggestionTx(tx, taskID, ids, title, reason)
		if e != nil {
			return e
		}
		if e = tx.Raw(`SELECT (tianqiong_ai.assert_finding_case_scope()).id`).Row().Scan(&checked); e != nil || checked != taskID {
			return aiapp.ErrRunLeaseLost
		}
		result, _ := json.Marshal(map[string]string{"suggestion_id": strconv.FormatInt(suggestionID, 10)})
		return tx.Exec(`INSERT INTO ai_tool_mutation_receipt(tenant_id,owner_user_id,execution_id,tool_use_id,operation,request_hash,result) VALUES(?,?,?::uuid,?,'finding_case_suggestion',?,?::jsonb)`, assets.DefaultTenantID, user, run, toolUseID, digest, string(result)).Error
	})
	return suggestionID, replayed, err
}

func (s *TaskStore) ResolveFindingCaseSuggestion(ctx context.Context, user int, taskID, suggestionID int64, accept bool, exclude []int64) (int64, error) {
	var caseID int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockHumanGoalScope(tx, user, taskID); err != nil {
			return err
		}
		if err := setHumanFindingCaseMode(tx); err != nil {
			return err
		}
		var title, reason, state string
		err := tx.Raw(`SELECT title,reason,state FROM tianqiong_ai.finding_case_suggestions WHERE id=? AND task_id=? FOR UPDATE`, suggestionID, taskID).Row().Scan(&title, &reason, &state)
		if errors.Is(err, sql.ErrNoRows) {
			return aiapp.ErrTaskNotFound
		}
		if err != nil {
			return err
		}
		if state != "pending" {
			return aiapp.ErrTaskControlConflict
		}
		members, err := caseSuggestionMembers(tx, suggestionID, taskID)
		if err != nil {
			return err
		}
		if len(members) < 2 || len(members) > 20 {
			return aiapp.ErrTaskControlConflict
		}
		if err = lockCaseFindings(tx, taskID, members); err != nil {
			return err
		}
		kept := make([]int64, 0, len(members))
		for _, id := range exclude {
			if !slices.Contains(members, id) {
				return aiapp.ErrInvalidInput
			}
		}
		for _, id := range members {
			if !slices.Contains(exclude, id) {
				kept = append(kept, id)
			}
		}
		if accept {
			if len(kept) < 2 {
				return aiapp.ErrInvalidInput
			}
			var sharedGroup bool
			if err = tx.Raw(`SELECT EXISTS(SELECT 1 FROM tianqiong_ai.finding_case_members a JOIN tianqiong_ai.finding_case_members b ON b.case_id=a.case_id
 WHERE a.finding_id IN (SELECT value::bigint FROM jsonb_array_elements_text(?::jsonb)) AND b.finding_id IN (SELECT value::bigint FROM jsonb_array_elements_text(?::jsonb)))`, findingIDsJSON(exclude), findingIDsJSON(kept)).Scan(&sharedGroup).Error; err != nil {
				return err
			}
			if sharedGroup {
				return aiapp.ErrTaskControlConflict
			}
			caseID, err = mergeFindingCaseTx(tx, taskID, kept, title, reason, "human")
			if err != nil {
				return err
			}
			for _, dropped := range exclude {
				for _, retained := range kept {
					if err = tx.Exec(`INSERT INTO tianqiong_ai.finding_case_blocks(left_id,right_id,reason) VALUES(LEAST(?::bigint,?::bigint),GREATEST(?::bigint,?::bigint),'人工确认时排除') ON CONFLICT DO NOTHING`, dropped, retained, dropped, retained).Error; err != nil {
						return err
					}
				}
			}
			if err = tx.Exec(`UPDATE tianqiong_ai.finding_case_suggestions SET state='accepted',updated_at=now() WHERE id=? AND state='pending'`, suggestionID).Error; err != nil {
				return err
			}
		} else {
			for i, left := range members {
				for _, right := range members[i+1:] {
					var same bool
					if same, err = allMembersInSameCase(tx, taskID, []int64{left, right}); err != nil {
						return err
					}
					if !same {
						if err = tx.Exec(`INSERT INTO tianqiong_ai.finding_case_blocks(left_id,right_id,reason) VALUES(?,?,'人工否决') ON CONFLICT DO NOTHING`, left, right).Error; err != nil {
							return err
						}
					}
				}
			}
			if err = tx.Exec(`UPDATE tianqiong_ai.finding_case_suggestions SET state='rejected',updated_at=now() WHERE id=? AND state='pending'`, suggestionID).Error; err != nil {
				return err
			}
		}
		return sweepFindingCaseSuggestions(tx, taskID)
	})
	return caseID, err
}

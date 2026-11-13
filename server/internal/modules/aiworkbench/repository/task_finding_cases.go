package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strconv"

	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"gorm.io/gorm"
)

// A source group is visible only if every one of its members is within the
// current task's eligible finding scope; otherwise its title and report could
// disclose findings outside the selected target.
const visibleFindingCases = taskFindingScope + `,visible_cases AS (
 SELECT c.*,s.inherited FROM tianqiong_ai.finding_cases c JOIN sources s ON s.id=c.task_id
 WHERE c.active AND c.tenant_id=s.tenant_id AND c.owner_user_id=s.owner_user_id
 AND EXISTS(SELECT 1 FROM tianqiong_ai.finding_case_members m JOIN eligible e ON e.id=m.finding_id WHERE m.case_id=c.id
  AND (?='' OR lower(e.severity)=?)
  AND (?='' OR strpos(lower(concat_ws(' ',c.title,c.reason,e.name,e.vulnclass,e.summary)),lower(?))>0))
 AND NOT EXISTS(SELECT 1 FROM tianqiong_ai.finding_case_members m WHERE m.case_id=c.id AND NOT EXISTS(SELECT 1 FROM eligible e WHERE e.id=m.finding_id))
 AND (?=0 OR c.id=?)
) `

func caseScopeArgs(user int, taskID int64, tenant string, filter aiapp.TaskFindingCaseFilter) []any {
	return []any{taskID, tenant, user, false, 0, 0, "", "", "", "", filter.Severity, filter.Severity, filter.Query, filter.Query, filter.CaseID, filter.CaseID}
}

func (s *TaskStore) ListTaskFindingCases(ctx context.Context, user int, taskID int64, filter aiapp.TaskFindingCaseFilter) (aiapp.TaskFindingCasePage, error) {
	caseID, page, size := filter.CaseID, filter.Page, filter.Size
	out := aiapp.TaskFindingCasePage{Items: []aiapp.TaskFindingCase{}, Page: page, Size: size}
	tenant, err := s.TrafficTaskTenant(ctx, user, taskID)
	if err != nil {
		return out, err
	}
	sources, err := s.findingSources(ctx, user, taskID, tenant)
	if err != nil {
		return out, err
	}
	args := caseScopeArgs(user, taskID, tenant, filter)
	if err = s.db.WithContext(ctx).Raw(visibleFindingCases+`SELECT count(*) FROM visible_cases`, args...).Row().Scan(&out.Total); err != nil {
		return out, err
	}
	values := append(append([]any{}, args...), caseID > 0, caseID > 0, size, page*size)
	rows, err := s.db.WithContext(ctx).Raw(visibleFindingCases+`SELECT c.id,c.task_id,COALESCE((SELECT name FROM public.ai_task WHERE id=c.task_id),''),c.title,c.reason,c.severity,
 CASE WHEN ? THEN c.severity_reason ELSE '' END,CASE WHEN ? THEN c.report ELSE '' END,c.version,c.report_version,(c.report<>''),
 (SELECT count(*) FROM tianqiong_ai.finding_case_members m WHERE m.case_id=c.id),c.inherited,c.created_at
 FROM visible_cases c ORDER BY c.created_at DESC,c.id DESC LIMIT ? OFFSET ?`, values...).Rows()
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var item aiapp.TaskFindingCase
		if err = rows.Scan(&item.ID, &item.TaskID, &item.TaskName, &item.Title, &item.Reason, &item.Severity, &item.SeverityReason, &item.Report, &item.Version, &item.ReportVersion, &item.HasReport, &item.MemberCount, &item.Inherited, &item.CreatedAt); err != nil {
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
	if caseID > 0 && len(out.Items) == 1 {
		item := &out.Items[0]
		item.Members = []aiapp.TaskFindingCaseMember{}
		memberArgs := append(append([]any{}, args...), caseID)
		memberRows, e := s.db.WithContext(ctx).Raw(visibleFindingCases+`SELECT e.id,e.name,e.vulnclass,e.severity,e.status,e.summary
 FROM eligible e JOIN tianqiong_ai.finding_case_members m ON m.finding_id=e.id JOIN visible_cases c ON c.id=m.case_id
 WHERE c.id=? ORDER BY e.id`, memberArgs...).Rows()
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
		// Historical reasons can refer to detached, off-target findings, so
		// inherited groups expose their current members but not source history.
		if !item.Inherited {
			item.Events = []aiapp.TaskFindingCaseEvent{}
			eventRows, eventErr := s.db.WithContext(ctx).Raw(`SELECT id,action,actor,reason,finding_ids,created_at FROM tianqiong_ai.finding_case_events WHERE case_id=? ORDER BY id DESC LIMIT 101`, caseID).Rows()
			if eventErr != nil {
				return out, eventErr
			}
			for eventRows.Next() {
				var event aiapp.TaskFindingCaseEvent
				var raw []byte
				if eventErr = eventRows.Scan(&event.ID, &event.Action, &event.Actor, &event.Reason, &raw, &event.CreatedAt); eventErr != nil {
					eventRows.Close()
					return out, eventErr
				}
				var ids []int64
				if eventErr = json.Unmarshal(raw, &ids); eventErr != nil {
					eventRows.Close()
					return out, eventErr
				}
				event.FindingIDs = make([]string, 0, len(ids))
				for _, id := range ids {
					event.FindingIDs = append(event.FindingIDs, strconv.FormatInt(id, 10))
				}
				if len(item.Events) == 100 {
					item.EventsTruncated = true
					break
				}
				item.Events = append(item.Events, event)
			}
			if eventErr = eventRows.Err(); eventErr != nil {
				eventRows.Close()
				return out, eventErr
			}
			if eventErr = eventRows.Close(); eventErr != nil {
				return out, eventErr
			}
		}
		var version int64
		e = s.db.WithContext(ctx).Raw(visibleFindingCases+`SELECT version FROM visible_cases WHERE id=?`, append(append([]any{}, args...), caseID)...).Row().Scan(&version)
		if errors.Is(e, sql.ErrNoRows) || e == nil && (version != item.Version || len(item.Members) != item.MemberCount) {
			return aiapp.TaskFindingCasePage{}, aiapp.ErrTaskControlConflict
		}
		if e != nil {
			return out, e
		}
	}
	current, err := s.TrafficTaskTenant(ctx, user, taskID)
	if err != nil || current != tenant {
		if err != nil {
			return aiapp.TaskFindingCasePage{}, err
		}
		return aiapp.TaskFindingCasePage{}, aiapp.ErrTaskNotFound
	}
	currentSources, err := s.findingSources(ctx, user, taskID, tenant)
	if err != nil {
		return aiapp.TaskFindingCasePage{}, err
	}
	if !slices.Equal(sources, currentSources) {
		return aiapp.TaskFindingCasePage{}, aiapp.ErrTaskNotFound
	}
	checked := map[int64]bool{}
	for _, item := range out.Items {
		if item.Inherited && !checked[item.TaskID] {
			if _, err = s.TrafficTaskTenant(ctx, user, item.TaskID); err != nil {
				return aiapp.TaskFindingCasePage{}, err
			}
			checked[item.TaskID] = true
		}
	}
	return out, nil
}

func findingIDsJSON(ids []int64) string {
	if ids == nil {
		return "[]"
	}
	raw, _ := json.Marshal(ids)
	return string(raw)
}

// Retest completion can lock a finding before it locks its task.  Never wait
// for a finding while holding the task lock: return a conflict instead.
func lockCaseFindings(tx *gorm.DB, taskID int64, ids []int64) error {
	raw := findingIDsJSON(ids)
	rows, err := tx.Raw(`SELECT id FROM tianqiong_ai.findings WHERE task_id=? AND id IN (SELECT value::bigint FROM jsonb_array_elements_text(?::jsonb)) ORDER BY id FOR UPDATE SKIP LOCKED`, taskID, raw).Rows()
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		count++
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if count == len(ids) {
		return nil
	}
	var existing int
	if err = tx.Raw(`SELECT count(*) FROM tianqiong_ai.findings WHERE task_id=? AND id IN (SELECT value::bigint FROM jsonb_array_elements_text(?::jsonb))`, taskID, raw).Scan(&existing).Error; err != nil {
		return err
	}
	if existing == len(ids) {
		return aiapp.ErrTaskControlConflict
	}
	return aiapp.ErrTaskNotFound
}

func setHumanFindingCaseMode(tx *gorm.DB) error {
	return tx.Exec(`SELECT set_config('tianqiong.execution_mode','human_finding_case',TRUE)`).Error
}

func (s *TaskStore) MergeTaskFindingCase(ctx context.Context, user int, taskID int64, ids []int64, title, reason string) (int64, error) {
	var caseID int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockHumanGoalScope(tx, user, taskID); err != nil {
			return err
		}
		if err := setHumanFindingCaseMode(tx); err != nil {
			return err
		}
		var err error
		caseID, err = mergeFindingCaseTx(tx, taskID, ids, title, reason, "human")
		if err != nil {
			return err
		}
		return sweepFindingCaseSuggestions(tx, taskID)
	})
	return caseID, err
}

// The caller holds the task lock and has established a fenced human or
// Reporter execution mode. Existing groups are united as whole groups.
func mergeFindingCaseTx(tx *gorm.DB, taskID int64, ids []int64, title, reason, actor string) (int64, error) {
	if err := lockCaseFindings(tx, taskID, ids); err != nil {
		return 0, err
	}
	var groups []int64
	if err := tx.Raw(`SELECT DISTINCT c.id FROM tianqiong_ai.finding_cases c JOIN tianqiong_ai.finding_case_members m ON m.case_id=c.id WHERE c.task_id=? AND c.active AND m.finding_id IN (SELECT value::bigint FROM jsonb_array_elements_text(?::jsonb)) ORDER BY c.id`, taskID, findingIDsJSON(ids)).Scan(&groups).Error; err != nil {
		return 0, err
	}
	all := append([]int64{}, ids...)
	if len(groups) > 0 {
		var members []int64
		if err := tx.Raw(`SELECT finding_id FROM tianqiong_ai.finding_case_members WHERE case_id IN (SELECT value::bigint FROM jsonb_array_elements_text(?::jsonb)) ORDER BY finding_id`, findingIDsJSON(groups)).Scan(&members).Error; err != nil {
			return 0, err
		}
		all = append(all, members...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	all = slices.Compact(all)
	if len(all) > 100 {
		return 0, aiapp.ErrInvalidInput
	}
	if err := lockCaseFindings(tx, taskID, all); err != nil {
		return 0, err
	}
	raw := findingIDsJSON(all)
	var blocked bool
	if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM tianqiong_ai.finding_case_blocks WHERE left_id IN (SELECT value::bigint FROM jsonb_array_elements_text(?::jsonb)) AND right_id IN (SELECT value::bigint FROM jsonb_array_elements_text(?::jsonb)))`, raw, raw).Scan(&blocked).Error; err != nil {
		return 0, err
	}
	if blocked {
		return 0, aiapp.ErrTaskControlConflict
	}
	var caseID int64
	if len(groups) == 0 {
		if err := tx.Raw(`INSERT INTO tianqiong_ai.finding_cases(task_id,origin_task_id,tenant_id,owner_user_id,title,reason)
 SELECT id,id,tenant_id,owner_user_id,?,? FROM public.ai_task WHERE id=? RETURNING id`, title, reason, taskID).Row().Scan(&caseID); err != nil {
			return 0, err
		}
	} else {
		caseID = groups[0]
	}
	changed := int64(0)
	for _, findingID := range all {
		result := tx.Exec(`INSERT INTO tianqiong_ai.finding_case_members(finding_id,case_id) VALUES(?,?)
 ON CONFLICT(finding_id) DO UPDATE SET case_id=EXCLUDED.case_id WHERE tianqiong_ai.finding_case_members.case_id<>EXCLUDED.case_id`, findingID, caseID)
		if result.Error != nil {
			return 0, result.Error
		}
		changed += result.RowsAffected
	}
	for index, group := range groups {
		if index == 0 {
			continue
		}
		if err := tx.Exec(`UPDATE tianqiong_ai.finding_cases SET active=FALSE,version=version+1 WHERE id=? AND task_id=?`, group, taskID).Error; err != nil {
			return 0, err
		}
	}
	if changed == 0 {
		return caseID, nil
	}
	if err := tx.Exec(`UPDATE tianqiong_ai.finding_cases SET version=version+1 WHERE id=? AND task_id=?`, caseID, taskID).Error; err != nil {
		return 0, err
	}
	return caseID, tx.Exec(`INSERT INTO tianqiong_ai.finding_case_events(case_id,action,actor,reason,finding_ids) VALUES(?,'merge',?,?,?::jsonb)`, caseID, actor, reason, raw).Error
}

func (s *TaskStore) DetachTaskFindingCaseMember(ctx context.Context, user int, taskID, caseID, findingID int64, reason string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockHumanGoalScope(tx, user, taskID); err != nil {
			return err
		}
		if err := setHumanFindingCaseMode(tx); err != nil {
			return err
		}
		var members []int64
		if err := tx.Raw(`SELECT m.finding_id FROM tianqiong_ai.finding_case_members m JOIN tianqiong_ai.finding_cases c ON c.id=m.case_id WHERE c.id=? AND c.task_id=? AND c.active ORDER BY m.finding_id`, caseID, taskID).Scan(&members).Error; err != nil {
			return err
		}
		if !slices.Contains(members, findingID) {
			var removed bool
			if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM tianqiong_ai.finding_case_events e
 JOIN tianqiong_ai.finding_cases c ON c.id=e.case_id WHERE c.id=? AND c.task_id=?
 AND e.action IN ('detach','member_deleted') AND e.finding_ids @> jsonb_build_array(?::bigint))`, caseID, taskID, findingID).Scan(&removed).Error; err != nil {
				return err
			}
			if removed {
				return nil
			}
			return aiapp.ErrTaskNotFound
		}
		if err := lockCaseFindings(tx, taskID, members); err != nil {
			return err
		}
		for _, other := range members {
			if other == findingID {
				continue
			}
			if err := tx.Exec(`INSERT INTO tianqiong_ai.finding_case_blocks(left_id,right_id,reason) VALUES(LEAST(?::bigint,?::bigint),GREATEST(?::bigint,?::bigint),?) ON CONFLICT DO NOTHING`, findingID, other, findingID, other, reason).Error; err != nil {
				return err
			}
		}
		if err := tx.Exec(`INSERT INTO tianqiong_ai.finding_case_events(case_id,action,actor,reason,finding_ids) VALUES(?,'detach','human',?,jsonb_build_array(?::bigint))`, caseID, reason, findingID).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DELETE FROM tianqiong_ai.finding_case_members WHERE case_id=? AND finding_id=?`, caseID, findingID).Error; err != nil {
			return err
		}
		return sweepFindingCaseSuggestions(tx, taskID)
	})
}

func (s *TaskStore) UpdateTaskFindingCaseReport(ctx context.Context, user int, taskID, caseID int64, input aiapp.FindingCaseReport) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockHumanGoalScope(tx, user, taskID); err != nil {
			return err
		}
		if err := setHumanFindingCaseMode(tx); err != nil {
			return err
		}
		return updateFindingCaseReportTx(tx, taskID, caseID, input, "human")
	})
}

func updateFindingCaseReportTx(tx *gorm.DB, taskID, caseID int64, input aiapp.FindingCaseReport, actor string) error {
	var version, reportVersion int64
	var title, severity, severityReason, report string
	err := tx.Raw(`SELECT version,report_version,title,severity,severity_reason,report FROM tianqiong_ai.finding_cases WHERE id=? AND task_id=? AND active FOR UPDATE`, caseID, taskID).Row().Scan(&version, &reportVersion, &title, &severity, &severityReason, &report)
	if errors.Is(err, sql.ErrNoRows) {
		return aiapp.ErrTaskNotFound
	}
	if err != nil {
		return err
	}
	if version != input.Version {
		return aiapp.ErrTaskControlConflict
	}
	if reportVersion == version && title == input.Title && severity == input.Severity && severityReason == input.SeverityReason && report == input.Report {
		return nil
	}
	if err = tx.Exec(`UPDATE tianqiong_ai.finding_cases SET title=?,severity=?,severity_reason=?,report=?,report_version=? WHERE id=? AND task_id=?`, input.Title, input.Severity, input.SeverityReason, input.Report, version, caseID, taskID).Error; err != nil {
		return err
	}
	return tx.Exec(`INSERT INTO tianqiong_ai.finding_case_events(case_id,action,actor,reason) VALUES(?,'report_updated',?,?)`, caseID, actor, input.SeverityReason).Error
}

package repository

import (
	"context"
	"slices"

	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

const reviewCandidateScope = ` FROM tianqiong_ai.findings f JOIN ai_task t ON t.id=f.task_id
 JOIN tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id JOIN tenant tenant ON tenant.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
 LEFT JOIN target target ON target.id=t.target_id AND target.deleted_at IS NULL
 WHERE t.tenant_id=?::uuid AND t.owner_user_id=? AND t.deleted_at IS NULL AND tenant.status='active' AND u.is_active AND m.role IN ('admin','analyst')
 AND (t.original_target_id IS NULL OR target.id IS NOT NULL)
 AND NOT EXISTS(SELECT 1 FROM ai_task_archive a WHERE a.task_id=t.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting'))
 AND (?='' OR lower(f.severity)=?) AND (?='' OR strpos(lower(concat_ws(' ',f.name,f.vulnclass,f.summary,t.name)),lower(?))>0)`

func (s *TaskStore) ListFindingReviewCandidates(ctx context.Context, user int, query, severity string, page, size int) (aiapp.FindingReviewCandidatePage, error) {
	out := aiapp.FindingReviewCandidatePage{Items: []aiapp.FindingReviewCandidate{}, Page: page, Size: size}
	args := []any{assets.DefaultTenantID, user, severity, severity, query, query}
	sourceIDs := func() ([]int64, error) {
		ids := []int64{}
		err := s.db.WithContext(ctx).Raw(`SELECT DISTINCT t.id`+reviewCandidateScope+` ORDER BY t.id`, args...).Scan(&ids).Error
		return ids, err
	}
	before, err := sourceIDs()
	if err != nil {
		return out, err
	}
	if err = s.db.WithContext(ctx).Raw(`SELECT count(*)`+reviewCandidateScope, args...).Row().Scan(&out.Total); err != nil {
		return out, err
	}
	values := append(append([]any{}, args...), size, page*size)
	rows, err := s.db.WithContext(ctx).Raw(`SELECT f.id,t.id,t.name,COALESCE(target.name,''),f.name,f.vulnclass,f.severity,f.status,left(f.summary,400),
 (SELECT m.case_id FROM tianqiong_ai.finding_case_members m JOIN tianqiong_ai.finding_cases c ON c.id=m.case_id AND c.active AND c.task_id=t.id WHERE m.finding_id=f.id),f.created_at`+reviewCandidateScope+` ORDER BY f.created_at DESC,f.id DESC LIMIT ? OFFSET ?`, values...).Rows()
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var item aiapp.FindingReviewCandidate
		if err = rows.Scan(&item.ID, &item.TaskID, &item.TaskName, &item.TargetName, &item.Name, &item.VulnClass, &item.Severity, &item.Status, &item.Summary, &item.CaseID, &item.CreatedAt); err != nil {
			rows.Close()
			return out, err
		}
		item.InGroup = item.CaseID != nil
		out.Items = append(out.Items, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	after, err := sourceIDs()
	if err != nil {
		return out, err
	}
	if !slices.Equal(before, after) {
		return aiapp.FindingReviewCandidatePage{}, aiapp.ErrTaskNotFound
	}
	return out, nil
}

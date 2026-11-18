package external

import (
	"context"
	"time"

	"github.com/google/uuid"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	assetrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
)

type taskID struct {
	ID int64 `json:"task_id"`
	Page
}
type reportID struct {
	Task    int64 `json:"task_id"`
	Finding int64 `json:"finding_id,omitempty"`
	Case    int64 `json:"case_id,omitempty"`
}
type conversationID struct {
	ID  string `json:"conversation_id"`
	Run string `json:"run_id,omitempty"`
	Page
}

type taskRecord struct {
	ID            int64     `json:"id"`
	ExplorationID int64     `json:"-"`
	Name          string    `json:"name"`
	Status        string    `json:"status"`
	UpdatedAt     time.Time `json:"updatedAt"`
	Reason        string    `json:"endReason,omitempty"`
	Summary       string    `json:"summary,omitempty"`
}

const taskFrom = ` FROM ai_task t LEFT JOIN target x ON x.id=t.target_id AND x.deleted_at IS NULL
 WHERE t.tenant_id=? AND t.owner_user_id=? AND t.deleted_at IS NULL AND (t.original_target_id IS NULL OR x.id IS NOT NULL) `

// A result may include material inherited from another task. Restricted keys
// receive only tasks whose target, selected assets and company scope are known.
func taskQuery(c Client, columns string) (string, []any) {
	args := []any{assets.DefaultTenantID, c.OwnerUserID}
	query := "SELECT " + columns + taskFrom
	if c.AllOrganizations {
		return query, args
	}
	query = assetrepo.OrganizationAssetMembershipCTE + query + `
       AND (EXISTS(SELECT 1 FROM org_targets o WHERE o.target_id=t.target_id AND o.org_id=ANY(?::integer[]))
         OR (t.target_id IS NULL AND EXISTS(SELECT 1 FROM ai_task_scope a JOIN organization o ON o.id=a.organization_id AND o.deleted_at IS NULL
            WHERE a.task_id=t.id AND a.kind='company' AND o.id=ANY(?::integer[]))))
       AND NOT EXISTS(SELECT 1 FROM ai_task_scope a WHERE a.task_id=t.id AND a.kind='company' AND NOT a.organization_id=ANY(?::integer[]))
       AND (t.target_id IS NOT NULL OR NOT EXISTS(SELECT 1 FROM ai_task_scope a WHERE a.task_id=t.id AND a.kind<>'company'))
       AND NOT EXISTS(SELECT 1 FROM ai_task_asset a WHERE a.task_id=t.id AND NOT EXISTS(
           SELECT 1 FROM org_assets o WHERE o.asset_id=a.asset_id AND o.org_id=ANY(?::integer[])))
       AND NOT EXISTS(SELECT 1 FROM ai_task_relation r WHERE r.task_id=t.id) `
	return query, append(args, c.OrganizationIDs, c.OrganizationIDs, c.OrganizationIDs, c.OrganizationIDs)
}
func (s *Service) tasks(ctx context.Context, c Client, in Page) (any, error) {
	size, offset, err := in.limits()
	if err != nil {
		return nil, err
	}
	query, args := taskQuery(c, "t.id,t.name,t.status,t.updated_at")
	rows := []taskRecord{}
	err = s.query(ctx).Raw(query+" ORDER BY t.id DESC LIMIT ? OFFSET ?", append(args, size+1, offset)...).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return listPage(rows, size), nil
}
func (s *Service) task(ctx context.Context, c Client, id int64) (taskRecord, error) {
	var item taskRecord
	if id <= 0 {
		return item, errInput
	}
	query, args := taskQuery(c, `t.id,t.exploration_id,t.name,t.status,t.updated_at,left(t.error_message,2000) AS reason,
        COALESCE((SELECT left(COALESCE(NULLIF(a.detail,''),a.summary),16000) FROM tianqiong_ai.activity a
        WHERE a.exploration_id=t.exploration_id AND a.node_id IS NULL AND a.kind IN ('result','assistant_message','text')
        AND COALESCE(NULLIF(a.detail,''),a.summary,'')<>'' ORDER BY a.id DESC LIMIT 1),'') AS summary`)
	r := s.query(ctx).Raw(query+" AND t.id=?", append(args, id)...).Scan(&item)
	if r.Error != nil {
		return item, r.Error
	}
	if r.RowsAffected == 0 {
		return item, errDenied
	}
	return item, nil
}

type findingRecord struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Severity  string `json:"severity"`
	Summary   string `json:"summary,omitempty"`
	HasReport bool   `json:"hasReport"`
}

func (s *Service) taskResult(ctx context.Context, c Client, in taskID) (any, error) {
	size, offset, err := in.limits()
	if err != nil {
		return nil, err
	}
	task, err := s.task(ctx, c, in.ID)
	if err != nil {
		return nil, err
	}
	found := []findingRecord{}
	err = s.query(ctx).Raw(`SELECT id,name,severity,left(summary,1600) AS summary,(report<>'') AS has_report
         FROM tianqiong_ai.findings WHERE task_id=? ORDER BY id LIMIT ? OFFSET ?`, in.ID, size+1, offset).Scan(&found).Error
	if err != nil {
		return nil, err
	}
	cases := []findingRecord{}
	err = s.query(ctx).Raw(`SELECT id,title AS name,severity,(report<>'') AS has_report FROM tianqiong_ai.finding_cases
         WHERE active AND task_id=? AND owner_user_id=? AND tenant_id=? AND (? OR origin_task_id=task_id)
         ORDER BY id LIMIT ? OFFSET ?`, in.ID, c.OwnerUserID, assets.DefaultTenantID, c.AllOrganizations, size+1, offset).Scan(&cases).Error
	if err != nil {
		return nil, err
	}
	return map[string]any{"task": task, "findings": listPage(found, size), "cases": listPage(cases, size)}, nil
}

func (s *Service) findingReport(ctx context.Context, c Client, in reportID) (any, error) {
	if (in.Finding <= 0) == (in.Case <= 0) || in.Finding < 0 || in.Case < 0 {
		return nil, errInput
	}
	if _, err := s.task(ctx, c, in.Task); err != nil {
		return nil, err
	}
	query := `SELECT left(report,32768) AS markdown,length(report)>32768 AS truncated FROM tianqiong_ai.findings WHERE task_id=? AND id=?`
	args := []any{in.Task, in.Finding}
	if in.Case > 0 {
		query = `SELECT left(report,32768) AS markdown,length(report)>32768 AS truncated FROM tianqiong_ai.finding_cases
           WHERE active AND task_id=? AND id=? AND owner_user_id=? AND tenant_id=? AND (? OR origin_task_id=task_id)`
		args = []any{in.Task, in.Case, c.OwnerUserID, assets.DefaultTenantID, c.AllOrganizations}
	}
	var report struct {
		Markdown  string `json:"markdown"`
		Truncated bool   `json:"truncated"`
	}
	r := s.query(ctx).Raw(query, args...).Scan(&report)
	if r.Error != nil {
		return nil, r.Error
	}
	if r.RowsAffected == 0 {
		return nil, errDenied
	}
	return report, nil
}

const chatReports = ` FROM ai_conversation_vulnerability_report p JOIN vulnerability v ON v.id=p.vulnerability_id
 JOIN target x ON x.id=v.target_id AND x.deleted_at IS NULL
 WHERE p.tenant_id=c.tenant_id AND p.owner_user_id=c.owner_user_id AND p.conversation_id=c.id
 AND (? OR EXISTS(SELECT 1 FROM organization_target ot JOIN organization o ON o.id=ot.organization_id AND o.deleted_at IS NULL
 WHERE ot.target_id=x.id AND o.id=ANY(?::integer[]))) `
const chats = ` FROM ai_conversation c WHERE c.tenant_id=? AND c.owner_user_id=?
 AND tianqiong_ai.finding_case_review_conversation_visible(c.id)
 AND (? OR EXISTS (SELECT 1 ` + chatReports + `)) `

type chatRecord struct {
	ID        string    `json:"id"`
	Title     string    `json:"title,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func chatArgs(c Client) []any {
	return []any{c.AllOrganizations, assets.DefaultTenantID, c.OwnerUserID, c.AllOrganizations, c.AllOrganizations, c.OrganizationIDs}
}

const chatSelect = `SELECT c.id::text,CASE WHEN ? THEN c.title ELSE '' END title,c.updated_at ` + chats

func (s *Service) conversations(ctx context.Context, c Client, in Page) (any, error) {
	size, offset, err := in.limits()
	if err != nil {
		return nil, err
	}
	rows := []chatRecord{}
	err = s.query(ctx).Raw(chatSelect+" ORDER BY c.updated_at DESC,c.id LIMIT ? OFFSET ?", append(chatArgs(c), size+1, offset)...).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return listPage(rows, size), nil
}

func (s *Service) conversationResult(ctx context.Context, c Client, in conversationID) (any, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, errInput
	}
	if in.Run != "" {
		if _, err = uuid.Parse(in.Run); err != nil {
			return nil, errInput
		}
	}
	size, offset, err := in.limits()
	if err != nil {
		return nil, err
	}
	var chat chatRecord
	r := s.query(ctx).Raw(chatSelect+" AND c.id=?", append(chatArgs(c), id)...).Scan(&chat)
	if r.Error != nil {
		return nil, r.Error
	}
	if r.RowsAffected == 0 {
		return nil, errDenied
	}
	type runResult struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Reason  string `json:"endReason,omitempty"`
		Summary string `json:"summary,omitempty"`
	}
	args := []any{c.AllOrganizations, c.AllOrganizations, id, c.OwnerUserID, assets.DefaultTenantID, c.AllOrganizations, c.AllOrganizations, c.OrganizationIDs}
	runQuery := `SELECT r.id::text, CASE WHEN r.status IN ('running','stopping') AND r.lease_until<=clock_timestamp() THEN 'interrupted' ELSE r.status END AS status,
      CASE WHEN ? THEN left(COALESCE(NULLIF(r.terminal_reason,''),r.error_message),2000) ELSE '' END AS reason,
      CASE WHEN ? THEN COALESCE((SELECT left(COALESCE(NULLIF(a.detail,''),a.summary),16000) FROM ai_conversation_activity a
        WHERE a.run_id=r.id AND a.kind IN ('result','assistant_message','text') AND COALESCE(NULLIF(a.detail,''),a.summary,'')<>''
        ORDER BY CASE WHEN a.kind='assistant_message' THEN 0 ELSE 1 END,a.id DESC LIMIT 1),'') ELSE '' END AS summary
      FROM ai_conversation c JOIN ai_run r ON r.conversation_id=c.id AND r.owner_user_id=c.owner_user_id AND r.tenant_id=c.tenant_id
      WHERE c.id=? AND c.owner_user_id=? AND c.tenant_id=? AND (? OR EXISTS(SELECT 1 ` + chatReports + ` AND p.run_id=r.id))`
	if in.Run != "" {
		runQuery += " AND r.id=?"
		args = append(args, in.Run)
	}
	var run runResult
	r = s.query(ctx).Raw(runQuery+" ORDER BY r.created_at DESC,r.id DESC LIMIT 1", args...).Scan(&run)
	if r.Error != nil {
		return nil, r.Error
	}
	if run.ID == "" && in.Run != "" {
		return nil, errDenied
	}
	found := []findingRecord{}
	if run.ID != "" {
		r = s.query(ctx).Raw(`SELECT f.* FROM ai_conversation c CROSS JOIN LATERAL
          (SELECT v.id,COALESCE(NULLIF(v.display_name,''),p.report->>'name',v.vuln_type) AS name,v.severity,left(p.report->>'summary',1600) AS summary
          `+chatReports+` AND p.run_id=? ORDER BY p.id LIMIT ? OFFSET ?) f
          WHERE c.id=? AND c.owner_user_id=? AND c.tenant_id=?`, c.AllOrganizations, c.OrganizationIDs, run.ID, size+1, offset, id, c.OwnerUserID, assets.DefaultTenantID).Scan(&found)
		if r.Error != nil {
			return nil, r.Error
		}
	}
	return map[string]any{"conversation": chat, "run": run, "findings": listPage(found, size), "findingsOnly": !c.AllOrganizations}, nil
}

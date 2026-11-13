package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

func (s *AssetRegistryStore) agentCompanyTarget(ctx context.Context, user int, taskID int64, run string) (int, error) {
	var target int
	if taskID > 0 {
		err := s.db.WithContext(ctx).Raw(`SELECT COALESCE(t.target_id,0) FROM ai_task t WHERE t.tenant_id=?::uuid AND t.id=? AND t.owner_user_id=? AND tianqiong_ai.task_run_live(t.id,t.owner_user_id,?::uuid)`, assetapp.DefaultTenantID, taskID, user, run).Row().Scan(&target)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, assetapp.ErrForbidden
		}
		return target, err
	}
	err := s.db.WithContext(ctx).Raw(`SELECT 0 FROM ai_run r JOIN ai_conversation c ON c.id=r.conversation_id AND c.tenant_id=r.tenant_id AND c.owner_user_id=r.owner_user_id JOIN tenant_membership m ON m.tenant_id=r.tenant_id AND m.user_id=r.owner_user_id JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
 WHERE r.tenant_id=?::uuid AND r.owner_user_id=? AND r.id=?::uuid AND r.status='running' AND r.lease_until>clock_timestamp() AND m.role IN ('admin','analyst') AND u.is_active AND t.status='active' AND tianqiong_ai.finding_case_review_conversation_visible(c.id)
 AND NOT EXISTS(SELECT 1 FROM ai_agent_trigger_job job LEFT JOIN ai_task task ON task.id=job.source_task_id AND task.tenant_id=job.tenant_id AND task.owner_user_id=job.owner_user_id WHERE job.run_id=r.id AND job.source_task_id IS NOT NULL AND (job.status<>'running' OR task.id IS NULL OR task.deleted_at IS NOT NULL OR (task.original_target_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM target x WHERE x.id=task.target_id AND x.deleted_at IS NULL)) OR EXISTS(SELECT 1 FROM ai_task_archive a WHERE a.task_id=task.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting'))))`, assetapp.DefaultTenantID, user, run).Row().Scan(&target)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, assetapp.ErrForbidden
	}
	return target, err
}

func (s *AssetRegistryStore) ListAgentCompanies(ctx context.Context, user int, taskID int64, run, search string) (assetapp.AgentCompanyPage, error) {
	out := assetapp.AgentCompanyPage{Companies: []assetapp.AgentCompany{}}
	target, err := s.agentCompanyTarget(ctx, user, taskID, run)
	if err != nil {
		return out, err
	}
	filter, args := assetFilters(assetapp.AssetQuery{TenantID: assetapp.DefaultTenantID, TargetID: target}, false)
	query := organizationAssetCTE + `, counts AS (SELECT oa.org_id,count(DISTINCT e.id) AS total FROM org_assets oa JOIN asset_entity e ON e.id=oa.asset_id WHERE ` + filter + ` GROUP BY oa.org_id)
 SELECT o.id,o.name,COALESCE(counts.total,0),COALESCE((SELECT jsonb_agg(x.name ORDER BY x.name) FROM (SELECT DISTINCT t.name FROM organization_target link JOIN target t ON t.id=link.target_id AND t.deleted_at IS NULL WHERE link.organization_id=o.id AND (?=0 OR t.id=?) ORDER BY t.name LIMIT 101) x),'[]'::jsonb)
 FROM organization o LEFT JOIN counts ON counts.org_id=o.id WHERE o.deleted_at IS NULL AND (?='' OR strpos(lower(o.name),lower(?))>0) AND (?=0 OR EXISTS(SELECT 1 FROM org_targets ot WHERE ot.org_id=o.id AND ot.target_id=?)) ORDER BY o.name,o.id LIMIT 201`
	args = append(args, target, target, search, search, target, target)
	rows, err := s.db.WithContext(ctx).Raw(query, args...).Rows()
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var item assetapp.AgentCompany
		var scope []byte
		if err = rows.Scan(&item.ID, &item.Name, &item.AssetCount, &scope); err != nil {
			return out, err
		}
		if err = json.Unmarshal(scope, &item.Scope); err != nil {
			return out, err
		}
		if len(item.Scope) > 100 {
			item.Scope = item.Scope[:100]
			item.ScopeTruncated = true
		}
		out.Companies = append(out.Companies, item)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	current, err := s.agentCompanyTarget(ctx, user, taskID, run)
	if err != nil || current != target {
		if err == nil {
			err = assetapp.ErrForbidden
		}
		return assetapp.AgentCompanyPage{}, err
	}
	if len(out.Companies) > 200 {
		out.Companies = out.Companies[:200]
		out.Truncated = true
	}
	out.Count = len(out.Companies)
	return out, nil
}

var _ assetapp.AgentCompanyStore = (*AssetRegistryStore)(nil)

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"

	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"gorm.io/gorm"
)

const taskFindingScope = `WITH current_task AS (
 SELECT t.id,t.tenant_id,t.owner_user_id,t.target_id,t.exploration_id FROM ai_task t JOIN tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id JOIN tenant tenant ON tenant.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
 WHERE t.id=? AND t.tenant_id=?::uuid AND t.owner_user_id=? AND t.deleted_at IS NULL AND tenant.status='active' AND u.is_active AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target x WHERE x.id=t.target_id AND x.deleted_at IS NULL))
), sources AS (
 SELECT c.id,c.tenant_id,c.owner_user_id,c.target_id,c.exploration_id,FALSE AS inherited FROM current_task c
 UNION ALL SELECT s.id,s.tenant_id,s.owner_user_id,s.target_id,s.exploration_id,TRUE FROM current_task c JOIN ai_task_relation r ON r.task_id=c.id AND r.tenant_id=c.tenant_id JOIN ai_task s ON s.id=r.source_task_id AND s.tenant_id=c.tenant_id AND s.owner_user_id=c.owner_user_id WHERE s.deleted_at IS NULL AND (s.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target x WHERE x.id=s.target_id AND x.deleted_at IS NULL)) AND NOT EXISTS(SELECT 1 FROM ai_task_archive a WHERE a.task_id=s.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting'))
), eligible_all AS (
 SELECT f.*,s.inherited,s.id AS source_task_id,c.tenant_id AS context_tenant,c.target_id AS context_target
 FROM current_task c JOIN sources s ON TRUE JOIN tianqiong_ai.findings f ON f.task_id=s.id
 WHERE (c.target_id IS NULL OR s.id=c.id OR s.target_id=c.target_id OR (jsonb_array_length(CASE WHEN jsonb_typeof(f.asset_ids)='array' THEN f.asset_ids ELSE '[]'::jsonb END)>0 AND NOT EXISTS(
  SELECT 1 FROM jsonb_array_elements_text(CASE WHEN jsonb_typeof(f.asset_ids)='array' THEN f.asset_ids ELSE '[]'::jsonb END) ref(value)
  WHERE NOT EXISTS(SELECT 1 FROM asset_entity a WHERE a.id::text=ref.value AND a.tenant_id=c.tenant_id AND (
   EXISTS(SELECT 1 FROM asset_website_binding b JOIN website x ON x.id=b.website_id WHERE b.asset_id=a.id AND x.target_id=c.target_id) OR
   EXISTS(SELECT 1 FROM asset_endpoint_binding b JOIN endpoint x ON x.id=b.endpoint_id WHERE b.asset_id=a.id AND x.target_id=c.target_id) OR
   EXISTS(SELECT 1 FROM asset_subdomain_binding b JOIN subdomain x ON x.id=b.subdomain_id WHERE b.asset_id=a.id AND x.target_id=c.target_id) OR
   EXISTS(SELECT 1 FROM asset_host_port_binding b JOIN host_port_mapping x ON x.id=b.host_port_id WHERE b.asset_id=a.id AND x.target_id=c.target_id) OR
   EXISTS(SELECT 1 FROM asset_observation o WHERE o.asset_id=a.id AND o.tenant_id=c.tenant_id AND o.target_id=c.target_id) OR
   EXISTS(SELECT 1 FROM asset_application_visible_website w JOIN website x ON x.id=w.website_id WHERE w.tenant_id=c.tenant_id AND w.application_id=a.id AND x.target_id=c.target_id) OR
   EXISTS(SELECT 1 FROM asset_root_domain_target r JOIN target x ON x.id=r.target_id AND x.deleted_at IS NULL WHERE r.tenant_id=c.tenant_id AND r.root_asset_id=a.id AND r.target_id=c.target_id AND x.type='domain' AND x.name=a.label AND a.asset_type='root_domain')
  )))))
), eligible AS (
 SELECT * FROM eligible_all f WHERE (?=false OR NOT f.inherited) AND (?=0 OR f.id=?) AND (?='' OR lower(f.severity)=?) AND (?='' OR strpos(lower(concat_ws(' ',f.name,f.vulnclass,f.summary)),lower(?))>0)
) `

func (s *TaskStore) ListTaskFindings(ctx context.Context, user int, taskID int64, filter aiapp.TaskFindingFilter, detail bool) (aiapp.TaskFindingPage, error) {
	out := aiapp.TaskFindingPage{Items: []aiapp.TaskFinding{}, Page: filter.Page, Size: filter.Size}
	tenant, err := s.TrafficTaskTenant(ctx, user, taskID)
	if err != nil {
		return out, err
	}
	sources, err := s.findingSources(ctx, user, taskID, tenant)
	if err != nil {
		return out, err
	}
	args := []any{taskID, tenant, user, filter.OwnOnly, filter.ID, filter.ID, filter.Severity, filter.Severity, filter.Query, filter.Query}
	if err = s.db.WithContext(ctx).Raw(taskFindingScope+`SELECT count(*) FROM eligible`, args...).Row().Scan(&out.Total); err != nil {
		return out, err
	}
	queryArgs := append(append([]any{}, args...), detail, detail, detail, filter.Size, filter.Page*filter.Size)
	rows, err := s.db.WithContext(ctx).Raw(taskFindingScope+`SELECT f.id,f.task_id,f.node_id,COALESCE((SELECT t.name FROM ai_task t WHERE t.id=f.source_task_id),''),f.name,f.vulnclass,f.severity,f.status,f.summary,
 CASE WHEN ? THEN f.evidence ELSE '' END,CASE WHEN ? THEN f.report ELSE '' END,f.evidence_version,f.report_evidence_version,(f.report<>''),
 (SELECT count(*) FROM tianqiong_ai.finding_traffic_bindings b WHERE b.finding_id=f.id),
 (SELECT link.vulnerability_id FROM ai_finding_risk_link link JOIN vulnerability v ON v.id=link.vulnerability_id JOIN target x ON x.id=v.target_id AND x.deleted_at IS NULL WHERE link.finding_id=f.id AND (f.context_target IS NULL OR v.target_id=f.context_target) LIMIT 1),
 (SELECT member.case_id FROM tianqiong_ai.finding_case_members member JOIN tianqiong_ai.finding_cases c ON c.id=member.case_id AND c.active WHERE member.finding_id=f.id
  AND NOT EXISTS(SELECT 1 FROM tianqiong_ai.finding_case_members hidden WHERE hidden.case_id=c.id AND NOT EXISTS(SELECT 1 FROM eligible_all visible WHERE visible.id=hidden.finding_id))),
 COALESCE((SELECT jsonb_agg(jsonb_build_object('id',a.id::text,'type',a.asset_type,'label',a.label) ORDER BY a.id) FROM asset_entity a WHERE a.tenant_id=f.context_tenant AND a.id::text IN (SELECT jsonb_array_elements_text(CASE WHEN jsonb_typeof(f.asset_ids)='array' THEN f.asset_ids ELSE '[]'::jsonb END))),'[]'::jsonb),f.inherited,f.created_at,
 CASE WHEN ? THEN (SELECT encode(sha256(convert_to(to_jsonb(original)::text,'UTF8')),'hex') FROM tianqiong_ai.findings original WHERE original.id=f.id) ELSE '' END
 FROM eligible f ORDER BY f.created_at DESC,f.id DESC LIMIT ? OFFSET ?`, queryArgs...).Rows()
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var item aiapp.TaskFinding
		var assetJSON []byte
		if err = rows.Scan(&item.ID, &item.TaskID, &item.NodeID, &item.TaskName, &item.Name, &item.VulnClass, &item.Severity, &item.Status, &item.Summary, &item.Evidence, &item.Report, &item.EvidenceVersion, &item.ReportEvidenceVersion, &item.HasReport, &item.TrafficCount, &item.RiskID, &item.CaseID, &assetJSON, &item.Inherited, &item.CreatedAt, &item.Fingerprint); err != nil {
			return out, err
		}
		if err = json.Unmarshal(assetJSON, &item.Assets); err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	current, err := s.TrafficTaskTenant(ctx, user, taskID)
	if err != nil {
		return aiapp.TaskFindingPage{}, err
	}
	if current != tenant {
		return aiapp.TaskFindingPage{}, aiapp.ErrTaskNotFound
	}
	currentSources, err := s.findingSources(ctx, user, taskID, tenant)
	if err != nil {
		return aiapp.TaskFindingPage{}, err
	}
	if !slices.Equal(sources, currentSources) {
		return aiapp.TaskFindingPage{}, aiapp.ErrTaskNotFound
	}
	checked := map[int64]bool{}
	for _, item := range out.Items {
		if item.Inherited && !checked[item.TaskID] {
			if _, err = s.TrafficTaskTenant(ctx, user, item.TaskID); err != nil {
				return aiapp.TaskFindingPage{}, err
			}
			checked[item.TaskID] = true
		}
	}
	return out, nil
}
func (s *TaskStore) findingSources(ctx context.Context, user int, taskID int64, tenant string) ([]int64, error) {
	ids := []int64{}
	err := s.db.WithContext(ctx).Raw(`SELECT source.id FROM ai_task_relation r JOIN ai_task source ON source.id=r.source_task_id AND source.tenant_id=r.tenant_id WHERE r.task_id=? AND r.tenant_id=?::uuid AND source.owner_user_id=? AND source.deleted_at IS NULL AND (source.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target x WHERE x.id=source.target_id AND x.deleted_at IS NULL)) AND NOT EXISTS(SELECT 1 FROM ai_task_archive a WHERE a.task_id=source.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting')) ORDER BY source.id`, taskID, tenant, user).Scan(&ids).Error
	return ids, err
}
func (s *TaskStore) UpdateTaskFindingStatus(ctx context.Context, user int, taskID, findingID int64, status string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockHumanGoalScope(tx, user, taskID); err != nil {
			return err
		}
		var id int64
		err := tx.Raw(`SELECT id FROM tianqiong_ai.findings WHERE id=? AND task_id=? FOR UPDATE SKIP LOCKED`, findingID, taskID).Row().Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			var exists bool
			if queryErr := tx.Raw(`SELECT EXISTS(SELECT 1 FROM tianqiong_ai.findings WHERE id=? AND task_id=?)`, findingID, taskID).Scan(&exists).Error; queryErr != nil {
				return queryErr
			}
			if exists {
				return aiapp.ErrTaskControlConflict
			}
			return aiapp.ErrTaskNotFound
		}
		if err != nil {
			return err
		}
		if err = tx.Exec(`SELECT set_config('tianqiong.execution_mode','human_finding_status',TRUE),set_config('tianqiong.finding_id',?,TRUE)`, strconv.FormatInt(findingID, 10)).Error; err != nil {
			return err
		}
		return tx.Exec(`UPDATE tianqiong_ai.findings SET status=? WHERE id=? AND task_id=?`, status, findingID, taskID).Error
	})
}

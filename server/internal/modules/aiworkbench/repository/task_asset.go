package repository

import (
	"context"

	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
)

func (s *TaskStore) ListTaskAssets(ctx context.Context, user int, id, after int64) ([]aiapp.TaskAsset, error) {
	task, err := s.GetTask(ctx, user, id)
	if err != nil {
		return nil, err
	}
	items := []aiapp.TaskAsset{}
	err = s.db.WithContext(ctx).Raw(`WITH scopes AS (
 SELECT ?::bigint AS task_id,?::bigint AS exploration_id
 UNION ALL SELECT source.id,source.exploration_id FROM ai_task_relation r JOIN ai_task source ON source.id=r.source_task_id AND source.tenant_id=r.tenant_id WHERE r.task_id=? AND r.tenant_id=? AND source.owner_user_id=? AND source.deleted_at IS NULL AND (source.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=source.target_id AND deleted_at IS NULL))
), candidates AS (
 SELECT a.asset_id,(array_agg(a.source ORDER BY (a.task_id=?) DESC,a.task_id))[1] AS original_source,bool_or(a.task_id=?) AS own,COALESCE(min(a.task_id) FILTER (WHERE a.task_id<>?),0) AS source_task_id
 FROM scopes JOIN ai_task_asset a ON a.task_id=scopes.task_id WHERE a.tenant_id=? GROUP BY a.asset_id
)
 SELECT e.asset_id AS id,e.asset_type,e.label,CASE WHEN c.asset_id IS NULL THEN 'scope' WHEN c.own THEN c.original_source ELSE 'inherited' END AS source,COALESCE(NOT c.own,FALSE) AS inherited,CASE WHEN c.own THEN 0 ELSE COALESCE(c.source_task_id,0) END AS source_task_id,
 (SELECT count(DISTINCT n.id) FROM tianqiong_ai.exploration_anchors anchor JOIN tianqiong_ai.exploration_nodes n ON n.id=anchor.node_id JOIN scopes ON scopes.exploration_id=n.exploration_id WHERE anchor.asset_id=e.asset_id AND n.kind='fact' AND n.state='confirmed') AS fact_count
 FROM tianqiong_ai.task_scope_assets(?,?) e LEFT JOIN candidates c ON c.asset_id=e.asset_id WHERE e.asset_id>? ORDER BY e.asset_id LIMIT 101`,
		task.ID, task.ExplorationID, task.ID, task.TenantID, user, task.ID, task.ID, task.ID, task.TenantID, task.ID, user, after).Scan(&items).Error
	return items, err
}

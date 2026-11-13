package repository

import (
	"context"
	"strings"

	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

// A result is attributable only to the same task run, worker, graph node and
// tool-use ID, and only until another identical call begins. Pre-attribution
// records stay visible but are deliberately not paired with guessed results.
func (s *TaskStore) ListTaskToolExecutions(ctx context.Context, user int, query string, before int64, limit int, detail bool) ([]aiapp.TaskToolExecution, error) {
	comparison := "(?=0 OR u.id<?)"
	input, output := "left(COALESCE(u.detail,''),500)", "left(COALESCE(result.detail,''),500)"
	if detail {
		comparison = "(? > 0 AND u.id=?)"
		input = "COALESCE(u.detail,'')"
		output = "COALESCE(result.detail,'')"
	}
	statement := `SELECT u.id,task.id AS task_id,task.name AS task_name,COALESCE(u.execution_id::text,'') AS run_id,COALESCE(u.tool,'') AS tool,` + input + ` AS input,` + output + ` AS output,
  CASE WHEN result.id IS NULL THEN CASE WHEN tianqiong_ai.task_run_live(task.id,task.owner_user_id,u.execution_id) THEN 'running' ELSE 'unknown' END
   WHEN result.is_error THEN 'failed' ELSE 'succeeded' END AS status,u.created_at
 FROM tianqiong_ai.activity u JOIN ai_task task ON task.exploration_id=u.exploration_id
 JOIN tenant_membership m ON m.tenant_id=task.tenant_id AND m.user_id=task.owner_user_id
 JOIN tenant tenant ON tenant.id=m.tenant_id JOIN auth_user a ON a.id=m.user_id
 LEFT JOIN LATERAL (
  SELECT x.id,x.detail,x.is_error FROM tianqiong_ai.activity x
  WHERE u.execution_id IS NOT NULL AND u.tool_use_id IS NOT NULL
   AND x.exploration_id=u.exploration_id AND x.execution_id=u.execution_id AND x.worker IS NOT DISTINCT FROM u.worker
   AND x.node_id IS NOT DISTINCT FROM u.node_id AND x.tool_use_id=u.tool_use_id AND x.kind='tool_result' AND x.id>u.id
   AND NOT EXISTS(SELECT 1 FROM tianqiong_ai.activity later WHERE later.exploration_id=u.exploration_id AND later.execution_id=u.execution_id
    AND later.worker IS NOT DISTINCT FROM u.worker AND later.node_id IS NOT DISTINCT FROM u.node_id AND later.tool_use_id=u.tool_use_id
    AND later.kind='tool_use' AND later.id>u.id AND later.id<x.id)
  ORDER BY x.id LIMIT 1
 ) result ON TRUE
 WHERE task.tenant_id=? AND task.owner_user_id=? AND task.deleted_at IS NULL AND tenant.status='active' AND a.is_active=TRUE AND u.kind='tool_use'
  AND (?='' OR u.tool ILIKE ? ESCAPE '\' OR u.detail ILIKE ? ESCAPE '\') AND ` + comparison + `
 ORDER BY u.id DESC LIMIT ?`
	pattern := "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(query) + "%"
	items := []aiapp.TaskToolExecution{}
	err := s.db.WithContext(ctx).Raw(statement, assetapp.DefaultTenantID, user, query, pattern, pattern, before, before, limit).Scan(&items).Error
	return items, err
}

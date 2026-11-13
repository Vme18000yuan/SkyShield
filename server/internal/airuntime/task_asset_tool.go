package airuntime

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
)

func taskUntestedAssetsTool(db *sql.DB, task TaskExecution) tool.CoreTool {
	return tool.Build(tool.Spec{
		Name:        "list_untested_assets",
		Description: "查询当前任务和直接来源任务的范围内尚无已确认事实锚点的资产。结果仅为粗略线索，不代表完整的安全测试。",
		Schema:      map[string]any{"type": "object", "properties": map[string]any{"type": map[string]any{"type": "string", "enum": []string{"root_domain", "subdomain", "ip", "app", "service", "endpoint"}}, "page": map[string]any{"type": "integer", "minimum": 1}, "page_size": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}}},
		ReadOnly:    func(json.RawMessage) bool { return true }, Concurrent: func(json.RawMessage) bool { return true },
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(ctx context.Context, raw json.RawMessage, _ *tool.ToolContext) (tool.Result, error) {
			var request struct {
				Type     string `json:"type"`
				Page     int    `json:"page"`
				PageSize int    `json:"page_size"`
			}
			if err := json.Unmarshal(raw, &request); err != nil {
				return tool.Errorf("资产查询参数无效"), nil
			}
			request.Type = strings.TrimSpace(request.Type)
			if request.Page <= 0 {
				request.Page = 1
			}
			if request.PageSize <= 0 {
				request.PageSize = 10
			}
			if request.Page > 10000 || request.PageSize > 100 {
				return tool.Errorf("资产分页超出范围"), nil
			}
			switch request.Type {
			case "", "root_domain", "subdomain", "ip", "app", "service", "endpoint":
			default:
				return tool.Errorf("资产类型无效"), nil
			}
			var total int
			var assets []byte
			var permitted bool
			err := db.QueryRowContext(ctx, `WITH permitted AS (
    SELECT t.id FROM public.ai_task t JOIN public.tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id
    JOIN public.auth_user u ON u.id=m.user_id JOIN public.tenant tenant ON tenant.id=m.tenant_id
    WHERE t.id=$1 AND t.owner_user_id=$2 AND tianqiong_ai.task_run_live(t.id,t.owner_user_id,$3::uuid)
    AND t.deleted_at IS NULL AND u.is_active=TRUE AND tenant.status='active' AND m.role IN ('admin','analyst')
    AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM public.target WHERE id=t.target_id AND deleted_at IS NULL))
   ), candidates AS (
    SELECT a.asset_id AS id,a.asset_type,a.label FROM permitted p
    CROSS JOIN LATERAL tianqiong_ai.task_scope_assets(p.id,$2) a
    WHERE NOT a.tested AND ($4='' OR a.asset_type=$4)
   ), selected AS (SELECT id,asset_type,label FROM candidates ORDER BY id LIMIT $5 OFFSET $6)
   SELECT (SELECT count(*) FROM candidates),COALESCE((SELECT jsonb_agg(jsonb_build_object('id',id,'type',asset_type,'label',label) ORDER BY id) FROM selected),'[]'::jsonb),EXISTS(SELECT 1 FROM permitted)`,
				task.ID, task.UserID, task.RunID, request.Type, request.PageSize, (request.Page-1)*request.PageSize).Scan(&total, &assets, &permitted)
			if err != nil || !permitted {
				return tool.Errorf("任务资产范围不可用"), nil
			}
			output, err := json.Marshal(map[string]any{"assets": json.RawMessage(assets), "total": total, "page": request.Page, "page_size": request.PageSize})
			if err != nil {
				return tool.Errorf("资产结果编码失败"), nil
			}
			return tool.Text(string(output)), nil
		},
	})
}

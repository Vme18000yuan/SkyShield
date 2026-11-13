package airuntime

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Autumn-27/artex/agent"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	"github.com/lib/pq"
)

// The ARTEX DSL is compiled to parameters only. The projection below reads the
// existing asset registry and active evidence; it does not create a second store.
func taskListAssetsTool(db *sql.DB, task TaskExecution) tool.CoreTool {
	return assetDSLTool(db, task.UserID, &task)
}

// GlobalAssetDSLTools provides ARTEX's list_assets to an owned conversation.
// Task agents use the separately fenced task implementation instead.
func GlobalAssetDSLTools(db *sql.DB, user int) []tool.CoreTool {
	if db == nil || user <= 0 {
		return nil
	}
	return []tool.CoreTool{assetDSLTool(db, user, nil)}
}

func assetDSLTool(db *sql.DB, user int, task *TaskExecution) tool.CoreTool {
	description := "查询当前账号可访问的统一资产。支持 id/ids，或 ARTEX DSL：field=value 模糊、field==value 精确、数字比较及 AND/OR/括号；未给 ID 时必须提供 DSL。可按 type、limit、offset 筛选。"
	if task != nil {
		description = "在当前任务和直接来源任务的资产范围中查询。支持 id/ids，或 ARTEX DSL：field=value 模糊、field==value 精确、数字比较及 AND/OR/括号；未给 ID 时必须提供 DSL。可按 type、limit、offset 筛选。"
	}
	return tool.Build(tool.Spec{
		Name:        "list_assets",
		Description: description,
		Schema: map[string]any{"type": "object", "properties": map[string]any{
			"dsl": map[string]any{"type": "string"}, "type": map[string]any{"type": "string"}, "id": map[string]any{"type": "integer"}, "ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}, "limit": map[string]any{"type": "integer"}, "offset": map[string]any{"type": "integer"},
		}},
		ReadOnly: func(json.RawMessage) bool { return true }, Concurrent: func(json.RawMessage) bool { return true },
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(ctx context.Context, raw json.RawMessage, _ *tool.ToolContext) (tool.Result, error) {
			var input struct {
				DSL    string  `json:"dsl"`
				Type   string  `json:"type"`
				ID     int64   `json:"id"`
				IDs    []int64 `json:"ids"`
				Limit  int     `json:"limit"`
				Offset int     `json:"offset"`
			}
			if err := json.Unmarshal(raw, &input); err != nil || input.ID < 0 || len(input.IDs) > 100 || input.Offset < 0 || input.Offset > 100000 || input.Limit > 100 || len(input.DSL) > 4096 {
				return tool.Errorf("资产查询参数无效"), nil
			}
			if input.Limit <= 0 {
				input.Limit = 10
			}
			if input.ID > 0 {
				input.IDs = []int64{input.ID}
			}
			for _, id := range input.IDs {
				if id <= 0 {
					return tool.Errorf("资产 ID 无效"), nil
				}
			}
			var identity []any
			permissionSQL, candidateSQL := taskAssetPermissionSQL, taskAssetCandidateSQL
			if task != nil {
				identity = []any{task.ID, user, task.RunID}
			} else {
				info := agent.RunInfoFrom(ctx)
				run := executionID(ctx)
				if info.TaskID > 0 || !canonicalUUID(info.SessionID) || !canonicalUUID(run) {
					return tool.Errorf("资产查询需要有效的对话运行上下文"), nil
				}
				identity = []any{info.SessionID, user, run}
				permissionSQL, candidateSQL = globalAssetPermissionSQL, globalAssetCandidateSQL
			}
			where := "TRUE"
			var values []any
			kind := ""
			if len(input.IDs) > 0 {
				// The candidate query applies the ID filter before projecting fields.
			} else {
				input.DSL = strings.TrimSpace(input.DSL)
				if input.DSL == "" {
					return tool.Errorf("未传 id/ids 时必须提供 DSL 查询条件"), nil
				}
				clause, compiled, err := artexdb.CompileAssetDSL(input.DSL, 4)
				if err != nil {
					return tool.Errorf("DSL 查询条件无效: " + err.Error()), nil
				}
				values = compiled
				where = clause
				input.Type = strings.TrimSpace(input.Type)
				if input.Type != "" {
					switch input.Type {
					case "root_domain", "subdomain", "ip", "app", "service", "endpoint":
					default:
						return tool.Errorf("资产类型无效"), nil
					}
					kind = input.Type
				}
			}
			queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			var permitted bool
			if err := db.QueryRowContext(queryCtx, permissionSQL+` SELECT EXISTS(SELECT 1 FROM permitted)`, identity...).Scan(&permitted); err != nil || !permitted {
				return tool.Errorf("资产查询不可用"), nil
			}
			idsFilter := input.IDs
			if idsFilter == nil {
				idsFilter = []int64{}
			}
			decoded := []json.RawMessage{}
			remainingOffset, cursor, totalBytes := input.Offset, int64(0), 0
			for len(decoded) < input.Limit {
				batch, err := assetCandidateIDs(queryCtx, db, permissionSQL+candidateSQL, identity, cursor, idsFilter, kind)
				if err != nil {
					return tool.Errorf("资产查询不可用"), nil
				}
				if len(batch) == 0 {
					break
				}
				args := append(append([]any{}, identity...), pq.Int64Array(batch))
				args = append(args, values...)
				args = append(args, input.Limit-len(decoded), remainingOffset)
				projection := permissionSQL + assetProjectionHeadSQL + boundedAssetSourceSQL + assetProjectionTailSQL
				query := projection + `, matched AS MATERIALIZED (SELECT * FROM projected WHERE ` + where + `) SELECT EXISTS(SELECT 1 FROM permitted),(SELECT count(*) FROM matched),COALESCE((SELECT jsonb_agg(to_jsonb(page)||jsonb_build_object('company_id',page.company_ids[1]) ORDER BY page.id DESC) FROM (SELECT * FROM matched ORDER BY id DESC` + fmt.Sprintf(` LIMIT $%d OFFSET $%d`, len(args)-1, len(args)) + `) page),'[]'::jsonb)`
				var count int
				var result []byte
				if err = db.QueryRowContext(queryCtx, query, args...).Scan(&permitted, &count, &result); err != nil || !permitted {
					return tool.Errorf("资产查询不可用"), nil
				}
				var page []json.RawMessage
				if err = json.Unmarshal(result, &page); err != nil {
					return tool.Errorf("资产结果编码失败"), nil
				}
				for _, item := range page {
					totalBytes += len(item)
					if totalBytes > 1<<20 {
						return tool.Errorf("资产结果过大，请缩小查询范围或分页数量"), nil
					}
					decoded = append(decoded, item)
				}
				if count > remainingOffset {
					remainingOffset = 0
				} else {
					remainingOffset -= count
				}
				cursor = batch[len(batch)-1]
				if len(batch) < 250 || len(idsFilter) > 0 {
					break
				}
			}
			// Recheck visibility and the active lease after the batched reads.
			selectedIDs := make([]int64, 0, len(decoded))
			for _, raw := range decoded {
				var item struct {
					ID int64 `json:"id"`
				}
				if json.Unmarshal(raw, &item) != nil || item.ID <= 0 {
					return tool.Errorf("资产结果编码失败"), nil
				}
				selectedIDs = append(selectedIDs, item.ID)
			}
			if len(selectedIDs) > 0 {
				visible, err := assetCandidateIDs(queryCtx, db, permissionSQL+candidateSQL, identity, 0, selectedIDs, "")
				if err != nil {
					return tool.Errorf("资产查询不可用"), nil
				}
				allowed := map[int64]bool{}
				for _, id := range visible {
					allowed[id] = true
				}
				kept := decoded[:0]
				for _, raw := range decoded {
					var item struct {
						ID int64 `json:"id"`
					}
					_ = json.Unmarshal(raw, &item)
					if allowed[item.ID] {
						kept = append(kept, raw)
					}
				}
				decoded = kept
			}
			if err := db.QueryRowContext(queryCtx, permissionSQL+` SELECT EXISTS(SELECT 1 FROM permitted)`, identity...).Scan(&permitted); err != nil || !permitted {
				return tool.Errorf("资产查询不可用"), nil
			}
			out, err := json.Marshal(map[string]any{"count": len(decoded), "assets": decoded})
			if err != nil {
				return tool.Errorf("资产结果编码失败"), nil
			}
			return tool.Text(string(out)), nil
		},
	})
}

func assetCandidateIDs(ctx context.Context, db *sql.DB, query string, identity []any, cursor int64, ids []int64, kind string) ([]int64, error) {
	args := append(append([]any{}, identity...), cursor, pq.Int64Array(ids), kind)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		items = append(items, id)
	}
	return items, rows.Err()
}

const taskAssetPermissionSQL = `WITH permitted AS (
 SELECT t.id,t.tenant_id,t.owner_user_id,t.target_id FROM public.ai_task t
 JOIN public.tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id
 JOIN public.auth_user u ON u.id=m.user_id JOIN public.tenant tenant ON tenant.id=m.tenant_id
 WHERE t.id=$1 AND t.owner_user_id=$2 AND tianqiong_ai.task_run_live(t.id,t.owner_user_id,$3::uuid)
 AND t.deleted_at IS NULL AND u.is_active AND tenant.status='active' AND m.role IN ('admin','analyst')
 AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM public.target WHERE id=t.target_id AND deleted_at IS NULL))
 )`

const globalAssetPermissionSQL = `WITH permitted AS (
 SELECT c.tenant_id,c.owner_user_id,NULL::integer AS target_id FROM public.ai_conversation c
 JOIN public.ai_run r ON r.conversation_id=c.id AND r.tenant_id=c.tenant_id AND r.owner_user_id=c.owner_user_id
 JOIN public.tenant_membership m ON m.tenant_id=c.tenant_id AND m.user_id=c.owner_user_id
 JOIN public.auth_user u ON u.id=m.user_id JOIN public.tenant tenant ON tenant.id=m.tenant_id
 WHERE c.id=$1::uuid AND c.owner_user_id=$2 AND r.id=$3::uuid AND r.status='running' AND r.lease_until>clock_timestamp()
 AND u.is_active AND tenant.status='active'
)`

const assetProjectionHeadSQL = `, projected AS (
 SELECT e.id,e.asset_type::text AS type,e.label,
  COALESCE((SELECT array_agg(DISTINCT org_id ORDER BY org_id) FROM (
    SELECT ot.organization_id::bigint AS org_id FROM public.organization_target ot JOIN public.organization org ON org.id=ot.organization_id AND org.deleted_at IS NULL JOIN public.target target ON target.id=ot.target_id AND target.deleted_at IS NULL WHERE (p.target_id IS NULL OR ot.target_id=p.target_id) AND (
      EXISTS(SELECT 1 FROM public.asset_observation a WHERE a.tenant_id=e.tenant_id AND a.asset_id=e.id AND a.target_id=ot.target_id) OR
      EXISTS(SELECT 1 FROM public.asset_subdomain_binding b JOIN public.subdomain a ON a.id=b.subdomain_id WHERE b.asset_id=e.id AND a.target_id=ot.target_id) OR
      EXISTS(SELECT 1 FROM public.asset_website_binding b JOIN public.website a ON a.id=b.website_id WHERE b.asset_id=e.id AND a.target_id=ot.target_id) OR
      EXISTS(SELECT 1 FROM public.asset_endpoint_binding b JOIN public.endpoint a ON a.id=b.endpoint_id WHERE b.asset_id=e.id AND a.target_id=ot.target_id) OR
      EXISTS(SELECT 1 FROM public.asset_host_port_binding b JOIN public.host_port_mapping a ON a.id=b.host_port_id WHERE b.asset_id=e.id AND a.target_id=ot.target_id) OR
      EXISTS(SELECT 1 FROM public.asset_application_visible_website x JOIN public.website a ON a.id=x.website_id WHERE x.tenant_id=e.tenant_id AND x.application_id=e.id AND a.target_id=ot.target_id) OR
      EXISTS(SELECT 1 FROM public.asset_root_domain_visible_source r JOIN public.asset_observation o ON o.tenant_id=r.tenant_id AND o.asset_id=r.source_asset_id WHERE r.tenant_id=e.tenant_id AND r.root_asset_id=e.id AND o.target_id=ot.target_id) OR
      EXISTS(SELECT 1 FROM public.asset_root_domain_visible_source r JOIN public.asset_website_binding b ON b.asset_id=r.source_asset_id JOIN public.website a ON a.id=b.website_id WHERE r.tenant_id=e.tenant_id AND r.root_asset_id=e.id AND a.target_id=ot.target_id) OR
      EXISTS(SELECT 1 FROM public.asset_root_domain_visible_source r JOIN public.asset_subdomain_binding b ON b.asset_id=r.source_asset_id JOIN public.subdomain a ON a.id=b.subdomain_id WHERE r.tenant_id=e.tenant_id AND r.root_asset_id=e.id AND a.target_id=ot.target_id) OR
      EXISTS(SELECT 1 FROM public.asset_root_domain_target r WHERE r.tenant_id=e.tenant_id AND r.root_asset_id=e.id AND r.target_id=ot.target_id))
    UNION SELECT o.organization_id::bigint FROM public.asset_observation o JOIN public.organization org ON org.id=o.organization_id AND org.deleted_at IS NULL LEFT JOIN public.target t ON t.id=o.target_id WHERE o.tenant_id=e.tenant_id AND o.asset_id=e.id AND (p.target_id IS NULL OR o.target_id=p.target_id) AND (o.original_target_id IS NULL OR t.id IS NOT NULL AND t.deleted_at IS NULL)
  ) orgs),'{}'::bigint[]) AS company_ids,
  COALESCE((SELECT array_agg(a.task_id ORDER BY a.task_id) FROM public.ai_task_asset a JOIN public.ai_task own ON own.id=a.task_id AND own.tenant_id=a.tenant_id AND own.deleted_at IS NULL WHERE a.tenant_id=e.tenant_id AND a.asset_id=e.id AND own.owner_user_id=$2 AND (own.original_target_id IS NULL OR EXISTS(SELECT 1 FROM public.target t WHERE t.id=own.target_id AND t.deleted_at IS NULL))),'{}'::bigint[]) AS task_ids,
  COALESCE(NULLIF(obs.payload->>'domain',''),CASE WHEN e.asset_type IN ('root_domain','subdomain','service','endpoint') THEN tianqiong_ai.scope_host(e.label) END,'') AS domain,
  COALESCE(CASE WHEN e.asset_type='root_domain' THEN e.label END,(SELECT root.label FROM public.asset_root_domain_source r JOIN public.asset_entity root ON root.id=r.root_asset_id AND root.tenant_id=r.tenant_id WHERE r.tenant_id=e.tenant_id AND r.source_asset_id=e.id LIMIT 1),'') AS root_domain,
  COALESCE(NULLIF(obs.payload->>'ip',''),NULLIF(obs.payload->>'serviceIp',''),NULLIF(obs.payload->'extra'->>'ip',''),CASE WHEN e.asset_type='ip' THEN e.label END,CASE WHEN tianqiong_ai.scope_inet(tianqiong_ai.scope_host(e.label)) IS NOT NULL THEN tianqiong_ai.scope_host(e.label) END,'') AS ip,
  COALESCE(CASE WHEN e.asset_type IN ('service','endpoint') AND e.label ~* '^https?://' THEN e.label END,'') AS url,
  COALESCE(NULLIF(obs.payload->>'pageTitle',''),web.title,'') AS page_title,
  COALESCE(obs.payload->>'icp','') AS icp,COALESCE(obs.payload->>'appIcp','') AS app_icp,
  COALESCE(obs.payload->>'serviceName','') AS service_name,COALESCE(CASE WHEN e.asset_type='app' THEN e.label END,'') AS app_name,
  COALESCE(obs.payload->>'description','') AS app_description,COALESCE(obs.payload->>'bundleId','') AS bundle_id,COALESCE(obs.payload->>'category','') AS category,
  COALESCE(obs.payload->>'faviconMmh3','') AS favicon_mmh3,
  COALESCE(obs.payload->'recordValue','[]'::jsonb) AS record_value,
  (CASE WHEN jsonb_typeof(obs.payload->'openPorts')='array' THEN obs.payload->'openPorts' ELSE '[]'::jsonb END) || COALESCE((SELECT jsonb_agg(jsonb_build_object('port',h.port) ORDER BY h.port) FROM public.asset_host_port_binding b JOIN public.host_port_mapping h ON h.id=b.host_port_id JOIN public.target t ON t.id=h.target_id AND t.deleted_at IS NULL WHERE b.asset_id=e.id AND (p.target_id IS NULL OR h.target_id=p.target_id)),'[]'::jsonb) AS open_ports,
  COALESCE(obs.payload->'params','[]'::jsonb) AS params,
  COALESCE(e.method,'') AS method,COALESCE(e.record_type,'') AS record_type,
  CASE WHEN e.asset_type='service' AND e.label ~* '^https?://' THEN 'http' WHEN e.asset_type='service' THEN 'other' ELSE '' END AS service_type,
  CASE WHEN COALESCE(obs.payload->>'port','') ~ '^[0-9]{1,5}$' THEN (obs.payload->>'port')::integer ELSE substring(public.tq_inventory_host_port(e.label) FROM ':([0-9]+)$')::integer END AS port,
  COALESCE(portset.ports,'{}'::integer[]) AS ports,
  COALESCE(web.status_code,CASE WHEN COALESCE(obs.payload->>'statusCode','') ~ '^[0-9]{1,6}$' THEN (obs.payload->>'statusCode')::integer END) AS status_code,
  COALESCE(web.content_length,CASE WHEN COALESCE(obs.payload->>'contentLength','') ~ '^[0-9]{1,12}$' THEN (obs.payload->>'contentLength')::bigint END) AS content_length,
  CASE WHEN jsonb_typeof(obs.payload->'technologies')='array' THEN ARRAY(SELECT jsonb_array_elements_text(obs.payload->'technologies')) ELSE COALESCE(web.tech,'{}'::varchar[])::text[] END AS technologies,
  CASE WHEN jsonb_typeof(obs.payload->'boundDomains')='array' THEN ARRAY(SELECT jsonb_array_elements_text(obs.payload->'boundDomains')) ELSE '{}'::text[] END AS bound_domains,
  e.last_seen_at AS last_seen`

const taskAssetCandidateSQL = ` SELECT scope.asset_id FROM permitted p CROSS JOIN LATERAL tianqiong_ai.task_scope_assets(p.id,p.owner_user_id) scope
 WHERE ($4::bigint=0 OR scope.asset_id<$4) AND (cardinality($5::bigint[])=0 OR scope.asset_id=ANY($5::bigint[])) AND ($6::text='' OR scope.asset_type=$6) ORDER BY scope.asset_id DESC LIMIT 250`

const globalAssetSourceSQL = ` FROM permitted p JOIN public.asset_entity e ON e.tenant_id=p.tenant_id AND (e.is_manual OR
 EXISTS(SELECT 1 FROM public.asset_observation o LEFT JOIN public.target t ON t.id=o.target_id WHERE o.tenant_id=e.tenant_id AND o.asset_id=e.id AND (o.original_target_id IS NULL OR t.id IS NOT NULL AND t.deleted_at IS NULL)) OR
 EXISTS(SELECT 1 FROM public.asset_inventory_visible_source v WHERE v.tenant_id=e.tenant_id AND v.source_asset_id=e.id) OR
 EXISTS(SELECT 1 FROM public.asset_application_visible_website x WHERE x.tenant_id=e.tenant_id AND x.application_id=e.id) OR
 EXISTS(SELECT 1 FROM public.asset_root_domain_visible_source r WHERE r.tenant_id=e.tenant_id AND r.root_asset_id=e.id) OR
 EXISTS(SELECT 1 FROM public.asset_root_domain_target r JOIN public.target t ON t.id=r.target_id AND t.deleted_at IS NULL WHERE r.tenant_id=e.tenant_id AND r.root_asset_id=e.id))`

const globalAssetCandidateSQL = ` SELECT e.id ` + globalAssetSourceSQL + ` WHERE ($4::bigint=0 OR e.id<$4) AND (cardinality($5::bigint[])=0 OR e.id=ANY($5::bigint[])) AND ($6::text='' OR e.asset_type=$6) ORDER BY e.id DESC LIMIT 250`

const boundedAssetSourceSQL = ` FROM permitted p JOIN public.asset_entity e ON e.tenant_id=p.tenant_id AND e.id=ANY($4::bigint[])`

const assetProjectionTailSQL = `
 LEFT JOIN LATERAL (SELECT o.payload FROM public.asset_observation o LEFT JOIN public.target t ON t.id=o.target_id WHERE o.tenant_id=e.tenant_id AND o.asset_id=e.id AND (p.target_id IS NULL OR o.target_id=p.target_id) AND (o.original_target_id IS NULL OR t.id IS NOT NULL AND t.deleted_at IS NULL) ORDER BY o.id DESC LIMIT 1) obs ON TRUE
 LEFT JOIN LATERAL (SELECT w.title,w.status_code,w.content_length,w.tech FROM public.asset_website_binding b JOIN public.website w ON w.id=b.website_id JOIN public.target t ON t.id=w.target_id AND t.deleted_at IS NULL WHERE b.asset_id=e.id AND (p.target_id IS NULL OR w.target_id=p.target_id) ORDER BY w.id DESC LIMIT 1) web ON TRUE
 LEFT JOIN LATERAL (SELECT array_agg(DISTINCT v.port ORDER BY v.port) AS ports FROM (
  SELECT h.port FROM public.asset_host_port_binding b JOIN public.host_port_mapping h ON h.id=b.host_port_id JOIN public.target t ON t.id=h.target_id AND t.deleted_at IS NULL WHERE b.asset_id=e.id AND (p.target_id IS NULL OR h.target_id=p.target_id)
  UNION SELECT CASE WHEN COALESCE(item.value->>'port','') ~ '^[0-9]{1,5}$' THEN (item.value->>'port')::integer END FROM public.asset_observation o LEFT JOIN public.target t ON t.id=o.target_id CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(o.payload->'openPorts')='array' THEN o.payload->'openPorts' ELSE '[]'::jsonb END) item(value) WHERE o.tenant_id=e.tenant_id AND o.asset_id=e.id AND (p.target_id IS NULL OR o.target_id=p.target_id) AND (o.original_target_id IS NULL OR t.id IS NOT NULL AND t.deleted_at IS NULL)
  UNION SELECT CASE WHEN COALESCE(obs.payload->>'port','') ~ '^[0-9]{1,5}$' THEN (obs.payload->>'port')::integer ELSE substring(public.tq_inventory_host_port(e.label) FROM ':([0-9]+)$')::integer END
 ) v WHERE v.port BETWEEN 1 AND 65535) portset ON TRUE
)`

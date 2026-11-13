package infrastructure

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
)

// TaskReader preserves the workbench's owner checks when Auto inspects tasks.
type TaskReader interface {
	List(context.Context, int, int64) ([]aiapp.Task, error)
	Graph(context.Context, int, int64) (*aiapp.TaskGraph, error)
	NodeDetail(context.Context, int, int64, int64) (*aiapp.TaskGraphNode, error)
	Trace(context.Context, int, int64, int64, []int64) ([]aiapp.TaskActivity, error)
}

func parseAutoTaskID(raw json.RawMessage) (int64, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		value = string(raw)
	}
	id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || id <= 0 {
		return 0, aiapp.ErrInvalidInput
	}
	return id, nil
}

func AutoTaskReadTools(reader TaskReader, user int) []tool.CoreTool {
	if reader == nil || user <= 0 {
		return nil
	}
	makeTool := func(name, description string, schema map[string]any, run func(context.Context, json.RawMessage) (any, error)) tool.CoreTool {
		return tool.Build(tool.Spec{
			Name: name, Description: description, Schema: schema,
			ReadOnly: func(json.RawMessage) bool { return true }, Concurrent: func(json.RawMessage) bool { return true },
			Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
				return permission.Allowed()
			},
			Run: func(ctx context.Context, raw json.RawMessage, _ *tool.ToolContext) (tool.Result, error) {
				result, err := run(ctx, raw)
				if err != nil {
					return tool.Errorf("任务查询失败，请检查任务编号和访问权限"), nil
				}
				output, err := json.Marshal(result)
				if err != nil {
					return tool.Errorf("任务查询结果编码失败"), nil
				}
				return tool.Text(string(output)), nil
			},
		})
	}
	idSchema := map[string]any{"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "string", "description": "当前账号拥有的任务 ID"}}, "required": []string{"task_id"}}
	taskID := func(raw json.RawMessage) (int64, error) {
		var request struct {
			TaskID json.RawMessage `json:"task_id"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			return 0, aiapp.ErrInvalidInput
		}
		return parseAutoTaskID(request.TaskID)
	}
	return []tool.CoreTool{
		makeTool("list_tasks", "列出当前账号的 AI 任务及运行状态，支持按 ID 向前翻页。", map[string]any{"type": "object", "properties": map[string]any{"before": map[string]any{"type": "integer", "minimum": 0}}}, func(ctx context.Context, raw json.RawMessage) (any, error) {
			var request struct {
				Before int64 `json:"before"`
			}
			if err := json.Unmarshal(raw, &request); err != nil || request.Before < 0 {
				return nil, aiapp.ErrInvalidInput
			}
			items, err := reader.List(ctx, user, request.Before)
			if err != nil {
				return nil, err
			}
			more := len(items) > 50
			if more {
				items = items[:50]
			}
			out := make([]map[string]any, 0, len(items))
			for _, item := range items {
				goal := []rune(item.Goal)
				if len(goal) > 800 {
					goal = goal[:800]
				}
				out = append(out, map[string]any{"id": item.ID, "name": item.Name, "goal": string(goal), "status": item.Status, "queued": item.Queued})
			}
			return map[string]any{"tasks": out, "has_more": more}, nil
		}),
		makeTool("get_task_graph", "读取指定任务的目标、意图、事实、发现及其关系；只返回当前账号可访问的任务。", idSchema, func(ctx context.Context, raw json.RawMessage) (any, error) {
			id, err := taskID(raw)
			if err != nil {
				return nil, err
			}
			graph, err := reader.Graph(ctx, user, id)
			if err != nil {
				return nil, err
			}
			if graph == nil {
				return nil, aiapp.ErrTaskNotFound
			}
			return boundedTaskGraph(graph, false), nil
		}),
		makeTool("list_task_findings", "列出指定任务中已登记的发现，内容来自任务图。", idSchema, func(ctx context.Context, raw json.RawMessage) (any, error) {
			id, err := taskID(raw)
			if err != nil {
				return nil, err
			}
			graph, err := reader.Graph(ctx, user, id)
			if err != nil {
				return nil, err
			}
			if graph == nil {
				return nil, aiapp.ErrTaskNotFound
			}
			return boundedTaskGraph(graph, true), nil
		}),
		makeTool("get_task_node_detail", "读取指定任务内某个目标、意图、事实或发现的完整节点内容。", map[string]any{"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "string"}, "id": map[string]any{"type": "integer", "minimum": 1}}, "required": []string{"task_id", "id"}}, func(ctx context.Context, raw json.RawMessage) (any, error) {
			var request struct {
				TaskID json.RawMessage `json:"task_id"`
				ID     int64           `json:"id"`
			}
			if err := json.Unmarshal(raw, &request); err != nil || request.ID <= 0 {
				return nil, aiapp.ErrInvalidInput
			}
			id, err := parseAutoTaskID(request.TaskID)
			if err != nil {
				return nil, err
			}
			node, err := reader.NodeDetail(ctx, user, id, request.ID)
			if err != nil {
				return nil, err
			}
			if node == nil {
				return nil, aiapp.ErrTaskNotFound
			}
			if len(node.Payload) > 256*1024 {
				return map[string]any{"id": node.ID, "kind": node.Kind, "state": node.State, "payload_truncated": true}, nil
			}
			out := map[string]any{"id": node.ID, "kind": node.Kind, "state": node.State, "payload": node.Payload}
			if node.Inherited {
				out["inherited"], out["source_task_id"] = true, node.SourceTaskID
			}
			if node.FindingID > 0 {
				out["finding_id"], out["finding_node_id"], out["traffic_count"] = node.FindingID, node.ID, node.TrafficCount
			}
			return out, nil
		}),
		makeTool("list_task_worker_traces", "列出指定任务已执行意图的编号、摘要和状态，以便选择执行过程查看。", map[string]any{"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "string"}, "q": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer"}}, "required": []string{"task_id"}}, func(ctx context.Context, raw json.RawMessage) (any, error) {
			var request struct {
				TaskID json.RawMessage `json:"task_id"`
				Q      string          `json:"q"`
				Limit  int             `json:"limit"`
			}
			if json.Unmarshal(raw, &request) != nil {
				return nil, aiapp.ErrInvalidInput
			}
			task, err := parseAutoTaskID(request.TaskID)
			if err != nil {
				return nil, err
			}
			if request.Limit <= 0 {
				request.Limit = 50
			}
			if request.Limit > 200 {
				request.Limit = 200
			}
			graph, err := reader.Graph(ctx, user, task)
			if err != nil {
				return nil, err
			}
			if graph == nil {
				return nil, aiapp.ErrTaskNotFound
			}
			works := make([]map[string]any, 0)
			q := strings.ToLower(strings.TrimSpace(request.Q))
			truncated := graph.Truncated
			for _, n := range graph.Nodes {
				if n.Kind != "intent" {
					continue
				}
				switch n.State {
				case "running", "done", "exhausted", "blocked", "stopped":
				default:
					continue
				}
				var payload struct {
					Summary string `json:"summary"`
				}
				_ = json.Unmarshal(n.Payload, &payload)
				if q != "" && !strings.Contains(strings.ToLower(payload.Summary), q) {
					continue
				}
				if len(works) == request.Limit {
					truncated = true
					break
				}
				works = append(works, map[string]any{"intent_id": n.ID, "summary": payload.Summary, "state": n.State})
			}
			return map[string]any{"works": works, "truncated": truncated}, nil
		}),
		makeTool("search_task_worker_traces", "按关键词搜索指定任务的执行步骤，返回意图编号及步骤摘要。", map[string]any{"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "string"}, "q": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer"}}, "required": []string{"task_id", "q"}}, func(ctx context.Context, raw json.RawMessage) (any, error) {
			var request struct {
				TaskID json.RawMessage `json:"task_id"`
				Q      string          `json:"q"`
				Limit  int             `json:"limit"`
			}
			if json.Unmarshal(raw, &request) != nil {
				return nil, aiapp.ErrInvalidInput
			}
			task, err := parseAutoTaskID(request.TaskID)
			if err != nil {
				return nil, err
			}
			if request.Limit <= 0 {
				request.Limit = 100
			}
			if request.Limit > 200 {
				request.Limit = 200
			}
			searcher, ok := reader.(interface {
				SearchTrace(context.Context, int, int64, string, int) ([]aiapp.TaskActivity, error)
			})
			if !ok {
				return nil, aiapp.ErrTaskNotFound
			}
			items, err := searcher.SearchTrace(ctx, user, task, request.Q, request.Limit)
			if err != nil {
				return nil, err
			}
			hits := make([]map[string]any, 0, len(items))
			for _, item := range items {
				var intent int64
				if item.NodeID != nil {
					intent = *item.NodeID
				}
				summary := []rune(strings.SplitN(item.Summary, "\n", 2)[0])
				if len(summary) > 100 {
					summary = summary[:100]
				}
				hits = append(hits, map[string]any{"intent_id": intent, "step_id": item.ID, "worker": item.Worker, "kind": item.Kind, "tool": item.Tool, "is_error": item.IsError, "summary": string(summary)})
			}
			return map[string]any{"query": request.Q, "hits": hits}, nil
		}),
		makeTool("get_task_worker_trace", "读取任务中一个意图的执行步骤摘要；可指定最多五个 step_ids 读取详情。", map[string]any{"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "string"}, "intent_id": map[string]any{"type": "integer", "minimum": 1}, "step_ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer", "minimum": 1}}}, "required": []string{"task_id", "intent_id"}}, func(ctx context.Context, raw json.RawMessage) (any, error) {
			var request struct {
				TaskID   json.RawMessage `json:"task_id"`
				IntentID int64           `json:"intent_id"`
				StepIDs  []int64         `json:"step_ids"`
			}
			if err := json.Unmarshal(raw, &request); err != nil || request.IntentID <= 0 || len(request.StepIDs) > 1000 {
				return nil, aiapp.ErrInvalidInput
			}
			task, err := parseAutoTaskID(request.TaskID)
			if err != nil {
				return nil, err
			}
			ids, omitted, seen := []int64{}, []int64{}, map[int64]bool{}
			for _, id := range request.StepIDs {
				if id <= 0 || seen[id] {
					continue
				}
				seen[id] = true
				if len(ids) < 5 {
					ids = append(ids, id)
				} else {
					omitted = append(omitted, id)
				}
			}
			if len(request.StepIDs) > 0 && len(ids) == 0 {
				return nil, aiapp.ErrInvalidInput
			}
			items, err := reader.Trace(ctx, user, task, request.IntentID, ids)
			if err != nil {
				return nil, err
			}
			truncated := len(items) > 200
			if truncated {
				items = items[:200]
			}
			steps := make([]map[string]any, 0, len(items))
			for _, item := range items {
				summary := []rune(item.Summary)
				if len(summary) > 100 {
					summary = summary[:100]
				}
				step := map[string]any{"step_id": item.ID, "kind": item.Kind, "tool": item.Tool, "is_error": item.IsError, "summary": string(summary)}
				if len(ids) > 0 {
					detail := []rune(item.Detail)
					if len(detail) > 65536 {
						detail = detail[:65536]
						step["detail_truncated"] = true
					}
					step["detail"] = string(detail)
				}
				steps = append(steps, step)
			}
			return map[string]any{"intent_id": request.IntentID, "steps": steps, "omitted_step_ids": omitted, "truncated": truncated}, nil
		}),
	}
}

func boundedTaskGraph(graph *aiapp.TaskGraph, findingsOnly bool) map[string]any {
	nodes := make([]map[string]any, 0)
	selected := map[int64]bool{}
	truncated := graph.Truncated
	for _, node := range graph.Nodes {
		if findingsOnly && node.Kind != "finding" {
			continue
		}
		if len(nodes) == 200 {
			truncated = true
			break
		}
		var payload any = node.Payload
		if len(node.Payload) > 32*1024 {
			payload = map[string]any{"truncated": true}
			truncated = true
		}
		item := map[string]any{"id": node.ID, "kind": node.Kind, "state": node.State, "payload": payload}
		if node.Inherited {
			item["inherited"], item["source_task_id"] = true, node.SourceTaskID
		}
		if node.FindingID > 0 {
			item["finding_id"], item["finding_node_id"], item["traffic_count"] = node.FindingID, node.ID, node.TrafficCount
		}
		nodes = append(nodes, item)
		selected[node.ID] = true
	}
	edges := make([]map[string]any, 0)
	if !findingsOnly {
		for _, edge := range graph.Edges {
			if selected[edge.SourceID] && selected[edge.TargetID] {
				if len(edges) == 400 {
					truncated = true
					break
				}
				edges = append(edges, map[string]any{"source_id": edge.SourceID, "target_id": edge.TargetID, "relation": edge.Relation})
			}
		}
	}
	return map[string]any{"nodes": nodes, "edges": edges, "truncated": truncated}
}

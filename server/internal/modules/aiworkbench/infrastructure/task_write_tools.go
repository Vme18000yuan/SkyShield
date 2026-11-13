package infrastructure

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
)

type TaskCreator interface {
	Create(context.Context, aiapp.CreateTaskInput) (*aiapp.Task, error)
	PauseFromAuto(context.Context, int, int64, aiapp.AutoTaskMutation) (*aiapp.Task, error)
}

// AutoTaskWriteTools ties each state change to the caller's live conversation
// lease and tool-use ID. The repository enforces that fence in the transaction.
func AutoTaskWriteTools(writer TaskCreator, user int) []tool.CoreTool {
	if writer == nil || user <= 0 {
		return nil
	}
	return []tool.CoreTool{tool.Build(tool.Spec{
		Name: "spawn_task", Description: "创建并启动一个独立 AI 任务；可选绑定平台目标、已有资产及只读来源任务。超时为 0 或不填时不限时。",
		Schema: map[string]any{"type": "object", "properties": map[string]any{
			"description":            map[string]any{"type": "string", "description": "任务标题"},
			"goal":                   map[string]any{"type": "string", "description": "要达成的目标"},
			"target_id":              map[string]any{"type": "integer", "minimum": 0},
			"asset_ids":              map[string]any{"type": "array", "items": map[string]any{"type": "integer", "minimum": 1}, "maxItems": 1000},
			"source_task_ids":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 8, "description": "只读继承的直接来源任务 ID，最多 8 个"},
			"llm_profile_id":         map[string]any{"type": "string", "description": "可选任务模型配置 ID"},
			"timeout_seconds":        map[string]any{"type": "integer", "minimum": 0, "maximum": 2147483647},
			"plan_heartbeat_seconds": map[string]any{"type": "integer", "minimum": 0, "maximum": 2147483647},
			"seed_first_intent":      map[string]any{"type": "boolean", "description": "目标拆解后优先执行一条种子意图"},
		}, "required": []string{"description", "goal"}},
		ReadOnly: func(json.RawMessage) bool { return false }, Concurrent: func(json.RawMessage) bool { return false },
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(ctx context.Context, raw json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
			if tc == nil || tc.ToolUseID == "" || airuntime.ExecutionID(ctx) == "" {
				return tool.Errorf("缺少有效对话运行上下文"), nil
			}
			var request struct {
				Description          string   `json:"description"`
				Goal                 string   `json:"goal"`
				TargetID             int      `json:"target_id"`
				AssetIDs             []int64  `json:"asset_ids"`
				SourceTaskIDs        []string `json:"source_task_ids"`
				LLMProfileID         string   `json:"llm_profile_id"`
				TimeoutSeconds       int      `json:"timeout_seconds"`
				PlanHeartbeatSeconds int      `json:"plan_heartbeat_seconds"`
				SeedFirstIntent      bool     `json:"seed_first_intent"`
			}
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			var trailing any
			if err := decoder.Decode(&request); err != nil {
				return tool.Errorf("任务参数无效或包含不支持的字段"), nil
			}
			if err := decoder.Decode(&trailing); err != io.EOF {
				return tool.Errorf("任务参数无效"), nil
			}
			request.Description, request.Goal = strings.TrimSpace(request.Description), strings.TrimSpace(request.Goal)
			if request.Description == "" || request.Goal == "" {
				return tool.Errorf("任务标题和目标不能为空"), nil
			}
			var sourceIDs []int64
			for _, rawID := range request.SourceTaskIDs {
				encoded, _ := json.Marshal(strings.TrimSpace(rawID))
				id, err := parseAutoTaskID(encoded)
				if err != nil {
					return tool.Errorf("来源任务编号无效"), nil
				}
				sourceIDs = append(sourceIDs, id)
			}
			var profiles []string
			if strings.TrimSpace(request.LLMProfileID) != "" {
				profiles = []string{strings.TrimSpace(request.LLMProfileID)}
			}
			canonical, err := json.Marshal(request)
			if err != nil {
				return tool.Errorf("任务参数无效"), nil
			}
			digest := sha256.Sum256(canonical)
			item, err := writer.Create(ctx, aiapp.CreateTaskInput{
				UserID: user, Name: request.Description, Description: request.Description, Goal: request.Goal,
				TargetID: request.TargetID, AssetIDs: request.AssetIDs, SourceTaskIDs: sourceIDs, LLMProfileIDs: profiles, TimeoutSeconds: request.TimeoutSeconds, PlanHeartbeatSeconds: request.PlanHeartbeatSeconds, StartImmediately: true, SeedFirstIntent: request.SeedFirstIntent,
				ConversationRunID: airuntime.ExecutionID(ctx), ToolUseID: tc.ToolUseID, ToolInputHash: hex.EncodeToString(digest[:]),
			})
			if err != nil {
				return tool.Errorf("任务创建失败，请检查运行状态、模型配置和目标权限"), nil
			}
			return tool.Text(fmt.Sprintf("task created: %d", item.ID)), nil
		},
	}), tool.Build(tool.Spec{
		Name: "pause_task", Description: "暂停当前账号拥有的指定 AI 任务。",
		Schema:   map[string]any{"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "string", "description": "要暂停的任务 ID"}}, "required": []string{"task_id"}},
		ReadOnly: func(json.RawMessage) bool { return false }, Concurrent: func(json.RawMessage) bool { return false },
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(ctx context.Context, raw json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
			if tc == nil || tc.ToolUseID == "" || airuntime.ExecutionID(ctx) == "" {
				return tool.Errorf("缺少有效对话运行上下文"), nil
			}
			var request struct {
				TaskID json.RawMessage `json:"task_id"`
			}
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&request); err != nil {
				return tool.Errorf("任务编号无效"), nil
			}
			var trailing any
			if err := decoder.Decode(&trailing); err != io.EOF {
				return tool.Errorf("任务参数无效"), nil
			}
			id, err := parseAutoTaskID(request.TaskID)
			if err != nil {
				return tool.Errorf("任务编号无效"), nil
			}
			digest := sha256.Sum256([]byte(fmt.Sprintf("pause:%d", id)))
			item, err := writer.PauseFromAuto(ctx, user, id, aiapp.AutoTaskMutation{RunID: airuntime.ExecutionID(ctx), ToolUseID: tc.ToolUseID, InputHash: hex.EncodeToString(digest[:])})
			if err != nil {
				return tool.Errorf("任务暂停失败，请检查运行状态和任务权限"), nil
			}
			return tool.Text(fmt.Sprintf("pause request handled: %d; current status: %s", id, item.Status)), nil
		},
	})}
}

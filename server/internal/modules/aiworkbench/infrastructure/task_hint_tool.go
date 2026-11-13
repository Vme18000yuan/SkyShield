package infrastructure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/Autumn-27/artex/agent"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
)

type AutoTaskHintWriter interface {
	AddAutoTaskHints(context.Context, int, int64, aiapp.AutoTaskMutation, []aiapp.AutoTaskHint) (aiapp.AutoTaskHintResult, error)
}

func AutoTaskHintTools(writer AutoTaskHintWriter, user int, trafficEnabled func(context.Context, int) bool) []tool.CoreTool {
	if writer == nil || user <= 0 {
		return nil
	}
	type hintInput struct {
		Text        string               `json:"text"`
		AssetIDs    []json.RawMessage    `json:"asset_ids"`
		TrafficRefs []artexdb.TrafficRef `json:"traffic_refs"`
	}
	props := map[string]any{"text": map[string]any{"type": "string"}, "asset_ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}, "traffic_refs": agent.HintTrafficSchema()}
	return []tool.CoreTool{tool.Build(tool.Spec{
		Name: "add_task_hint", Description: "向当前账号的指定任务添加战略提示，规划者会在下一轮读取。可用 hints 批量提交，返回每条提示的编号及错误。",
		Schema:   map[string]any{"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "string"}, "text": props["text"], "asset_ids": props["asset_ids"], "traffic_refs": props["traffic_refs"], "hints": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": props}}}, "required": []string{"task_id"}},
		ReadOnly: func(json.RawMessage) bool { return false }, Concurrent: func(json.RawMessage) bool { return false }, Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(ctx context.Context, raw json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
			if tc == nil || tc.ToolUseID == "" || airuntime.ExecutionID(ctx) == "" {
				return tool.Errorf("缺少有效对话运行上下文"), nil
			}
			var input struct {
				TaskID json.RawMessage `json:"task_id"`
				Hints  []hintInput     `json:"hints"`
				hintInput
			}
			if len(raw) > 1<<20 || strictPlatformInput(raw, &input) != nil {
				return tool.Errorf("提示参数无效"), nil
			}
			taskID, err := parseAutoTaskID(input.TaskID)
			if err != nil {
				return tool.Errorf("任务编号无效"), nil
			}
			batch := len(input.Hints) > 0
			requested := input.Hints
			if !batch {
				requested = []hintInput{input.hintInput}
			}
			if len(requested) > 100 {
				return tool.Errorf("一次最多提交 100 条提示"), nil
			}
			items := make([]aiapp.AutoTaskHint, len(requested))
			for index, item := range requested {
				items[index] = aiapp.AutoTaskHint{Text: item.Text, TrafficRefs: item.TrafficRefs}
				if len(item.TrafficRefs) > 0 && (trafficEnabled == nil || !trafficEnabled(ctx, user)) {
					items[index].ValidationError = "traffic_disabled"
				}
				for _, rawID := range item.AssetIDs {
					id, e := parseAutoTaskID(rawID)
					if e != nil {
						items[index].ValidationError = "invalid"
						break
					}
					items[index].AssetIDs = append(items[index].AssetIDs, id)
				}
			}
			canonical, err := json.Marshal(input)
			if err != nil {
				return tool.Errorf("提示参数无效"), nil
			}
			digest := sha256.Sum256(canonical)
			result, err := writer.AddAutoTaskHints(ctx, user, taskID, aiapp.AutoTaskMutation{RunID: airuntime.ExecutionID(ctx), ToolUseID: tc.ToolUseID, InputHash: hex.EncodeToString(digest[:])}, items)
			if err != nil {
				return tool.Errorf("提示未保存，请检查任务权限、运行状态和输入"), nil
			}
			if !batch {
				if message := result.Errors["0"]; message != "" {
					return tool.Errorf(message), nil
				}
				return tool.Text(fmt.Sprintf("hint added: %d", result.IDs[0])), nil
			}
			out, _ := json.Marshal(result)
			return tool.Text(string(out)), nil
		},
	})}
}

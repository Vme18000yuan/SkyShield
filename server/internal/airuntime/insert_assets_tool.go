package airuntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/agent"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

var artexAssetFieldNames = map[string]string{
	"target_id": "targetId", "company_id": "organizationId", "organization_id": "organizationId",
	"record_type": "recordType", "record_value": "recordValue", "bound_domains": "boundDomains", "open_ports": "openPorts",
	"app_name": "appName", "bundle_id": "bundleId", "app_icp": "appIcp", "status_code": "statusCode", "content_length": "contentLength",
	"page_title": "pageTitle", "favicon_mmh3": "faviconMmh3", "service_name": "serviceName", "service_ip": "serviceIp",
}

func decodeARTEXAsset(raw json.RawMessage) (assetapp.AssetInput, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return assetapp.AssetInput{}, fmt.Errorf("资产输入必须为对象")
	}
	for old, current := range artexAssetFieldNames {
		if value, ok := fields[old]; ok {
			if _, duplicate := fields[current]; duplicate {
				return assetapp.AssetInput{}, fmt.Errorf("资产字段重复: %s", current)
			}
			fields[current] = value
			delete(fields, old)
		}
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return assetapp.AssetInput{}, err
	}
	var input assetapp.AssetInput
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, err
	}
	return input, nil
}

func insertAssetsTool(writer *assetapp.RegistrationFacade, userID int) tool.CoreTool {
	properties := map[string]any{
		"type":      map[string]any{"type": "string", "enum": []string{"root_domain", "subdomain", "ip", "app", "service", "endpoint"}},
		"target_id": map[string]any{"type": "integer", "description": "已授权的现有目标 ID；填写后校验范围并投影到原资产页面"},
	}
	for _, field := range []string{"domain", "ip", "icp", "record_type", "app_name", "bundle_id", "platform", "category", "description", "app_icp", "url", "page_title", "favicon_mmh3", "service_name", "service_ip", "proto", "method"} {
		properties[field] = map[string]any{"type": "string"}
	}
	for _, field := range []string{"port", "company_id", "status_code", "content_length"} {
		properties[field] = map[string]any{"type": "integer"}
	}
	for _, field := range []string{"record_value", "bound_domains", "technologies"} {
		properties[field] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	}
	for _, field := range []string{"open_ports", "auth", "params"} {
		properties[field] = map[string]any{"type": "array", "items": map[string]any{"type": "object"}}
	}
	return tool.Build(tool.Spec{
		Name:        "insert_assets",
		Description: "批量登记新发现的资产，保留 ARTEX 六类资产字段。各类型必填：root_domain/subdomain→domain；ip→ip；app→app_name；HTTP service→url；其他 service→service_name+port+ip/domain；endpoint→url。target_id 为已授权现有目标，提供后回写原资产页面；不提供时保留为独立资产。返回逐项 results/errors；登记资产不表示已完成测试。",
		Schema:      map[string]any{"type": "object", "properties": map[string]any{"assets": map[string]any{"type": "array", "minItems": 1, "maxItems": 100, "items": map[string]any{"type": "object", "properties": properties, "required": []string{"type"}}}}, "required": []string{"assets"}},
		ReadOnly:    func(json.RawMessage) bool { return false }, Concurrent: func(json.RawMessage) bool { return true },
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(ctx context.Context, raw json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
			run := executionID(ctx)
			if !canonicalUUID(run) || tc == nil || tc.ToolUseID == "" {
				return tool.Errorf("资产工具缺少可信运行上下文"), nil
			}
			var request struct {
				Assets []json.RawMessage `json:"assets"`
			}
			if err := json.Unmarshal(raw, &request); err != nil || len(request.Assets) == 0 || len(request.Assets) > 100 {
				return tool.Errorf("资产输入无效"), nil
			}
			var items []assetapp.AssetInput
			decodeErrors := map[int]string{}
			for index, item := range request.Assets {
				decoded, err := decodeARTEXAsset(item)
				if err != nil {
					decodeErrors[index] = "资产字段无效: " + err.Error()
					decoded = assetapp.AssetInput{}
				}
				if target := taskTarget(ctx); target > 0 && err == nil {
					if decoded.TargetID != 0 && decoded.TargetID != target {
						decodeErrors[index] = "资产不属于本任务关联目标"
						decoded = assetapp.AssetInput{}
					}
					if decoded.Type != "" {
						decoded.TargetID = target
					}
				}
				items = append(items, decoded)
			}
			var task *assetapp.RegistrationTaskScope
			if info := agent.RunInfoFrom(ctx); info.TaskID > 0 {
				task = &assetapp.RegistrationTaskScope{TaskID: info.TaskID, ExplorationID: info.ExplorationID, IntentID: info.IntentID, ExecutionID: run}
			}
			result, err := writer.Register(ctx, assetapp.BatchRegisterInput{UserID: userID, Origin: "ai", RequestID: run + "/" + tc.ToolUseID, Assets: items, Task: task})
			if err != nil {
				return tool.Errorf("资产登记失败"), nil
			}
			for index := range result.Errors {
				if message, ok := decodeErrors[result.Errors[index].Index]; ok {
					result.Errors[index].Error = message
				}
			}
			if task != nil && agent.HostIntelligenceEnabled(ctx) {
				worker := strings.TrimSpace(tc.AgentID)
				if worker == "" || len(worker) > 200 {
					if task.IntentID > 0 {
						worker = "worker"
					} else {
						worker = "mainagent"
					}
				}
				for _, registered := range result.Results {
					if registered.Index < 0 || registered.Index >= len(items) || items[registered.Index].Type != "service" {
						continue
					}
					assetID, parseErr := strconv.ParseInt(registered.ID, 10, 64)
					if parseErr != nil || assetID <= 0 {
						continue
					}
					for _, auth := range items[registered.Index].Auth {
						content, marshalErr := json.Marshal(auth)
						if marshalErr != nil {
							result.Errors = append(result.Errors, assetapp.AssetRegistrationError{Index: registered.Index, Error: "资产已保存，凭据情报未保存"})
							break
						}
						input := artexdb.IntelligenceInput{Kind: "credential", Title: fmt.Sprintf("服务 #%d 的认证信息", assetID), Content: string(content), Evidence: fmt.Sprintf("insert_assets 上报，服务资产 #%d，来源意图 #%d", assetID, task.IntentID), Confidence: "observed", Status: "unverified", AssetIDs: []int64{assetID}}
						if _, recordErr := agent.RecordHostIntelligence(ctx, task.TaskID, task.IntentID, worker, input); recordErr != nil {
							result.Errors = append(result.Errors, assetapp.AssetRegistrationError{Index: registered.Index, Error: "资产已保存，凭据情报未保存"})
							break
						}
					}
				}
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				return tool.Errorf("资产结果编码失败"), nil
			}
			return tool.Text(string(encoded)), nil
		},
	})
}

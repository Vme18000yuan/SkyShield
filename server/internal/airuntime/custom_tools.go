// Custom tool execution follows ARTEX's command/script/http contracts.
package airuntime

import (
	"context"
	"encoding/json"
	"fmt"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/httpoutbound"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func CustomToolTester(root string) func(context.Context, int, artexdb.Tool, map[string]any) (tool.Result, error) {
	return CustomToolTesterWithPython(root, nil)
}

func CustomToolTesterWithPython(root string, python func(context.Context, int) (string, error)) func(context.Context, int, artexdb.Tool, map[string]any) (tool.Result, error) {
	return func(ctx context.Context, user int, row artexdb.Tool, params map[string]any) (tool.Result, error) {
		workspace := filepath.Join(root, assets.DefaultTenantID, fmt.Sprintf("user-%d", user), "work")
		ctx = withWorkspace(ctx, workspace, filepath.Join(workspace, "tool-tests", uuid.NewString()))
		if err := workspaceScopeError(ctx); err != nil {
			return tool.Result{}, err
		}
		workspace, _ = scopedWorkspace(ctx, workspace)
		if err := os.MkdirAll(workspace, 0700); err != nil {
			return tool.Result{}, err
		}
		var interpreter func(context.Context) (string, error)
		if python != nil {
			interpreter = func(ctx context.Context) (string, error) { return python(ctx, user) }
		}
		item := BuildCustomToolWithWorkspace(row, nil, interpreter, workspace)
		input, err := json.Marshal(params)
		if err != nil {
			return tool.Result{}, err
		}
		return item.Call(ctx, input, &tool.ToolContext{WorkingDir: workspace, OutputDir: filepath.Join(workspace, "cmd-output"), ToolUseID: uuid.NewString()})
	}
}

func BuildCustomTool(row artexdb.Tool, base []tool.CoreTool) tool.CoreTool {
	return BuildCustomToolWithPython(row, base, nil)
}

func BuildCustomToolWithPython(row artexdb.Tool, base []tool.CoreTool, python func(context.Context) (string, error)) tool.CoreTool {
	return buildCustomTool(row, base, python, "")
}

// BuildCustomToolWithWorkspace creates a confined process per shell invocation.
// Its interpreter-specific grants cannot leak to the Agent's other tools.
func BuildCustomToolWithWorkspace(row artexdb.Tool, base []tool.CoreTool, python func(context.Context) (string, error), workspace string) tool.CoreTool {
	return buildCustomTool(row, base, python, workspace)
}
func buildCustomTool(row artexdb.Tool, base []tool.CoreTool, python func(context.Context) (string, error), workspace string) tool.CoreTool {
	var schema map[string]any
	_ = json.Unmarshal(row.Schema, &schema)
	properties, _ := schema["properties"].(map[string]any)
	if len(properties) == 0 {
		schema = map[string]any{"type": "object", "properties": map[string]any{"args": map[string]any{"type": "string", "description": "命令/参数（自由文本）"}}}
	}
	var bash tool.CoreTool
	for _, item := range base {
		if item.Name() == "Bash" {
			bash = item
			break
		}
	}
	return tool.Build(tool.Spec{Name: row.Key, Description: row.Description, Schema: schema, Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
		return permission.Allowed()
	}, Run: func(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
		var parameters map[string]any
		if json.Unmarshal(input, &parameters) != nil {
			return tool.Errorf("自定义工具参数无效"), nil
		}
		var spec struct {
			Command   string            `json:"command"`
			Code      string            `json:"code"`
			Method    string            `json:"method"`
			URL       string            `json:"url"`
			Headers   map[string]string `json:"headers"`
			Body      string            `json:"body"`
			TimeoutMS int               `json:"timeout_ms"`
			Proxy     string            `json:"proxy"`
		}
		if json.Unmarshal(row.Exec, &spec) != nil {
			return tool.Errorf("执行规格无效"), nil
		}
		timeout := 120000
		if row.Kind == "http" {
			timeout = 30000
		}
		if spec.TimeoutMS > 0 {
			timeout = min(spec.TimeoutMS, 600000)
		}
		ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
		defer cancel()
		switch row.Kind {
		case "command", "script":
			if bash == nil && workspace == "" {
				return tool.Errorf("当前 Agent 的本地执行环境不可用"), nil
			}
			command := renderCustomTemplate(spec.Command, parameters, shellQuoteValue)
			interpreter := ""
			if row.Kind == "script" {
				interpreter = "python3"
				if python != nil {
					value, err := python(ctx)
					if err != nil {
						return tool.Errorf("Python 配置不可用"), nil
					}
					if strings.TrimSpace(value) != "" {
						interpreter = value
					}
				}
				encoded, _ := json.Marshal(parameters)
				env := []string{}
				for key, value := range parameters {
					if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(key) {
						continue
					}
					switch value.(type) {
					case string, float64, bool:
						env = append(env, "TOOL_"+strings.ToUpper(key)+"="+shellQuoteValue(customValue(value)))
					}
				}
				command = strings.Join(env, " ") + " " + shellQuoteValue(interpreter) + " -c " + shellQuoteValue(spec.Code) + " <<'TIANQIONG_TOOL_INPUT'\n" + string(encoded) + "\nTIANQIONG_TOOL_INPUT"
			}
			raw, _ := json.Marshal(map[string]any{"command": command, "timeout_ms": timeout})
			if workspace != "" {
				native, err := newNativeLocalToolsWithInterpreter(withAuxiliaryProcess(ctx), workspace, interpreter)
				if err != nil {
					return tool.Errorf("本地工具执行环境不可用"), nil
				}
				defer native.Close()
				for _, item := range native.Tools(false) {
					if item.Name() == "Bash" {
						return item.Call(ctx, raw, tc)
					}
				}
				return tool.Errorf("本地执行工具不可用"), nil
			}
			return bash.Call(ctx, raw, tc)
		case "http":
			method := strings.ToUpper(strings.TrimSpace(spec.Method))
			if method == "" {
				method = "GET"
			}
			request, err := http.NewRequestWithContext(ctx, method, renderCustomTemplate(spec.URL, parameters, func(value string) string { return value }), strings.NewReader(renderCustomTemplate(spec.Body, parameters, func(value string) string { return value })))
			if err != nil || request.URL.Scheme != "http" && request.URL.Scheme != "https" {
				return tool.Errorf("HTTP URL 无效"), nil
			}
			for key, value := range spec.Headers {
				request.Header.Set(key, renderCustomTemplate(value, parameters, func(value string) string { return value }))
			}
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.Proxy = httpoutbound.RecordingProxy(ctx, httpoutbound.DefaultProxyController.WithProfileProxy(spec.Proxy))
			if err = httpoutbound.ApplyRecordingTrust(ctx, transport); err != nil {
				return tool.Errorf("HTTP 录制证书不可用"), nil
			}
			defer transport.CloseIdleConnections()
			response, err := (&http.Client{Transport: transport, Timeout: time.Duration(timeout) * time.Millisecond}).Do(request)
			if err != nil {
				return tool.Errorf("HTTP 请求失败"), nil
			}
			defer response.Body.Close()
			body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			if err != nil {
				return tool.Errorf("读取 HTTP 响应失败"), nil
			}
			out, _ := json.Marshal(map[string]any{"status": response.StatusCode, "body": string(body)})
			return tool.Text(tool.Capture(tc, string(out))), nil
		default:
			return tool.Errorf("未知工具类型"), nil
		}
	}})
}
func shellQuoteValue(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

var customPlaceholder = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func renderCustomTemplate(template string, parameters map[string]any, quote func(string) string) string {
	return customPlaceholder.ReplaceAllStringFunc(template, func(match string) string {
		value, ok := parameters[match[1:len(match)-1]]
		if !ok {
			return match
		}
		return quote(customValue(value))
	})
}
func customValue(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(value)
	case nil:
		return ""
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Sprint(value)
		}
		return string(encoded)
	}
}

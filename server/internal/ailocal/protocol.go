// Package ailocal runs Norma's local tools in a separate, confined process.
package ailocal

import (
	"encoding/json"
	"github.com/Autumn-27/norma/tool"
	"time"
)

const workerToolTimeout = 10 * time.Minute

type Request struct {
	ID             string          `json:"id"`
	Cancel         string          `json:"cancel,omitempty"`
	Name           string          `json:"name"`
	Input          json.RawMessage `json:"input"`
	WorkingDir     string          `json:"working_dir"`
	OutputDir      string          `json:"output_dir"`
	MaxOutputChars int             `json:"max_output_chars"`
	ToolUseID      string          `json:"tool_use_id"`
	AgentID        string          `json:"agent_id"`
	Env            []string        `json:"env,omitempty"`
}
type Response struct {
	Progress      string              `json:"progress,omitempty"`
	ID            string              `json:"id"`
	Ready         bool                `json:"ready,omitempty"`
	Result        tool.Result         `json:"result"`
	Error         string              `json:"error,omitempty"`
	Notifications []tool.Notification `json:"notifications,omitempty"`
}

func NativeTools(interactive bool) []tool.CoreTool {
	tools := append(tool.DefaultTools(), tool.NewTaskOutput(), tool.NewTaskStop(), tool.NewTaskList(), tool.NewMonitor())
	if interactive {
		tools = append(tools, tool.ShellSessionTools()...)
	}
	return tools
}

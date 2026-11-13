package infrastructure

import (
	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/norma/tool"
)

// These definitions seed the catalog metadata for task-only tools. Independent
// chats filter them out; task runners retain their own graph-backed versions.
// Catalog resolution must never replace a live graph handler with this stub.
func TaskContextFallbackTools() []tool.CoreTool {
	out := []tool.CoreTool{}
	for _, item := range agent.NewToolSet(nil, "").AllDomainTools() {
		if item.Name() == "report_finding" || item.Name() == "list_findings" {
			out = append(out, item)
		}
	}
	return out
}

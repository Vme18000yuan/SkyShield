package airuntime

import (
	"context"
	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/norma/tool"
)

// ApprovalToolWrapper decorates a fully assembled tool using the trusted run
// identity and the owning task/conversation context.
type ApprovalToolWrapper func(context.Context, int, string, agent.RunInfo, func(Activity), tool.CoreTool) tool.CoreTool

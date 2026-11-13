package airuntime

import "github.com/Autumn-27/norma/tool"

type deferredTool struct{ tool.CoreTool }

func (t *deferredTool) IsDeferredTool() bool            { return true }
func WithDeferredTool(item tool.CoreTool) tool.CoreTool { return &deferredTool{CoreTool: item} }

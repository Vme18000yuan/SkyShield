package airuntime

import (
	"context"
	"encoding/json"
	"github.com/Autumn-27/norma/tool"
	"github.com/yyhuni/lunafox/server/internal/httpoutbound"
)

type SearchConfigResolver func(context.Context) (tool.WebSearchConfig, bool, error)
type dynamicWebSearch struct {
	tool.CoreTool
	resolve SearchConfigResolver
}

func NewDynamicWebSearch(resolve SearchConfigResolver) (tool.CoreTool, error) {
	prototype, err := tool.NewWebSearch(tool.WebSearchConfig{Backend: "ddgs"})
	if err != nil {
		return nil, err
	}
	return &dynamicWebSearch{CoreTool: prototype, resolve: resolve}, nil
}
func (t *dynamicWebSearch) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
	config, enabled, err := t.resolve(ctx)
	if err != nil {
		return tool.Errorf("网络搜索配置不可用"), nil
	}
	if !enabled {
		return tool.Errorf("网络搜索已在系统设置中关闭"), nil
	}
	relay, err := httpoutbound.NewLocalRelay(ctx, config.Proxy)
	if err != nil {
		return tool.Errorf("无法启动网络搜索代理"), nil
	}
	defer relay.Close()
	config.Proxy = relay.URL()
	config.CACert = relay.CABundle()
	search, err := tool.NewWebSearch(config)
	if err != nil {
		return tool.Errorf("网络搜索配置无效，请检查后端及凭据"), nil
	}
	return search.Call(ctx, input, tc)
}

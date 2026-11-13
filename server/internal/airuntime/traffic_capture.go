package airuntime

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Autumn-27/norma/tool"
	"github.com/yyhuni/lunafox/server/internal/httpoutbound"
)

type TrafficLease interface {
	ProxyURL() string
	CABundle() string
	Tools() []tool.CoreTool
	Close()
}
type TrafficAccess struct {
	Lease   TrafficLease
	Enabled func(context.Context) (bool, error)
}
type TaskTrafficResolver func(context.Context, TaskExecution) (*TrafficAccess, error)
type ConversationTrafficResolver func(context.Context, ChatRun) (*TrafficAccess, error)

func withTrafficCapture(ctx context.Context, access *TrafficAccess) (context.Context, error) {
	if access == nil {
		return ctx, nil
	}
	if access.Lease == nil || access.Enabled == nil {
		return ctx, errors.New("invalid traffic capture")
	}
	return httpoutbound.WithRecordingRoute(ctx, access.Lease.ProxyURL(), access.Lease.CABundle(), access.Enabled)
}

type scopedTrafficTool struct {
	tool.CoreTool
	enabled func(context.Context) (bool, error)
}

func (s scopedTrafficTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	allowed, err := s.enabled(ctx)
	if err != nil || !allowed {
		return tool.Errorf("流量录制已停用或运行权限已失效"), nil
	}
	result, err := s.CoreTool.Call(ctx, input, tc)
	if err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	allowed, err = s.enabled(ctx)
	if err != nil || !allowed {
		return tool.Errorf("流量录制已停用或运行权限已失效"), nil
	}
	return result, nil
}
func scopedTrafficTools(access *TrafficAccess) []tool.CoreTool {
	if access == nil || access.Lease == nil || access.Enabled == nil {
		return nil
	}
	originals := access.Lease.Tools()
	items := make([]tool.CoreTool, 0, len(originals))
	for _, item := range originals {
		items = append(items, scopedTrafficTool{CoreTool: item, enabled: access.Enabled})
	}
	return items
}

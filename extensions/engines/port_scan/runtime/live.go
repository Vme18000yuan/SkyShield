package portscanruntime

import (
	"context"
	"github.com/yyhuni/lunafox/engine-go/stream"
	contract "github.com/yyhuni/lunafox/engines/port_scan/contract"
	parser "github.com/yyhuni/lunafox/engines/port_scan/internal/results"
)

func (r *Runtime) executeLive(ctx context.Context, path string, run func(context.Context) error) error {
	if r.results == nil {
		return run(ctx)
	}
	return stream.Submit(ctx, r.results.Submit, func(liveCtx context.Context, emit func(contract.HostPort) error) error {
		return stream.File(liveCtx, path, 4*1024*1024, run, func(line string, oversized bool) error {
			if oversized {
				return nil
			}
			item, ok := parser.ParseLiveNaabuLine(line)
			if !ok {
				return nil
			}
			return emit(item)
		})
	})
}

package aiapprovalwiring

import (
	"context"
	"errors"

	approval "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/application"
	blacklist "github.com/yyhuni/lunafox/server/internal/modules/blacklist/application"
)

// NewPlatformBlacklistResolver reuses the scan policy's validated global and
// target-specific pattern union for AI asset decisions.
func NewPlatformBlacklistResolver(service blacklist.BlacklistPolicyApplicationService) (approval.PlatformBlacklistResolver, error) {
	if service == nil {
		return nil, errors.New("blacklist policy service is required")
	}
	return func(ctx context.Context, targetID int) ([]string, error) {
		if targetID > 0 {
			return service.ResolveEffectivePatternsForScan(ctx, targetID)
		}
		policy, err := service.GetGlobal(ctx)
		if err != nil {
			return nil, err
		}
		if policy == nil {
			return nil, errors.New("global blacklist policy unavailable")
		}
		return policy.Patterns, nil
	}, nil
}

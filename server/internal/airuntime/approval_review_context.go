package airuntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/tool"
	"github.com/yyhuni/lunafox/server/internal/ailocal"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

// CaptureApprovalReview reads potentially attacker-selected scripts only inside
// the same user-scoped local sandbox used for Agent tools.
func CaptureApprovalReview(ctx context.Context, root string, user int, name string, input json.RawMessage, tc *tool.ToolContext) (intercept.ReviewInput, error) {
	ctx, release, permitErr := AcquireAuxiliaryToolProcess(ctx)
	if permitErr != nil {
		return intercept.ReviewInput{}, permitErr
	}
	defer release()
	if err := workspaceScopeError(ctx); err != nil {
		return intercept.ReviewInput{}, err
	}
	if !filepath.IsAbs(root) || user <= 0 {
		return intercept.ReviewInput{}, errors.New("invalid review scope")
	}
	workspace := filepath.Join(root, assets.DefaultTenantID, fmt.Sprintf("user-%d", user), "work")
	workspace, readPaths := scopedWorkspace(ctx, workspace)
	client, err := ailocal.NewClientWithReadPaths(ctx, workspace, "", nil, readPaths)
	if err != nil {
		return intercept.ReviewInput{}, err
	}
	defer client.Close()
	cwd, background := intercept.ReviewContextFrom(ctx)
	if tc != nil && tc.WorkingDir != "" {
		cwd = tc.WorkingDir
	}
	if cwd == "" {
		cwd = workspace
	}
	raw, err := json.Marshal(map[string]any{"tool": name, "arguments": input, "background": background})
	if err != nil {
		return intercept.ReviewInput{}, err
	}
	result, err := client.Call(ctx, "__review_context", raw, &tool.ToolContext{WorkingDir: cwd})
	if err != nil {
		return intercept.ReviewInput{}, err
	}
	if result.IsError {
		return intercept.ReviewInput{}, errors.New("审查上下文采集失败")
	}
	var out intercept.ReviewInput
	if err = json.Unmarshal([]byte(result.Flatten()), &out); err != nil {
		return intercept.ReviewInput{}, err
	}
	return out, nil
}

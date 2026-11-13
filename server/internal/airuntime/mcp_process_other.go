//go:build !linux

package airuntime

import (
	"context"
	"errors"
)

type mcpProcessTracker struct{}

func newMCPProcessTracker() (*mcpProcessTracker, error) {
	return nil, errors.New("local MCP process isolation requires Linux")
}
func (*mcpProcessTracker) reportPath() string { return "" }
func (*mcpProcessTracker) ready(context.Context) error {
	return errors.New("local MCP process isolation requires Linux")
}
func (*mcpProcessTracker) closePipes()   {}
func (*mcpProcessTracker) failedLaunch() {}
func (*mcpProcessTracker) stop()         {}
func (*mcpProcessTracker) wait()         {}

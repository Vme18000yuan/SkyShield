//go:build !linux

package ailocal

import "errors"

func createMCPProcessSession() error { return errors.New("local MCP process isolation requires Linux") }

func enterSandbox(string, string, []string, []string, []string) error {
	return errors.New("本地工具执行目前需要 Linux Landlock")
}

//go:build linux

package ailocal

import (
	"errors"
	"fmt"
	"github.com/google/uuid"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

// The deployment supplies a delegated cgroup-v2 subtree. UseCgroupFD attaches
// the process atomically at clone time; descendants cannot race ahead of limits.
func constrainWorker(command *exec.Cmd) (func(), error) {
	root := os.Getenv("TIANQIONG_AI_CGROUP_ROOT")
	if root == "" {
		if os.Getenv("TIANQIONG_AI_REQUIRE_CGROUP") == "true" {
			return nil, errors.New("AI Runner 需要委派的 cgroup v2 目录")
		}
		return func() {}, nil
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) == "/" {
		return nil, errors.New("invalid AI cgroup root")
	}
	dir := filepath.Join(root, "skyshield-"+uuid.NewString())
	if err := os.Mkdir(dir, 0700); err != nil {
		return nil, fmt.Errorf("create AI resource group: %w", err)
	}
	clean := func() { _ = os.WriteFile(filepath.Join(dir, "cgroup.kill"), []byte("1"), 0600); _ = os.Remove(dir) }
	limits := map[string]string{"memory.max": "1073741824", "pids.max": "128", "cpu.max": "200000 100000"}
	for key, env := range map[string]string{"memory.max": "TIANQIONG_AI_WORKER_MEMORY_BYTES", "pids.max": "TIANQIONG_AI_WORKER_PIDS"} {
		if raw := os.Getenv(env); raw != "" {
			n, err := strconv.ParseUint(raw, 10, 64)
			if err != nil || n == 0 {
				clean()
				return nil, fmt.Errorf("invalid %s", env)
			}
			limits[key] = raw
		}
	}
	for name, value := range limits {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
			clean()
			return nil, fmt.Errorf("set AI resource limit: %w", err)
		}
	}
	file, err := os.Open(dir)
	if err != nil {
		clean()
		return nil, err
	}
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.UseCgroupFD = true
	command.SysProcAttr.CgroupFD = int(file.Fd())
	return func() { file.Close(); clean() }, nil
}

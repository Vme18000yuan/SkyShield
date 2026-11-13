//go:build linux

package airuntime

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type mcpProcessIdentity struct {
	pid   int
	start string
}
type mcpProcessTracker struct {
	read, write *os.File
	result      chan mcpProcessIdentity
	identity    mcpProcessIdentity
	once        sync.Once
}

func newMCPProcessTracker() (*mcpProcessTracker, error) {
	read, write, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	t := &mcpProcessTracker{read: read, write: write, result: make(chan mcpProcessIdentity, 1)}
	go func() {
		line, err := bufio.NewReaderSize(read, 128).ReadString('\n')
		pid, parseErr := strconv.Atoi(strings.TrimSpace(line))
		identity := mcpProcessIdentity{}
		if err == nil && parseErr == nil && pid > 0 {
			if fields, readErr := processStat(pid); readErr == nil {
				identity = mcpProcessIdentity{pid: pid, start: fields[19]}
			}
		}
		t.result <- identity
	}()
	return t, nil
}
func (t *mcpProcessTracker) reportPath() string {
	return fmt.Sprintf("/proc/%d/fd/%d", os.Getpid(), t.write.Fd())
}
func (t *mcpProcessTracker) ready(ctx context.Context) error {
	select {
	case t.identity = <-t.result:
		_ = t.write.Close()
		_ = t.read.Close()
		if t.identity.pid <= 0 || t.identity.start == "" {
			return fmt.Errorf("MCP process identity unavailable")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (t *mcpProcessTracker) closePipes() {
	_ = t.write.Close()
	_ = t.read.Close()
}
func (t *mcpProcessTracker) failedLaunch() { _ = t.write.Close() }
func (t *mcpProcessTracker) stop() {
	t.once.Do(func() {
		if t.identity.pid <= 0 {
			return
		}
		// Keep a PID reuse from targeting another process group. The leader is
		// still our unreaped child when Close is called.
		fields, err := processStat(t.identity.pid)
		if err == nil && fields[19] == t.identity.start && fields[2] == strconv.Itoa(t.identity.pid) && fields[3] == strconv.Itoa(t.identity.pid) {
			_ = syscall.Kill(-t.identity.pid, syscall.SIGKILL)
		}
	})
}
func (t *mcpProcessTracker) wait() {
	if t.identity.pid <= 0 {
		return
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		active := false
		paths, _ := filepath.Glob("/proc/[0-9]*/stat")
		for _, path := range paths {
			pid, err := strconv.Atoi(filepath.Base(filepath.Dir(path)))
			if err != nil {
				continue
			}
			fields, err := processStat(pid)
			if err == nil && fields[0] != "Z" && fields[2] == strconv.Itoa(t.identity.pid) && fields[3] == strconv.Itoa(t.identity.pid) {
				active = true
				break
			}
		}
		if !active {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}
func processStat(pid int) ([]string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return nil, err
	}
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return nil, fmt.Errorf("invalid process stat")
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) <= 19 {
		return nil, fmt.Errorf("incomplete process stat")
	}
	return fields, nil
}

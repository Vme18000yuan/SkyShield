//go:build !linux

package ailocal

import "os/exec"

func constrainWorker(*exec.Cmd) (func(), error) { return func() {}, nil }

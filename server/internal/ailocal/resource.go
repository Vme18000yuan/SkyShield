package ailocal

import "os/exec"

// ConstrainCommand configures the same resource boundary for local MCP tools.
// Release must run after the command exits, and also on launch failure.
func ConstrainCommand(command *exec.Cmd) (func(), error) { return constrainWorker(command) }

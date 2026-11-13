# Local tool execution

The worker confines file, shell and PTY tools to the selected workspace. A
normal command error or per-tool timeout returns an error result to the Agent;
it must not be treated as a successful observation or a cancelled whole run.

Hosted sessions set `ExternalBackgroundTasks`: Bash, Monitor, TaskOutput,
TaskList and TaskStop all execute in this worker and share its manager. The
outer SDK must not auto-register another set of same-named controls. Task IDs
are valid only for the worker/run that created them; old transcript IDs do not
resurrect processes after a run or server restart.

The client monitors both the process and its JSON response stream. Malformed
or closed transport wakes waiting calls and closes the worker instead of
waiting indefinitely. A fully received result takes precedence over a later
transport close. User cancellation remains authoritative.

The response watchdog uses the worker's existing ten-minute tool limit plus
thirty seconds of transport grace. A silent or partially written frame cannot
hold a conversation forever. It reports an uncertain result and closes the
unresponsive worker; it never reports a timeout as successful execution.

Transport loss can leave execution uncertain. Never reconnect and replay a
command automatically: it may already have changed files or performed network
requests. Return the uncertainty explicitly so the Agent can inspect existing
artifacts or choose another available capability.

The watchdog includes waiting for and writing the request pipe. Progress frames
have a separate bounded channel and cannot overwrite a final result. Ordinary
calls have eight slots; task query/stop calls retain two reserved slots.

Hosted workers retain command output and intent/result checkpoints under the
workspace's `.task-output` directory. Missing completion checkpoints mean an
uncertain outcome, not success; a new manager never replays those commands.
Session/task workspaces are writable; shared skill resources are read-only.
Optional delegated cgroup-v2 limits apply atomically when the worker is created,
including to stdio MCP. See `docs/12-AI执行可靠性整改.md` for deployment settings.

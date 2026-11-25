# AI history and storage deletion

The authenticated deletion routes use the same tenant/user ownership and parent
row locks as AI execution admission. Tasks must be stopped before deletion;
conversation deletion first requests a cooperative stop and waits briefly for
the terminal run acknowledgement. Trigger dispatch is fenced, including queued
jobs that do not yet have a conversation.

Deleting a task or conversation removes its database history and detailed model
recordings, and commits a filesystem cleanup job in the same transaction. The
job removes the owned work directory, transcripts and subagent transcripts,
compaction archives, traffic directory, CA bundle and linked information-gathering
artifacts. Task cleanup also includes its automatically triggered conversations.
Files are removed through `os.Root`; IDs and directory prefixes are validated.
Active source recorders and local task executors delay file removal. A cleanup
failure remains queued for retry after process restart and is returned as
`cleanupPending`, rather than reported as fully reclaimed storage.

Registered global assets and vulnerabilities are never deleted. Saved
conversation vulnerability reports/evidence retain their owner and vulnerability
link after their conversation/run references become NULL. Independent evidence
copies bound to findings are not disposable traffic. Token accounting and
trigger/review audit metadata retain their existing retention behavior.

Tool execution deletion removes the selected call/result pairs, using run,
worker, graph-node and call-ID boundaries to avoid deleting a later repeated
call. Raw transcripts remain until the parent conversation/task is deleted.
Traffic supports exact-record deletion and an explicitly confirmed whole-source
clear; shared blobs are collected only after their last reference is removed.
Interrupted legacy file removal is recovered from a commit-aware traffic
journal. Failed traffic reclamation is queued without replaying the original
record deletion, so a retry does not delete newly captured records.
The traffic store compacts/reclaims its SQLite index. PostgreSQL reuses deleted
row/TOAST space through its normal vacuum behavior; no blocking `VACUUM FULL`
is run from an HTTP request.

Migration 000086 adds the durable queue and narrowly scoped graph deletion
fences. The complete integration test runs only on a fresh database named
`skyshield_cleanup_test_*` via `SKYSHIELD_CLEANUP_TEST_DSN`. It covers ownership,
live-run rejection, repeated tool IDs, preserved registered data, symlink safety,
retry after restart, source trigger cancellation and traffic cleanup.

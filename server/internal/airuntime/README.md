# Model interruption and context recovery

Model errors and tool coverage are separate outcomes. A successful tool return
may describe a partial scan. Conversation failure recovery preserves completed
information-gathering checkpoints and applies the normal import rules; it never
reports an incomplete phase as complete.

`configuredProvider` owns pre-output streaming recovery. Its HTTP attempts share
a budget across SDK retries and the optional non-streaming fallback. After the
first failure, recovery has a 90-second ceiling within the caller's deadline.
Visible text and dispatched tools are never replayed by this loop. An exhausted
recovery is marked so the task intent loop cannot start another retry ladder.
Cancellation and run budgets remain authoritative. No provider or proxy is
changed automatically by this recovery path.

The conversation UI offers continuation after a model failure once its final
activity page has loaded. Continuation uses the same conversation's saved
transcript and collection IDs, preserving the unsent draft. Task runs retain
their existing pause/resume and lease handling.

Both context managers include system/tool-schema overhead and reserve the
actual output budget. The prior request's input usage calibrates the estimate;
new assistant and tool messages contribute additional growth. Cached input
tokens are already included in canonical input usage and are not added twice.
Built-in summaries must be nonempty and reduce the working set, and their
boundaries preserve complete tool exchanges. NOA learns a provider-reported
window, follows profile changes, and scales its compression cadence for small
windows. Raw transcripts and compression archives remain intact.

Regression checks use mock model transports and scan tools. They cover EOF
before output, no replay after output, shared retry exhaustion, continuation
from a saved tool result with both context managers, large tool-result growth,
tool pairing, summary rejection, and long-session compression under partial
model compliance. Live upstream availability is a separate operational check.

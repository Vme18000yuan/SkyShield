# notification

`notification` owns canonical occurrence validation, outbox materialization,
per-user inbox projections, installation destinations, and durable delivery
state. SkyShield uses the inbox and SSE projections. External delivery goes through
`internal/ainotification` and its account-owned channels. Existing installation
credentials stay in place; they are not copied or attributed to a user. Producer modules may append a
typed occurrence through a narrow transaction-owned port; they do not render
messages, expand recipients, or perform network I/O.

`domain/` contains closed vocabulary and immutable business records.
`application/` owns use cases and ports. `repository/`, `handler/`,
`router/`, and provider adapters are infrastructure boundaries and must not
redefine notification kinds, payloads, identity, or templates.

`auth_user.locale` is the sole asynchronous rendering authority. The frontend
synchronizes its already-resolved page locale through the current-user locale
resource; workers never infer a locale from browser data. Inbox locale, title,
and message are immutable projection snapshots after materialization.

The vocabulary is deliberately split at the domain boundary: inbox decoding
uses `InboxSupportedKinds`, while installation destination settings and fanout
use `ExternallyDeliverableKinds` in the legacy module. SkyShield routes new registered scan vulnerabilities and owned chat reports into the AI notification queue. AI task findings keep their existing event identity and account scope. The `000082` migration retires previously configured installation senders. Operators who used them can configure a personal channel under AI notification settings. `nuclei-poc-sync-succeeded` and
`nuclei-poc-sync-failed` are producer-owned Nuclei terminal facts accepted only
by the inbox pipeline; they cannot be subscribed to or delivered through
Discord, WeCom, Feishu, or another external destination. The failed kind also
accepts only the Nuclei domain's closed failure-code/summary pairs, so raw
repository diagnostics cannot become a durable inbox payload or display string.

The authenticated notification SSE endpoint carries invalidation hints only;
the persisted inbox and unread-count queries remain authoritative. The Server
flushes an SSE comment after 15 seconds of stream inactivity so idle proxy paths
stay observable, while JWT expiry and request cancellation still end the stream.

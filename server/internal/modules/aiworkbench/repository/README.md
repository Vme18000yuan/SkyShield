# Conversation storage

The authenticated exploration endpoint also accepts `view=timeline`. Its overview covers the selected run, while `from` pages the merged call/result cards in durable call order. Only an opened source drawer fetches full activity detail. User checklist items and asset mentions come from the saved user message. A completed Todo item represents the recorded checklist status.

Each section includes a task purpose and its source activity IDs. A successful checklist update with one active item or a public task declaration establishes a boundary. Older runs can present localized topics inferred from several recorded steps. The response labels the source and keeps the chronological ordinals for paging and evidence links. An unsupported purpose is reported as unknown.

Migration 84 adds optional observation fields for interactive chat. Actual start and finish timestamps surround the tool implementation after admission. A fresh occurrence ID pairs each returned record even if the provider repeats its tool ID. Overlap requires intersecting measured intervals. An older ambiguous pair stays unresolved. A registered finding can link to its originating call and earlier successful reads of the same saved evidence through scoped activity IDs.

- `ReadConversationExploration` is a read-only, owner/tenant-fenced projection of
  saved chat runs, tool-call/result pairs and vulnerability registration receipts.
  It paginates runs and calls independently of the chat's loaded transcript window.
  Execution order is not a causal claim; a tool result is not a confirmed finding.
  Missing results remain running only while the run lease is live, otherwise unknown.
  Finding nodes require an actual `ai_conversation_vulnerability_report` row.
  Full inputs, outputs and HTTP evidence are fetched only on explicit selection
  through the existing authenticated detail/evidence endpoints.

- Activity rows are a readable projection. PostgreSQL text cannot store NUL:
  `printableActivityText` renders it as `\x00` and replaces invalid UTF-8 for
  display. Raw session transcripts and workspace artifacts remain unchanged.
- Side-question snapshots contain protocol data, including reasoning and tool
  arguments. Never sanitize or remove characters from these structures. A
  document containing escaped NUL uses the `json-string-v1` envelope so JSONB
  can store it losslessly. Other documents retain their existing shape.
- All snapshot, exchange, and memory readers must use `unmarshalSideDocument`.
  Snapshot ordering metadata remains at the envelope root; cancellation and
  expiry status/error overrides remain authoritative when decoding exchanges.
- Side-question admission reads the latest conversation run and its lease in
  the authorized transaction. The application supplies that checked status to
  the side answer independently of the potentially older context snapshot.
- Deleting an owned conversation locks against trigger-job dispatch and
  cancels its queued, starting, or running trigger jobs in the same transaction.
  Completed jobs and unrelated conversations remain intact; review and retest
  projections retain their existing stop/audit behavior. A waiting job must not
  recreate a conversation after its owner deletes it.
- Automatically triggered Reporter event/interval jobs use internal conversation
  records for execution and audit, but are excluded from the interactive chat
  list before its 200-item limit. A prior automatic conversation that contains
  an explicitly started user run remains visible, as do manual Reporter chats,
  historical finding reviews, retests, and other agents' conversations.
  Filtering never removes records or changes report generation.

- Historical finding review creation is retired. The dispatcher excludes its
  jobs and reconciliation cancels queued/starting reviews or requests a
  cooperative stop for running reviews. Historical records and read/cancel
  access remain available for audit; automatic Reporter jobs and retests are
  unaffected.

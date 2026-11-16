# Conversation storage

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

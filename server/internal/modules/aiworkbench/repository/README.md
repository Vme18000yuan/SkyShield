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

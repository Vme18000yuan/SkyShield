# Asset coverage graph

Shared presentation for AI task scope and root-domain inventory. Transport and
query ownership live in `asset-coverage.service.ts` and `use-asset-coverage.ts`.
The discriminated scope includes the root organization filter; account and scope
changes remount local selection and folding state and use separate query caches.

The graph owns two inspection sheets: a folded-child browser with an expansion
footer, and node evidence. Their existing task interactions are preserved when
used from inventory. Canonical asset IDs alone permit evidence queries; synthetic
port/connector nodes never acquire fabricated IDs. Root evidence links back to
readable source tasks. Inline graph failures hide stale nodes and evidence while
leaving the surrounding detail page and retry control available.

Edges run from child to parent in the API and are reversed for left-to-right
rendering. Sibling folding prioritizes confirmed facts. A green border denotes
recorded confirmed facts, not exhaustive testing or freedom from vulnerabilities.

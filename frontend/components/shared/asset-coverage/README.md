# Asset coverage graph

Shared presentation for AI task scope and root-domain inventory. Transport and
query ownership live in `asset-coverage.service.ts` and `use-asset-coverage.ts`.
The discriminated scope includes the root organization filter; account and scope
changes remount local selection and folding state and use separate query caches.

The default presentation is a searchable, grouped hierarchy with an inline
asset detail pane; the original topology remains available as a separate view.
Groups expand in batches and long visible lists are virtualized. Search covers
the loaded graph, automatically opens matching paths, and preserves the explicit
5,000-node truncation notice. A spanning forest places each asset once; additional
real parent edges appear as selectable references, including in cyclic legacy
data. No relationships or canonical asset IDs are inferred by the presentation.

The topology retains its folded-child browser and evidence sheet. Canonical
asset IDs alone permit evidence queries; synthetic port/connector nodes never
acquire fabricated IDs. Root evidence links back to readable source tasks.
Inline graph failures hide stale nodes and evidence while leaving the surrounding
detail page and retry control available.

Edges run from child to parent in the API and are reversed for left-to-right
rendering. Sibling folding prioritizes confirmed facts. A green border denotes
recorded confirmed facts, not exhaustive testing or freedom from vulnerabilities.

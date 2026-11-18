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

The topology uses round type icons, compact labels and circle-to-circle straight
edges. A D3 force simulation runs in a dedicated worker and cools to a stop;
worker failures fall back to scheduled main-thread ticks. Unmounting or replacing
the visible graph cancels the old simulation. Messages are generation-bound, so
late coordinates cannot restore a collapsed branch. Reduced-motion preferences
skip intermediate animation frames.

Dragged nodes remain pinned across expansion and refresh. Rearranging clears
these pins. Parent controls collapse/expand branches; gray ellipsis nodes expand
children in batches and also offer a virtualized folded-child browser. Shared
children remain accessible through other expanded parents. Collapse/expansion
retains coordinates and the user's viewport; the initial view and explicit reset
actions fit the graph after settling. The topology retains its evidence sheet. Canonical
asset IDs alone permit evidence queries; synthetic port/connector nodes never
acquire fabricated IDs. Root evidence links back to readable source tasks.
Inline graph failures hide stale nodes and evidence while leaving the surrounding
detail page and retry control available.

Edges run from child to parent in the API; rendering preserves these real
relationships without adding inferred links. Sibling folding prioritizes confirmed facts. A green border denotes
recorded confirmed facts, not exhaustive testing or freedom from vulnerabilities.

The topology sends at most 200 nodes (including fold controls) and 400 edges to React Flow and the force simulation. It favors connected domain roots and keeps a real edge to each admitted child before showing additional shared edges. When the canvas reaches its limit, the searchable hierarchy remains available for the rest of the loaded graph.

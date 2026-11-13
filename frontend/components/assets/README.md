# Asset inventory

The inventory can be filtered by organization and supports totals and direct page jumps. The “all” view combines canonical asset records with host:port observations; port rows do not fabricate a canonical asset identity or offer asset-specific AI actions. Explicit service observations are shown as such, while names inferred only from common port numbers are labeled as estimates.

Root-domain rows navigate to an overview and related inventory scoped to the chosen organization. The backend derives relationships from root-source mappings, active target bindings, and recorded IP/domain associations. A root overview does not authorize a broader scan. Original URL, method, observation, and provenance data remain intact even though the list omits the method and source-count columns.

The root overview also exposes an asset coverage graph, using the same scoped inventory and shared graph presentation as AI tasks. Confirmed status and drill-down records come only from AI tasks readable by the current user; a green node means a confirmed fact exists, not that testing is complete. Port observations have no invented canonical asset ID. The graph loads up to 5,000 nodes and reports truncation, folds sibling groups, and keeps organization and account scopes separate in the query cache.

Application rows navigate to an evidence view with related websites and the recognition source. The registration form accepts bound domains so manually recorded applications can be associated with a root and, within their organization, reconciled with matching discovered websites. Identifications are candidates based on available evidence, not assertions of ownership or verified product identity.

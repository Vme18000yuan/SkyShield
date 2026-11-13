# AI approval module

This module adapts ARTEX tool interception to the product's tenant, user and run leases. The application owns rule evaluation and the approval lifecycle; the repository owns scoped persistence; handlers expose the authenticated API. Defaults are sourced from the local ARTEX reference and are seeded only once, so administrator edits and deletions survive restarts.

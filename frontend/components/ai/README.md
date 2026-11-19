# AI conversation exploration

The conversation exploration view follows the graph canvas and card composition
in `CodeScan-Artex/web/src/components/exploration-graph.tsx` (revision `74d34c7`,
AGPL-3.0). The layering algorithm is adapted in `conversation-graph-layout.ts`.
The conversation API remains the source of all titles, occurrence IDs and links.

- The canvas is the primary view. Step, checklist and finding collections open
  in a shared `Sheet`; raw records load only when explicitly expanded.
- `contains` means a recorded section belongs to this run. Section numbers and
  step ranges carry chronology. `registration` and `citation` require saved
  evidence links. Do not turn chronology into an inferred causal edge.
- Six sections are shown per graph window for readable density. Every loaded
  finding in that window is rendered, including unlinked findings; unknown
  sources stay disconnected. The finding drawer retains API pagination.
- Nodes have a draggable header and a keyboard-operable shared `Button` body.
  Polling preserves dragged positions and viewport. Changing graph windows
  resets layout; the explicit reset button restores computed positions.
- Typography uses `textRole`, controls use `Button`, `Select`, `Tabs`, `Sheet`,
  and glyphs come through the shared icon registry. Canvas colors resolve from
  theme tokens. No theme tokens or shared primitive behavior are changed.
- The local CSS owns canvas height and fixed renderer geometry (244 × 140 nodes,
  360 × 168 layout spacing). The single `button-conversation-graph-node` ledger
  exception covers the shared Button's node-body layout, not ordinary controls.

Verification: graph layout tests cover overlap/cycles and all receipt links;
component tests cover panel navigation, historical position, on-demand records
and live-result updates. Run focused Vitest, TypeScript, ESLint and UI foundation
checks. Browser review should include a long run, several receipts from one
section, dark mode, narrow screens, drag followed by polling, and reset layout.

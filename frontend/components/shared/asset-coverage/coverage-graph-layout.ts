// Folding follows ARTEX's task coverage graph, AGPL-3.0-only.
import type { CoverageKind, CoverageNode, CoverageEdge, CoverageGraph } from "@/types/asset-coverage.types"

export type CoverageFold = { key: string; groupId: string; parentKey: string; kind: CoverageKind; hidden: CoverageNode[] }

export const COVERAGE_FOLD_LIMIT = 20
export const COVERAGE_FOLD_STEP = 20

function sortAssets(a: CoverageNode, b: CoverageNode): number {
  if (a.tested !== b.tested) return a.tested ? -1 : 1
  return a.label.localeCompare(b.label) || a.key.localeCompare(b.key)
}

// Edges point from a child to its parent. Fold each parent's children by kind,
// without hiding a shared child that is already visible through another parent.
export function coverageVisible(graph: CoverageGraph, expanded: ReadonlyMap<string, number>) {
  const byKey = new Map(graph.nodes.map(node => [node.key, node]))
  const children = new Map<string, string[]>()
  const hasParent = new Set<string>()
  const edges: CoverageEdge[] = []
  const seenEdges = new Set<string>()
  for (const edge of graph.edges) {
    if (edge.src === edge.dst || !byKey.has(edge.src) || !byKey.has(edge.dst)) continue
    const pair = JSON.stringify([edge.src, edge.dst])
    if (seenEdges.has(pair)) continue
    seenEdges.add(pair)
    edges.push(edge)
    hasParent.add(edge.src)
    const items = children.get(edge.dst) ?? []
    items.push(edge.src)
    children.set(edge.dst, items)
  }

  const roots = graph.nodes.filter(node => !hasParent.has(node.key)).sort(sortAssets).map(node => node.key)
  // A malformed or legacy cyclic component must remain reachable in the UI.
  const reachable = new Set<string>()
  const markReachable = (start: string) => {
    const queue = [start]
    for (let i = 0; i < queue.length; i++) {
      if (reachable.has(queue[i])) continue
      reachable.add(queue[i])
      queue.push(...(children.get(queue[i]) ?? []))
    }
  }
  for (const root of roots) markReachable(root)
  for (const node of graph.nodes) {
    if (!reachable.has(node.key)) {
      roots.push(node.key)
      markReachable(node.key)
    }
  }

  const visible = new Set(roots)
  const queue = [...roots]
  const candidates: CoverageFold[] = []
  for (let i = 0; i < queue.length; i++) {
    const parent = queue[i]
    const groups = new Map<CoverageKind, CoverageNode[]>()
    for (const key of children.get(parent) ?? []) {
      const node = byKey.get(key)
      if (!node) continue
      const items = groups.get(node.kind) ?? []
      items.push(node)
      groups.set(node.kind, items)
    }
    for (const [kind, items] of groups) {
      items.sort(sortAssets)
      const groupId = JSON.stringify([parent, kind])
      const limit = Math.max(COVERAGE_FOLD_LIMIT, expanded.get(groupId) ?? COVERAGE_FOLD_LIMIT)
      for (const item of items.slice(0, limit)) {
        if (!visible.has(item.key)) {
          visible.add(item.key)
          queue.push(item.key)
        }
      }
      const hidden = items.slice(limit)
      if (hidden.length) candidates.push({ key: `fold:${groupId}`, groupId, parentKey: parent, kind, hidden })
    }
  }
  const folds = candidates.map(fold => ({ ...fold, hidden: fold.hidden.filter(node => !visible.has(node.key)) })).filter(fold => fold.hidden.length > 0)
  return {
    nodes: graph.nodes.filter(node => visible.has(node.key)),
    folds,
    edges: [...edges.filter(edge => visible.has(edge.src) && visible.has(edge.dst)), ...folds.map(fold => ({ src: fold.key, dst: fold.parentKey }))],
  }
}

export function coverageNodeLabel(node: CoverageNode): string {
  if (node.kind === "service") {
    const parts = [node.port ? `:${node.port}` : "", node.page_title ?? "", node.status_code ? String(node.status_code) : ""].filter(Boolean)
    if (parts.length) return parts.join(" · ")
  }
  if (node.kind === "endpoint" && node.url) {
    try { return new URL(node.url).pathname || "/" } catch { /* Keep the supplied label. */ }
  }
  return node.label
}

// Arrange each first-parent tree around its leaves while retaining all shared
// edges. Iterative traversal keeps large task graphs off the JavaScript stack.
export function coveragePositions(keys: string[], edges: CoverageEdge[], layers: ReadonlyMap<string, number>) {
  const all = new Set(keys)
  const parents = new Map<string, string>()
  const children = new Map<string, string[]>()
  for (const edge of edges) {
    if (!all.has(edge.src) || !all.has(edge.dst) || parents.has(edge.src)) continue
    parents.set(edge.src, edge.dst)
    const items = children.get(edge.dst) ?? []
    items.push(edge.src)
    children.set(edge.dst, items)
  }
  const positions = new Map<string, { x: number; y: number }>()
  const visiting = new Set<string>()
  let row = 0
  const visit = (root: string) => {
    const stack: { key: string; exit: boolean }[] = [{ key: root, exit: false }]
    while (stack.length) {
      const item = stack.pop()!
      if (item.exit) {
        const ys = (children.get(item.key) ?? []).map(key => positions.get(key)?.y).filter((y): y is number => y !== undefined)
        const y = ys.length ? (ys[0] + ys[ys.length - 1]) / 2 : row++ * 90
        positions.set(item.key, { x: (layers.get(item.key) ?? 0) * 255, y })
        visiting.delete(item.key)
        continue
      }
      if (positions.has(item.key) || visiting.has(item.key)) continue
      visiting.add(item.key)
      stack.push({ key: item.key, exit: true })
      const descendants = children.get(item.key) ?? []
      for (let i = descendants.length - 1; i >= 0; i--) {
        if (!visiting.has(descendants[i])) stack.push({ key: descendants[i], exit: false })
      }
    }
  }
  for (const key of keys) if (!parents.has(key)) visit(key)
  for (const key of keys) if (!positions.has(key)) visit(key)
  return positions
}

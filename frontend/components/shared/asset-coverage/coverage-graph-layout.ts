// Folding follows ARTEX's task coverage graph, AGPL-3.0-only.
import type { CoverageKind, CoverageNode, CoverageEdge, CoverageGraph } from "@/types/asset-coverage.types"

export type CoverageFold = { key: string; groupId: string; parentKey: string; kind: CoverageKind; hidden: CoverageNode[] }

export const COVERAGE_FOLD_LIMIT = 20
export const COVERAGE_FOLD_STEP = 20
// Apply these limits before constructing React Flow nodes or sending work to D3.
export const TOPOLOGY_NODE_LIMIT = 200
export const TOPOLOGY_EDGE_LIMIT = 400
const ASSET_LIMIT = 180 // leave room for folded groups

function sortAssets(a: CoverageNode, b: CoverageNode): number {
  if (a.tested !== b.tested) return a.tested ? -1 : 1
  return a.label.localeCompare(b.label) || a.key.localeCompare(b.key)
}

// Edges point from a child to its parent. Fold each parent's children by kind,
// without hiding a shared child that is already visible through another parent.
export function coverageVisible(graph: CoverageGraph, expanded: ReadonlyMap<string, number>, collapsed: ReadonlySet<string> = new Set()) {
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

  const roots = [...byKey.values()].filter(node => !hasParent.has(node.key)).sort(sortAssets).map(node => node.key)
  // A malformed or legacy cyclic component must remain reachable in the UI.
  const reachable = new Set<string>()
  const markReachable = (start: string) => {
    if (reachable.has(start)) return
    reachable.add(start)
    const queue = [start]
    for (let i = 0; i < queue.length; i++) {
      for (const child of children.get(queue[i]) ?? []) {
        if (reachable.has(child)) continue
        reachable.add(child)
        queue.push(child)
      }
    }
  }
  for (const root of roots) markReachable(root)
  for (const node of graph.nodes) {
    if (!reachable.has(node.key)) {
      roots.push(node.key)
      markReachable(node.key)
    }
  }

  // Reserve the first traversal for connected roots, so thousands of standalone
  // IPs cannot use every slot before a domain's children are visited.
  const rank = (key: string) => {
    const node = byKey.get(key)!
    if (node.kind === "company" || node.kind === "root_domain") return 0
    return children.has(key) ? 1 : 2
  }
  roots.sort((a, b) => rank(a) - rank(b) || sortAssets(byKey.get(a)!, byKey.get(b)!))
  const visible = new Set<string>()
  const candidates: CoverageFold[] = []
  const treeEdges: CoverageEdge[] = []
  let limited = false
  for (const root of roots) {
    if (visible.has(root)) continue
    if (visible.size >= ASSET_LIMIT) { limited = true; break }
    visible.add(root)
    const queue = [root]
    for (let i = 0; i < queue.length; i++) {
      const parent = queue[i]
      const groups = new Map<CoverageKind, CoverageNode[]>()
      for (const key of children.get(parent) ?? []) {
        const node = byKey.get(key)!
        const group = groups.get(node.kind) ?? []
        group.push(node)
        groups.set(node.kind, group)
      }
      for (const [kind, items] of groups) {
        items.sort(sortAssets)
        const groupId = JSON.stringify([parent, kind])
        const limit = collapsed.has(parent) ? 0 : Math.max(COVERAGE_FOLD_LIMIT, expanded.get(groupId) ?? COVERAGE_FOLD_LIMIT)
        const hidden: CoverageNode[] = []
        for (let at = 0; at < items.length; at++) {
          const item = items[at]
          if (visible.has(item.key)) continue
          if (at >= limit || visible.size >= ASSET_LIMIT) {
            if (at < limit) limited = true
            hidden.push(item)
          } else {
            visible.add(item.key)
            queue.push(item.key)
            treeEdges.push({ src: item.key, dst: parent })
          }
        }
        if (hidden.length) candidates.push({ key: `fold:${groupId}`, groupId, parentKey: parent, kind, hidden })
      }
    }
  }
  const allFolds = candidates.map(fold => ({ ...fold, hidden: fold.hidden.filter(node => !visible.has(node.key)) })).filter(fold => fold.hidden.length)
  const folds = allFolds.slice(0, TOPOLOGY_NODE_LIMIT - visible.size)
  const displayedEdges = [...treeEdges, ...folds.map(fold => ({ src: fold.key, dst: fold.parentKey }))]
  const included = new Set(treeEdges.map(edge => JSON.stringify([edge.src, edge.dst])))
  for (const edge of edges) {
    if (!visible.has(edge.src) || !visible.has(edge.dst) || collapsed.has(edge.dst) || included.has(JSON.stringify([edge.src, edge.dst]))) continue
    if (displayedEdges.length >= TOPOLOGY_EDGE_LIMIT) { limited = true; break }
    displayedEdges.push(edge)
  }
  return {
    nodes: [...byKey.values()].filter(node => visible.has(node.key)),
    folds,
    edges: displayedEdges,
    limited: limited || folds.length < allFolds.length,
    canExpand: visible.size < ASSET_LIMIT,
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

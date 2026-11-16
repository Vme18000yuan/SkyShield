import type { CoverageGraph, CoverageKind, CoverageNode } from "@/types/asset-coverage.types"

export const COVERAGE_TREE_PAGE_SIZE = 40
export const COVERAGE_TREE_ROOTS = "coverage-tree-roots"

const kindOrder: CoverageKind[] = ["company", "root_domain", "subdomain", "ip", "service", "app", "endpoint"]

export type CoverageTreeIndex = {
  nodes: Map<string, CoverageNode>
  roots: CoverageNode[]
  children: Map<string, Map<CoverageKind, CoverageNode[]>>
  parents: Map<string, CoverageNode[]>
  primaryParent: Map<string, string>
}

export type CoverageTreeRow =
  | { type: "asset"; id: string; depth: number; node: CoverageNode; reference: boolean; expandable: boolean }
  | { type: "group"; id: string; depth: number; groupId: string; kind: CoverageKind; count: number; open: boolean }
  | { type: "more"; id: string; depth: number; groupId: string; remaining: number }

export type CoverageTreeState = {
  nodes: ReadonlySet<string>
  groups: ReadonlySet<string>
  limits: ReadonlyMap<string, number>
}

function compareNodes(a: CoverageNode, b: CoverageNode) {
  if (a.tested !== b.tested) return a.tested ? -1 : 1
  return a.label.localeCompare(b.label) || a.key.localeCompare(b.key)
}

export function coverageTreeGroupId(parent: string, kind: CoverageKind) {
  return JSON.stringify([parent, kind])
}

// Build a spanning forest from actual edges. Extra parent edges remain visible as
// references, so shared assets and cyclic legacy data are never silently dropped.
export function buildCoverageTree(graph: CoverageGraph): CoverageTreeIndex {
  const nodes = new Map<string, CoverageNode>()
  for (const node of graph.nodes) if (!nodes.has(node.key)) nodes.set(node.key, node)
  const childKeys = new Map<string, Set<string>>()
  const parentKeys = new Map<string, Set<string>>()
  for (const edge of graph.edges) {
    if (edge.src === edge.dst || !nodes.has(edge.src) || !nodes.has(edge.dst)) continue
    const children = childKeys.get(edge.dst) ?? new Set<string>()
    children.add(edge.src)
    childKeys.set(edge.dst, children)
    const parents = parentKeys.get(edge.src) ?? new Set<string>()
    parents.add(edge.dst)
    parentKeys.set(edge.src, parents)
  }

  const sorted = [...nodes.values()].sort(compareNodes)
  const roots = sorted.filter(node => !parentKeys.has(node.key))
  const primaryParent = new Map<string, string>()
  const visited = new Set(roots.map(node => node.key))
  const queue = roots.map(node => node.key)
  let cursor = 0
  const drain = () => {
    while (cursor < queue.length) {
      const parent = queue[cursor++]
      const children = [...(childKeys.get(parent) ?? [])].map(key => nodes.get(key)!).sort(compareNodes)
      for (const child of children) if (!visited.has(child.key)) {
        visited.add(child.key)
        primaryParent.set(child.key, parent)
        queue.push(child.key)
      }
    }
  }
  drain()
  for (const node of sorted) if (!visited.has(node.key)) {
    roots.push(node)
    visited.add(node.key)
    queue.push(node.key)
    drain()
  }

  const children = new Map<string, Map<CoverageKind, CoverageNode[]>>()
  for (const [parent, keys] of childKeys) {
    const groups = new Map<CoverageKind, CoverageNode[]>()
    for (const key of keys) {
      const node = nodes.get(key)!
      const group = groups.get(node.kind) ?? []
      group.push(node)
      groups.set(node.kind, group)
    }
    for (const group of groups.values()) group.sort(compareNodes)
    children.set(parent, groups)
  }
  const parents = new Map<string, CoverageNode[]>()
  for (const [child, keys] of parentKeys) parents.set(child, [...keys].map(key => nodes.get(key)!).sort(compareNodes))
  return { nodes, roots, children, parents, primaryParent }
}

export function initialCoverageTreeState(index: CoverageTreeIndex): CoverageTreeState {
  const root = index.roots[0]
  const groups = new Set<string>()
  if (root) {
    const firstKind = kindOrder.find(kind => index.children.get(root.key)?.has(kind))
    if (firstKind) groups.add(coverageTreeGroupId(root.key, firstKind))
  }
  return { nodes: new Set(root ? [root.key] : []), groups, limits: new Map() }
}

function searchable(node: CoverageNode) {
  return [node.label, node.kind, node.domain, node.root_domain, node.ip, node.url, node.port, node.service_type, node.app_name, node.page_title, node.status_code].filter(value => value != null).join(" ").toLocaleLowerCase()
}

export function coverageTreeRows(index: CoverageTreeIndex, state: CoverageTreeState, rawQuery: string) {
  const query = rawQuery.trim().toLocaleLowerCase()
  const matches = new Set<string>()
  const needed = new Set<string>()
  if (query) {
    for (const node of index.nodes.values()) if (searchable(node).includes(query)) matches.add(node.key)
    for (const key of matches) {
      let current: string | undefined = key
      while (current && !needed.has(current)) {
        needed.add(current)
        current = index.primaryParent.get(current)
      }
    }
  }
  const allowed = (node: CoverageNode) => !query || needed.has(node.key)
  const rows: CoverageTreeRow[] = []
  const stack: CoverageTreeRow[] = []
  const roots = index.roots.filter(allowed)
  const rootLimit = state.limits.get(COVERAGE_TREE_ROOTS) ?? COVERAGE_TREE_PAGE_SIZE
  if (roots.length > rootLimit) stack.push({ type: "more", id: COVERAGE_TREE_ROOTS, depth: 0, groupId: COVERAGE_TREE_ROOTS, remaining: roots.length - rootLimit })
  for (const root of roots.slice(0, rootLimit).reverse()) stack.push({ type: "asset", id: `root:${root.key}`, depth: 0, node: root, reference: false, expandable: index.children.has(root.key) })

  while (stack.length) {
    const row = stack.pop()!
    rows.push(row)
    if (row.type === "asset" && !row.reference && (state.nodes.has(row.node.key) || Boolean(query))) {
      const groups = index.children.get(row.node.key)
      if (!groups) continue
      for (const kind of [...kindOrder].reverse()) {
        const items = (groups.get(kind) ?? []).filter(allowed)
        if (!items.length) continue
        const groupId = coverageTreeGroupId(row.node.key, kind)
        stack.push({ type: "group", id: `group:${groupId}`, depth: row.depth + 1, groupId, kind, count: items.length, open: Boolean(query) || state.groups.has(groupId) })
      }
    } else if (row.type === "group" && row.open) {
      const [parent, kind] = JSON.parse(row.groupId) as [string, CoverageKind]
      const items = (index.children.get(parent)?.get(kind) ?? []).filter(allowed)
      const limit = state.limits.get(row.groupId) ?? COVERAGE_TREE_PAGE_SIZE
      if (items.length > limit) stack.push({ type: "more", id: `more:${row.groupId}`, depth: row.depth + 1, groupId: row.groupId, remaining: items.length - limit })
      for (const node of items.slice(0, limit).reverse()) {
        const reference = index.primaryParent.get(node.key) !== parent
        stack.push({ type: "asset", id: `asset:${JSON.stringify([parent, node.key])}`, depth: row.depth + 1, node, reference, expandable: !reference && index.children.has(node.key) })
      }
    }
  }
  return { rows, matches: matches.size }
}

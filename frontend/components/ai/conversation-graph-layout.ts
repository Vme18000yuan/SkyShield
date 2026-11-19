// Adapted from CodeScan-Artex/web/src/components/exploration-graph.tsx
// (74d34c7), AGPL-3.0: longest-path layers and barycenter sweeps. Conversation
// edges describe containment/recorded evidence, never inferred causality.
export function explorationLayout(ids: string[], edges: { source: string; target: string }[]) {
  const known = new Set(ids)
  const valid = edges.filter(edge => known.has(edge.source) && known.has(edge.target))
  const outgoing = new Map<string, string[]>()
  for (const { source, target } of valid) outgoing.set(source, [...(outgoing.get(source) ?? []), target])
  const visited = new Map<string, number>()
  const back = new Set<string>()
  for (const root of ids) {
    if (visited.has(root)) continue
    const stack = [{ id: root, index: 0 }]
    visited.set(root, 1)
    while (stack.length) {
      const top = stack[stack.length - 1]
      const children = outgoing.get(top.id) ?? []
      if (top.index === children.length) { visited.set(top.id, 2); stack.pop(); continue }
      const child = children[top.index++]
      if (visited.get(child) === 1) back.add(`${top.id}:${child}`)
      else if (!visited.has(child)) { visited.set(child, 1); stack.push({ id: child, index: 0 }) }
    }
  }
  const dag = valid.filter(edge => !back.has(`${edge.source}:${edge.target}`))
  const incoming = new Map(ids.map(id => [id, 0]))
  const next = new Map<string, string[]>()
  for (const { source, target } of dag) {
    incoming.set(target, incoming.get(target)! + 1)
    next.set(source, [...(next.get(source) ?? []), target])
  }
  const depth = new Map(ids.map(id => [id, 0]))
  const queue = ids.filter(id => incoming.get(id) === 0)
  for (let i = 0; i < queue.length; i++) {
    const id = queue[i]
    for (const child of next.get(id) ?? []) {
      depth.set(child, Math.max(depth.get(child)!, depth.get(id)! + 1))
      incoming.set(child, incoming.get(child)! - 1)
      if (incoming.get(child) === 0) queue.push(child)
    }
  }
  const columns: string[][] = Array.from({ length: Math.max(0, ...depth.values()) + 1 }, () => [])
  ids.forEach(id => columns[depth.get(id)!].push(id))
  const neighbors = new Map<string, string[]>()
  for (const { source, target } of dag) {
    neighbors.set(source, [...(neighbors.get(source) ?? []), target])
    neighbors.set(target, [...(neighbors.get(target) ?? []), source])
  }
  for (let pass = 0; pass < 4; pass++) {
    for (const direction of [1, -1]) {
      const start = direction === 1 ? 1 : columns.length - 2
      const end = direction === 1 ? columns.length : -1
      for (let c = start; c !== end; c += direction) {
        const reference = new Map(columns[c - direction].map((id, i) => [id, i]))
        columns[c] = columns[c].map((id, i) => {
          const scores = (neighbors.get(id) ?? []).map(other => reference.get(other)).filter((n): n is number => n !== undefined)
          return { id, i, score: scores.length ? scores.reduce((a, b) => a + b, 0) / scores.length : i }
        }).sort((a, b) => a.score - b.score || a.i - b.i).map(item => item.id)
      }
    }
  }
  const midline = (Math.max(1, ...columns.map(column => column.length)) - 1) * 84
  const positions = new Map<string, { x: number; y: number }>()
  columns.forEach((column, c) => column.forEach((id, i) => {
    positions.set(id, { x: c * 360, y: midline - (column.length - 1) * 84 + i * 168 })
  }))
  return positions
}

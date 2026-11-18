import type { CoverageEdge } from "@/types/asset-coverage.types"

export const TOPOLOGY_NODE_WIDTH = 176
export const TOPOLOGY_NODE_HEIGHT = 88
export const TOPOLOGY_CENTER = { x: TOPOLOGY_NODE_WIDTH / 2, y: 28 }
export type CoveragePoint = { id: string; x: number; y: number; pinned?: boolean }
export type CoverageForceInput = { id: string; radius: number; parent?: string }
export type ForceRequest =
  | { type: "start"; generation: number; nodes: CoverageForceInput[]; edges: CoverageEdge[]; points: CoveragePoint[] }
  | { type: "pin"; generation: number; point: CoveragePoint }
export type ForceFrame = { generation: number; points: CoveragePoint[]; settled: boolean }

function hash(value: string) {
  let result = 2166136261
  for (let i = 0; i < value.length; i++) result = Math.imul(result ^ value.charCodeAt(i), 16777619)
  return result >>> 0
}

// Keep existing coordinates and introduce newly unfolded nodes near their parent.
// A deterministic initial spread prevents random jumps after a simple refresh.
export function seedCoveragePoints(nodes: CoverageForceInput[], saved: ReadonlyMap<string, CoveragePoint>): CoveragePoint[] {
  const ordered = [...nodes].sort((a, b) => a.id.localeCompare(b.id))
  const result = new Map<string, CoveragePoint>()
  for (const item of ordered) {
    const previous = saved.get(item.id)
    if (previous && Number.isFinite(previous.x) && Number.isFinite(previous.y)) result.set(item.id, { ...previous })
  }
  ordered.forEach((item, index) => {
    if (result.has(item.id)) return
    const parent = item.parent ? result.get(item.parent) : null
    const angle = parent ? (hash(item.id) / 0xffffffff) * 2 * Math.PI : index * Math.PI * (3 - Math.sqrt(5))
    const radius = parent ? 145 : 90 * Math.sqrt(index + .5)
    result.set(item.id, { id: item.id, x: (parent?.x ?? 0) + Math.cos(angle) * radius, y: (parent?.y ?? 0) + Math.sin(angle) * radius })
  })
  return nodes.map(item => result.get(item.id)!)
}

import { describe, expect, it, vi } from "vitest"
import { explorationLayout } from "../../conversation-graph-layout"
import { timelineDiagram } from "../../conversation-timeline-graph"
import { reconcileMapNodes } from "../../conversation-timeline-map"
import type { ConversationTimeline } from "@/types/conversation-exploration.types"

const t = Object.assign((key: string) => key, { has: () => true })
const counts = { steps: 4, returned: 4, failed: 0, interrupted: 0, pending: 0, missing: 0, measured: 0, overlap: 0 }
const fixture = (): ConversationTimeline => ({
  run: null, runs: [], request: { text: "Review" }, conclusion: { text: "Done" }, counts,
  sections: [1, 5].map(first => ({ ...counts, first, last: first + 3, tool: "Read", findings: 0 })),
  findings: [], findingCount: 0, steps: [], checks: [], checksTruncated: false,
})
function diagram(graph: ConversationTimeline) {
  return timelineDiagram({ graph, from: 1, choose: vi.fn(), goal: vi.fn(), outcome: vi.fn(), receipts: vi.fn() }, t)
}

describe("layered conversation graph", () => {
  it("layers branches without overlapping nodes, including cycles and missing endpoints", () => {
    const ids = ["root", "a", "b", "c", "d", "isolated"]
    const edges = [["root", "a"], ["root", "b"], ["a", "c"], ["b", "c"], ["c", "d"], ["d", "c"], ["missing", "a"]].map(([source, target]) => ({ source, target }))
    const positions = explorationLayout(ids, edges)
    expect(positions.size).toBe(ids.length)
    expect(positions.get("a")!.x).toBe(positions.get("b")!.x)
    expect(positions.get("c")!.x).toBeGreaterThan(positions.get("a")!.x)
    for (const [i, id] of ids.entries()) for (const other of ids.slice(i + 1)) {
      const a = positions.get(id)!, b = positions.get(other)!
      expect(Math.abs(a.x - b.x) >= 244 || Math.abs(a.y - b.y) >= 140).toBe(true)
    }
  })

  it("renders all loaded findings and keeps an unknown origin unlinked", () => {
    const graph = fixture()
    graph.findings = Array.from({ length: 5 }, (_, i) => ({ receiptId: String(i), vulnerabilityId: i + 1, title: "Finding " + i, severity: "low", reviewed: false, step: i === 4 ? undefined : 2 }))
    graph.findingCount = 5
    const { nodes, edges } = diagram(graph)
    expect(nodes.filter(node => node.data.kind === "finding")).toHaveLength(5)
    expect(edges.filter(edge => edge.source === "section-1" && edge.target.startsWith("receipt-"))).toHaveLength(4)
    expect(edges.some(edge => edge.target === "receipt-4")).toBe(false)
    // Task membership is explicit; no causal or sequential edges are invented.
    expect(edges.some(edge => edge.source === "section-1" && edge.target === "section-5")).toBe(false)
    expect(edges.find(edge => edge.target === "section-5")?.label).toBe("map.contains")
  })

  it("preserves dragged positions on a live update and moves new cards out of their way", () => {
    const initial = diagram(fixture()).nodes
    const moved = initial.map(node => node.id === "section-1" ? { ...node, position: { x: 720, y: 0 } } : node)
    const graph = fixture()
    graph.findings = [{ receiptId: "new", vulnerabilityId: 3, title: "New result", severity: "low", reviewed: false, step: 1 }]
    const updated = diagram(graph).nodes.map(node => node.id === "receipt-new" ? { ...node, position: { x: 720, y: 0 } } : node)
    const merged = reconcileMapNodes(moved, updated)
    expect(merged.find(node => node.id === "section-1")?.position).toEqual({ x: 720, y: 0 })
    expect(merged.find(node => node.id === "receipt-new")!.position.y).toBeGreaterThanOrEqual(156)
  })
})

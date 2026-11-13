import { describe, expect, it } from "vitest"
import { coverageNodeLabel, coveragePositions, coverageVisible } from "@/components/shared/asset-coverage/coverage-graph-layout"
import type { CoverageGraph, CoverageNode } from "@/types/asset-coverage.types"

const node = (key: string, kind: CoverageNode["kind"], tested = false): CoverageNode => ({ key, kind, label: key, tested, in_scope: true })

describe("ARTEX task coverage graph", () => {
  it("folds children per parent and type, prioritizes tested assets, and expands in batches", () => {
    const domains = Array.from({ length: 45 }, (_, index) => node(`domain-${String(index).padStart(2, "0")}`, "subdomain", index === 44))
    const graph: CoverageGraph = {
      nodes: [node("root", "root_domain"), ...domains, node("hidden-service", "service")],
      edges: [...domains.map(item => ({ src: item.key, dst: "root" })), { src: "hidden-service", dst: "domain-43" }],
      truncated: false,
    }
    const first = coverageVisible(graph, new Map())
    expect(first.nodes).toHaveLength(21)
    expect(first.nodes.some(item => item.key === "domain-44")).toBe(true)
    expect(first.nodes.some(item => item.key === "hidden-service")).toBe(false)
    expect(first.folds).toHaveLength(1)
    expect(first.folds[0].hidden).toHaveLength(25)
    const more = coverageVisible(graph, new Map([[first.folds[0].groupId, 40]]))
    expect(more.folds[0].hidden).toHaveLength(5)
    const all = coverageVisible(graph, new Map([[first.folds[0].groupId, 60]]))
    expect(all.folds).toHaveLength(0)
    expect(all.nodes.some(item => item.key === "hidden-service")).toBe(true)
  })

  it("keeps shared children and their edges visible through another parent", () => {
    const shared = Array.from({ length: 23 }, (_, index) => node(`domain-${String(index).padStart(2, "0")}`, "subdomain"))
    const graph: CoverageGraph = { nodes: [node("a", "root_domain"), node("b", "root_domain"), ...shared], edges: [...shared.map(item => ({ src: item.key, dst: "a" })), ...shared.slice(20).map(item => ({ src: item.key, dst: "b" }))], truncated: false }
    const visible = coverageVisible(graph, new Map())
    expect(visible.nodes).toHaveLength(25)
    expect(visible.folds).toHaveLength(0)
    expect(visible.edges).toHaveLength(26)
  })

  it("does not lose cyclic or disconnected legacy nodes, and ignores invalid edges", () => {
    const graph: CoverageGraph = { nodes: [node("a", "ip"), node("b", "ip"), node("alone", "app")], edges: [{ src: "a", dst: "b" }, { src: "b", dst: "a" }, { src: "a", dst: "missing" }, { src: "alone", dst: "alone" }], truncated: false }
    const view = coverageVisible(graph, new Map())
    expect(view.nodes.map(item => item.key).sort()).toEqual(["a", "alone", "b"])
    expect(view.edges).toHaveLength(2)
    const positions = coveragePositions(view.nodes.map(item => item.key), view.edges, new Map([["a", 1], ["b", 2], ["alone", 3]]))
    expect(positions.size).toBe(3)
    expect([...positions.values()].every(position => Number.isFinite(position.y))).toBe(true)
  })

  it("uses ARTEX labels for services and endpoint paths", () => {
    expect(coverageNodeLabel({ ...node("service", "service"), port: 443, page_title: "Portal", status_code: 200 })).toBe(":443 · Portal · 200")
    expect(coverageNodeLabel({ ...node("endpoint", "endpoint"), url: "https://example.test/api?q=1" })).toBe("/api")
    expect(coverageNodeLabel({ ...node("fallback", "endpoint"), url: "invalid" })).toBe("fallback")
  })
})

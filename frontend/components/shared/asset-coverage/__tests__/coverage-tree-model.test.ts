import { describe, expect, it } from "vitest"
import { buildCoverageTree, coverageTreeGroupId, coverageTreeRows, initialCoverageTreeState } from "../coverage-tree-model"
import type { CoverageGraph, CoverageNode } from "@/types/asset-coverage.types"

const node = (key: string, kind: CoverageNode["kind"], tested = false): CoverageNode => ({ key, kind, label: key, tested, in_scope: true })

describe("coverage tree", () => {
  it("groups children, prioritizes confirmed facts and expands large groups in batches", () => {
    const domains = Array.from({ length: 90 }, (_, index) => node(`domain-${String(index).padStart(2, "0")}`, "subdomain", index === 89))
    const graph: CoverageGraph = { nodes: [node("root", "root_domain"), ...domains], edges: domains.map(item => ({ src: item.key, dst: "root" })), truncated: false }
    const index = buildCoverageTree(graph)
    const state = initialCoverageTreeState(index)
    const first = coverageTreeRows(index, state, "")
    expect(first.rows.filter(row => row.type === "asset")).toHaveLength(41)
    expect(first.rows.some(row => row.type === "asset" && row.node.key === "domain-89")).toBe(true)
    expect(first.rows.find(row => row.type === "more")).toMatchObject({ remaining: 50 })
    const expanded = coverageTreeRows(index, { ...state, limits: new Map([[coverageTreeGroupId("root", "subdomain"), 80]]) }, "")
    expect(expanded.rows.find(row => row.type === "more")).toMatchObject({ remaining: 10 })
    const search = coverageTreeRows(index, state, "DOMAIN-88")
    expect(search.matches).toBe(1)
    expect(search.rows.filter(row => row.type === "asset").map(row => row.type === "asset" && row.node.key)).toEqual(["root", "domain-88"])
  })

  it("preserves shared edges as references and makes cycles and disconnected assets reachable", () => {
    const graph: CoverageGraph = {
      nodes: [node("root-a", "root_domain"), node("root-b", "root_domain"), node("shared", "subdomain"), node("loop-a", "ip"), node("loop-b", "ip"), node("alone", "app")],
      edges: [{ src: "shared", dst: "root-a" }, { src: "shared", dst: "root-b" }, { src: "shared", dst: "root-a" }, { src: "loop-a", dst: "loop-b" }, { src: "loop-b", dst: "loop-a" }, { src: "alone", dst: "missing" }, { src: "alone", dst: "alone" }], truncated: false,
    }
    const index = buildCoverageTree(graph)
    expect(index.parents.get("shared")?.map(item => item.key)).toEqual(["root-a", "root-b"])
    expect(index.roots.map(item => item.key)).toContain("alone")
    expect(index.roots.some(item => item.key.startsWith("loop-"))).toBe(true)
    const groups = new Set([coverageTreeGroupId("root-a", "subdomain"), coverageTreeGroupId("root-b", "subdomain")])
    const state = { nodes: new Set(["root-a", "root-b"]), groups, limits: new Map<string, number>() }
    const shared = coverageTreeRows(index, state, "").rows.filter(row => row.type === "asset" && row.node.key === "shared")
    expect(shared).toHaveLength(2)
    expect(shared.filter(row => row.type === "asset" && row.reference)).toHaveLength(1)
  })

  it("finds a deep descendant without recursion or dropping its ancestors", () => {
    const nodes = Array.from({ length: 5000 }, (_, index) => node(index === 4999 ? "needle" : `node-${index}`, "service"))
    const graph: CoverageGraph = { nodes, edges: nodes.slice(1).map((item, index) => ({ src: item.key, dst: nodes[index].key })), truncated: false }
    const index = buildCoverageTree(graph)
    const result = coverageTreeRows(index, initialCoverageTreeState(index), "needle")
    expect(result.matches).toBe(1)
    expect(result.rows.filter(row => row.type === "asset")).toHaveLength(5000)
    expect(result.rows.at(-1)).toMatchObject({ type: "asset", node: { key: "needle" } })
  })
})

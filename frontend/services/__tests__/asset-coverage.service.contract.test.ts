import { beforeEach, describe, expect, it, vi } from "vitest"
import { AssetCoverageService } from "../asset-coverage.service"
const get = vi.hoisted(() => vi.fn())
vi.mock("@/lib/api-client", () => ({ api: { get } }))

describe("asset coverage transport", () => {
  beforeEach(() => { get.mockReset(); get.mockResolvedValue({ data: { nodes: [], edges: [], truncated: false } }) })
  it("binds root graphs and references to the selected organization", async () => {
    const scope = { kind: "root", id: "17", organizationId: 3 } as const
    await AssetCoverageService.graph(scope)
    expect(get).toHaveBeenLastCalledWith("/assetEntities/17/coverageGraph", expect.objectContaining({ params: { organizationId: 3 } }))
    await AssetCoverageService.refs(scope, "18")
    expect(get).toHaveBeenLastCalledWith("/assetEntities/17/coverageRefs", expect.objectContaining({ params: { organizationId: 3, assetId: "18" } }))
  })
  it("preserves the existing task graph and asset reference contracts", async () => {
    await AssetCoverageService.graph({ kind: "task", id: "30" })
    expect(get).toHaveBeenLastCalledWith("/aiTasks/30/coverageGraph", expect.anything())
    await AssetCoverageService.refs({ kind: "task", id: "30" }, "18")
    expect(get).toHaveBeenLastCalledWith("/aiTasks/30/assetRefs", expect.objectContaining({ params: { asset_id: "18" } }))
  })
  it("rejects invalid identities before making an HTTP request", async () => {
    await expect(AssetCoverageService.graph({ kind: "root", id: "../17" })).rejects.toThrow()
    await expect(AssetCoverageService.graph({ kind: "root", id: "17", organizationId: -1 })).rejects.toThrow()
    await expect(AssetCoverageService.refs({ kind: "task", id: "1" }, "0")).rejects.toThrow()
    expect(get).not.toHaveBeenCalled()
  })
})

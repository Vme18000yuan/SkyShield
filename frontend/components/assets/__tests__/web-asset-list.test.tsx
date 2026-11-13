import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { AssetEntityList } from "../asset-entity-list"

const fixture = [
  { id: "1", assetType: "service", displayType: "web", label: "http://site.example/", method: null },
  { id: "2", assetType: "endpoint", displayType: "web", label: "https://site.example/api", method: "GET" },
  { id: "3", assetType: "service", displayType: "service", label: "ssh.example:22", method: null },
].map(item => ({ ...item, name: `assetEntities/${item.id}`, sourceCount: 1, firstSeenAt: "2026-10-31T00:00:00Z", lastSeenAt: "2026-10-31T00:00:00Z" }))
const useAssets = vi.hoisted(() => vi.fn())
vi.mock("@/hooks/use-asset-entities", () => ({ useAssetEntities: (params: unknown) => useAssets(params), useAssetPorts: () => ({ isPending: false, data: { ports: [{ label: "ssh.example:22", port: 22, service: "SSH", serviceSource: "port", organizations: [], firstSeenAt: "2026-10-31T00:00:00Z", lastSeenAt: "2026-10-31T00:00:00Z" }], totalSize: 1 } }), useAssetInventory: () => ({ isPending: false, data: { items: [{ kind: "asset", asset: fixture[0] }, { kind: "asset", asset: fixture[1] }, { kind: "port", port: { label: "ssh.example:22", port: 22, service: "SSH", serviceSource: "port", organizations: [], firstSeenAt: "2026-10-31T00:00:00Z", lastSeenAt: "2026-10-31T00:00:00Z" } }], totalSize: 3 } }) }))
vi.mock("@/components/ai/ai-task-create-dialog", () => ({ AITaskCreateDialog: () => null }))
vi.mock("../asset-registration-dialog", () => ({ AssetRegistrationDialog: () => null }))
vi.mock("../asset-organization-filter", () => ({ AssetOrganizationFilter: () => null }))

describe("Web asset inventory", () => {
  it("renders Web for HTTP sites and URLs, while keeping other services distinct and filterable", async () => {
    useAssets.mockImplementation((params: { assetType?: string; assetCategory?: string }) => ({ isPending: false, error: null, data: { assetEntities: fixture.filter(item => params.assetCategory === "web" ? item.displayType === "web" : params.assetCategory === "non_web_service" ? item.displayType === "service" : params.assetType ? item.assetType === params.assetType : true), nextPageToken: "" } }))
    render(<AssetEntityList />)
    const siteRow = (await screen.findByText("http://site.example/")).closest("tr")!
    const urlRow = screen.getByText("https://site.example/api").closest("tr")!
    const sshRow = screen.getByText("ssh.example:22").closest("tr")!
    expect(within(siteRow).getByText("web")).toBeInTheDocument()
    expect(within(urlRow).getByText("· URL")).toBeInTheDocument()
    expect(within(sshRow).getByText("SSH")).toBeInTheDocument()
    fireEvent.click(screen.getByRole("tab", { name: "web" }))
    await waitFor(() => expect(useAssets).toHaveBeenLastCalledWith(expect.objectContaining({ assetCategory: "web" })))
    expect(screen.queryByText("ssh.example:22")).not.toBeInTheDocument()
    expect(screen.getByText("https://site.example/api")).toBeInTheDocument()
    fireEvent.click(screen.getByRole("tab", { name: "port" }))
    expect(screen.getByText("ssh.example:22")).toBeInTheDocument()
    expect(screen.getByText("SSH")).toBeInTheDocument()
    expect(screen.queryByText("http://site.example/")).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole("tab", { name: "endpoint" }))
    await waitFor(() => expect(useAssets).toHaveBeenLastCalledWith(expect.objectContaining({ assetType: "endpoint" })))
    expect(screen.getByText("https://site.example/api")).toBeInTheDocument()
  })
})

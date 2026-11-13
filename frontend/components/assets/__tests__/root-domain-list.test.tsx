import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import { describe, expect, it, vi } from "vitest"
import messages from "@/messages/zh.json"
import { AssetEntityList } from "../asset-entity-list"

const root = { id: "17", name: "assetEntities/17", assetType: "root_domain", label: "skyshield.example", method: null, sourceCount: 2, firstSeenAt: "2026-10-31T00:00:00Z", lastSeenAt: "2026-10-31T00:00:00Z", derived: true }
const child = { ...root, id: "18", assetType: "subdomain", label: "z.skyshield.example", derived: false }
const useAssets = vi.hoisted(() => vi.fn())
vi.mock("@/hooks/use-asset-entities", () => ({ useAssetEntities: (params: unknown) => useAssets(params), useAssetPorts: () => ({ isPending: false, data: { ports: [], totalSize: 0 } }), useAssetInventory: () => ({ isPending: false, data: { items: [{ kind: "asset", asset: root }, { kind: "asset", asset: child }], totalSize: 2 } }) }))
vi.mock("@/components/ai/ai-task-create-dialog", () => ({ AITaskCreateDialog: () => null }))
vi.mock("@/components/ai/artex/asset-intelligence-dialog", () => ({ AssetIntelligenceDialog: () => null }))
vi.mock("../asset-registration-dialog", () => ({ AssetRegistrationDialog: () => null }))
vi.mock("../asset-organization-filter", () => ({ AssetOrganizationFilter: () => null }))

describe("root-domain inventory", () => {
  it("links the root to its overview without a derived badge", async () => {
    useAssets.mockImplementation((params: { assetType?: string }) => ({ isPending: false, error: null, data: { assetEntities: params.assetType === "root_domain" ? [root] : [root, child], nextPageToken: "", totalSize: 2 } }))
    render(<NextIntlClientProvider locale="zh" messages={messages} timeZone="Asia/Shanghai"><AssetEntityList /></NextIntlClientProvider>)
    expect(await screen.findByText("skyshield.example")).toBeInTheDocument()
    expect(screen.getByText("z.skyshield.example")).toBeInTheDocument()
    expect(screen.getByRole("link", { name: "skyshield.example" })).toHaveAttribute("href", "/assets/root-domains/17")
    expect(screen.queryByText("derived")).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole("tab", { name: "root_domain" }))
    await waitFor(() => expect(useAssets).toHaveBeenLastCalledWith(expect.objectContaining({ assetType: "root_domain" })))
    expect(screen.getByText("skyshield.example")).toBeInTheDocument()
    expect(screen.queryByText("z.skyshield.example")).not.toBeInTheDocument()
  })
})

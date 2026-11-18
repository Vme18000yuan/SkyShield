import { render, screen } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import AssetsPage from "@/app/assets/page"

const lookups = vi.hoisted(() => ({ organizations: vi.fn(), overview: vi.fn(), roots: vi.fn(), inventory: vi.fn() }))
vi.mock("next-intl/server", () => ({ getTranslations: async () => (key: string) => key }))
vi.mock("next/navigation", () => ({ useRouter: () => ({ replace: vi.fn() }) }))
vi.mock("@/components/common/page-header", () => ({ PageHeader: () => null }))
vi.mock("@/hooks/use-organizations", () => ({ useOrganizations: lookups.organizations }))
vi.mock("@/hooks/use-asset-entities", () => ({ useOrganizationAssetOverview: lookups.overview, useAssetEntities: lookups.roots }))
vi.mock("../asset-entity-list", () => ({ AssetEntityList: (props: unknown) => { lookups.inventory(props); return null } }))

async function visit(params: Record<string,string>) {
  render(await AssetsPage({ searchParams: Promise.resolve(params) }))
}

describe("organization asset navigation", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    lookups.organizations.mockReturnValue({ data: { organizations: [{ id: 7, name: "North" }], total: 1 } })
    lookups.overview.mockReturnValue({ data: { id: 7, name: "North", rootDomains: 2, subdomains: 15, ips: 4 } })
    lookups.roots.mockReturnValue({ data: { assetEntities: [{ id: "8", label: "example.test" }] } })
  })
  it("starts with organizations and does not fetch an inventory", async () => {
    await visit({})
    expect(screen.getByRole("link", { name: "North" })).toHaveAttribute("href", "/assets?organizationId=7")
    expect(lookups.overview).not.toHaveBeenCalled()
    expect(lookups.roots).not.toHaveBeenCalled()
    expect(lookups.inventory).not.toHaveBeenCalled()
  })
  it("opens organization totals and links an IP count to the same filtered list", async () => {
    await visit({ organizationId: "7" })
    expect(lookups.overview).toHaveBeenCalledWith(7, true)
    expect(lookups.roots).toHaveBeenCalledWith(expect.objectContaining({ organizationId: 7, assetType: "root_domain" }))
    expect(screen.getByRole("link", { name: "example.test" })).toHaveAttribute("href", "/assets/root-domains/8?organizationId=7&from=overview")
    expect(screen.getByRole("link", { name: /ip 4/i })).toHaveAttribute("href", "/assets?organizationId=7&view=inventory&type=ip")
    expect(lookups.inventory).not.toHaveBeenCalled()
  })
  it("preserves the selected organization, category and CDN switch on deep links", async () => {
    await visit({ organizationId: "7", view: "inventory", type: "subdomain", showCdn: "1" })
    expect(lookups.inventory).toHaveBeenCalledWith(expect.objectContaining({ organizationId: 7, fixedOrganization: true, initialType: "subdomain", showCdn: true }))
  })
  it("retains existing search links while rejecting a malformed organization scope", async () => {
    await visit({ q: "example.test" })
    expect(lookups.inventory).toHaveBeenCalledWith(expect.objectContaining({ initialSearch: "example.test" }))
    lookups.inventory.mockClear()
    await visit({ organizationId: "-1", q: "example.test" })
    expect(lookups.inventory).not.toHaveBeenCalled()
  })
})

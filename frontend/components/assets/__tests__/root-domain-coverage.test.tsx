import { fireEvent, render, screen } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import { describe, expect, it, vi } from "vitest"
import messages from "@/messages/zh.json"
import { RootDomainOverview } from "../root-domain-overview"

const fixtures = vi.hoisted(() => ({ graph: vi.fn(), replace: vi.fn() }))
vi.mock("next/navigation", () => ({ useSearchParams: () => new URLSearchParams("organizationId=3"), useRouter: () => ({ replace: fixtures.replace }) }))
vi.mock("@/hooks/use-asset-entities", () => ({ useRootDomainOverview: () => ({ data: { domain: "example.test", counts: { subdomains: 1, ips: 1, ports: 1, apps: 0, web: 1, urls: 0 } } }) }))
vi.mock("../asset-entity-list", () => ({ AssetEntityList: () => <div>Inventory fixture</div> }))
vi.mock("../asset-organization-filter", () => ({ AssetOrganizationFilter: ({ onChange }: { onChange: (id?: number) => void }) => <button onClick={() => onChange(4)}>Switch organization</button> }))
vi.mock("@/components/shared/asset-coverage/asset-coverage-graph", () => ({ AssetCoverageGraph: (props: unknown) => { fixtures.graph(props); return <div>Graph fixture</div> } }))

describe("root-domain coverage entry", () => {
  it("loads the graph on demand with root and organization scope", () => {
    render(<NextIntlClientProvider locale="zh" messages={messages} timeZone="Asia/Shanghai"><RootDomainOverview id="17" /></NextIntlClientProvider>)
    expect(screen.queryByText("Graph fixture")).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole("tab", { name: "coverageGraph" }))
    expect(screen.getByText("Graph fixture")).toBeInTheDocument()
    expect(fixtures.graph).toHaveBeenLastCalledWith({ scope: { kind: "root", id: "17", organizationId: 3 } })
    fireEvent.click(screen.getByRole("button", { name: "Switch organization" }))
    expect(fixtures.replace).toHaveBeenCalledWith("/assets/root-domains/17/?organizationId=4")
  })
})

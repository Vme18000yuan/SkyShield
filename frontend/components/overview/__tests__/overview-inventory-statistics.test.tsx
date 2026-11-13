import { render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { OverviewAssetOverview } from "@/components/overview/overview-asset-overview"

vi.mock("next-intl", () => ({ useLocale: () => "zh-CN", useTranslations: () => (key: string) => key }))
vi.mock("framer-motion", () => ({ useReducedMotion: () => true }))
vi.mock("@/hooks/use-overview", () => ({
  useAssetStatistics: () => ({ data: { totalAssets: 11, totalTargets: 1, totalSubdomains: 2, totalIps: 3, totalServices: 6, totalEndpoints: 999, totalWebsites: 888, changeAssets: 0, changeTargets: 0 } }),
  useStatisticsHistory: () => ({ data: [
    { date: "2026-10-30", totalAssets: 5, totalSubdomains: 1, totalIps: 2, totalServices: 2 },
    { date: "2026-10-31", totalAssets: 11, totalSubdomains: 2, totalIps: 3, totalServices: 6 },
  ] }),
}))
vi.mock("@/components/overview/overview-area-chart", () => ({ OverviewAreaChart: () => null }))
vi.mock("@/components/overview/overview-number-ticker", () => ({ OverviewNumberTicker: ({ value }: { value: number }) => <span>{value}</span> }))

describe("overview inventory statistics", () => {
  it("shows unique subdomains, IPs and host ports with matching history", () => {
    render(<OverviewAssetOverview />)
    const distribution = screen.getByRole("img", { name: /tabs\.subdomains/ })
    expect(distribution).toHaveAccessibleName(/tabs\.subdomains 2 \(18%\)/)
    expect(distribution).toHaveAccessibleName(/tabs\.ips 3 \(27%\)/)
    expect(distribution).toHaveAccessibleName(/services 6 \(55%\).*newIn7d \+4/)
    expect(distribution).not.toHaveAccessibleName(/endpoints|websites/)
  })

  it("renders the moved metrics and the trend as separate sections", () => {
    const metrics = render(<OverviewAssetOverview part="metrics" />)
    expect(screen.getByText("assets")).toBeVisible()
    expect(screen.getByText("targets")).toBeVisible()
    expect(screen.getByRole("img", { name: /tabs\.subdomains/ })).toBeVisible()
    expect(screen.queryByRole("heading", { name: "trend" })).not.toBeInTheDocument()
    metrics.unmount()

    render(<OverviewAssetOverview part="trend" />)
    expect(screen.getByRole("heading", { name: "trend" })).toBeVisible()
    expect(screen.queryByText("targets")).not.toBeInTheDocument()
    expect(screen.queryByRole("img", { name: /tabs\.subdomains/ })).not.toBeInTheDocument()
  })
})

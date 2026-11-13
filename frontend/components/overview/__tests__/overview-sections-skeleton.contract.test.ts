import { describe, expect, it } from "vitest"
import { readFileSync } from "node:fs"
import path from "node:path"

const source = readFileSync(path.resolve(process.cwd(), "components/overview/overview-sections-skeleton.tsx"), "utf8")
const layoutSource = readFileSync(path.resolve(process.cwd(), "components/overview/overview-section-layouts.tsx"), "utf8")

describe("overview-sections-skeleton contract", () => {
  it("reuses the operational dashboard layout owners", () => {
    expect(source).toContain("from \"@/components/overview/overview-section-layouts\"")
    expect(source).toContain("OverviewOperationalGrid")
    expect(source).toContain("OverviewSectionPanel")
    expect(source).toContain("OverviewTokenUsageSkeleton")
    expect(source).not.toContain("OverviewAgentLocationMapSkeleton")
    expect(source).not.toContain('data-slot="agent-status-legend"')
  })

  it("shares the major geometry constants used by the resolved dashboard", () => {
    const constants = [
      "OVERVIEW_SECTIONS_SHELL_CLASS",
      "OVERVIEW_RISK_SUMMARY_GRID_CLASS",
      "OVERVIEW_ASSET_OVERVIEW_GRID_CLASS",
      "OVERVIEW_ASSET_CHART_SHELL_CLASS",
      "OVERVIEW_ASSET_TREND_PANEL_CLASS",
      "OVERVIEW_ASSET_TREND_CHART_CLASS",
    ]

    for (const constant of constants) {
      expect(source).toContain(constant)
      expect(layoutSource).toContain(constant)
    }
  })

  it("matches the rearranged risk, asset, operational, and system regions", () => {
    expect(source).not.toContain("OverviewReadinessSection")
    expect(source).not.toContain("OVERVIEW_READINESS_GRID_CLASS")
    expect(source).not.toContain("OVERVIEW_READINESS_ITEM_CLASS")
    expect(source.indexOf("<OverviewRiskSummarySkeleton />")).toBeLessThan(source.indexOf('<OverviewAssetOverviewSkeleton part="metrics"'))
    expect(source.indexOf('<OverviewAssetOverviewSkeleton part="metrics"')).toBeLessThan(source.indexOf("<OverviewTokenUsageSkeleton />"))
    expect(source.indexOf("<OverviewScanQueueSkeleton />")).toBeLessThan(source.indexOf("<OverviewRuntimeDetailsSkeleton />"))
    expect(source.indexOf("<OverviewRuntimeDetailsSkeleton />")).toBeLessThan(source.indexOf('<OverviewAssetOverviewSkeleton part="trend"'))
    expect(source).toContain("<OverviewScanQueueSkeleton />")
    expect(source.indexOf("<OverviewTokenUsageSkeleton />")).toBeLessThan(source.indexOf("<OverviewScanQueueSkeleton />"))
  })

  it("preserves the route-owned loading boundary without hidden dynamic content", () => {
    expect(source).toContain("export function OverviewSectionsSkeleton")
    expect(source).not.toContain("ContentHandoff")
    expect(source).not.toContain("next/dynamic")
    expect(source).not.toContain("animate-pulse")
    expect(source).not.toContain("OverviewScanStatus")
    expect(source).toContain("OverviewRuntimeDetailsSkeleton")
    expect(source).toContain("OverviewTokenUsageSkeleton")
    expect(source).not.toContain("OVERVIEW_AGENT_LOCATION_MAP_SURFACE_CLASS")
    expect(source).toContain("OVERVIEW_ASSET_CHART_SHELL_CLASS")
  })

  it("uses the shared independent runtime-card geometry", () => {
    expect(source).toContain("OverviewRuntimeDetailsLayout")
    expect(source).toContain("OverviewRuntimeDetailsCard")
    expect(layoutSource).toContain("OVERVIEW_RUNTIME_DETAILS_GRID_CLASS")
    expect(layoutSource).toContain("OVERVIEW_RUNTIME_DETAILS_ITEM_CLASS")
    expect(layoutSource).toContain("OVERVIEW_RUNTIME_DETAILS_ITEM_CONTENT_CLASS")
    expect(layoutSource).toContain('flex h-full min-h-52 flex-col gap-3')
    expect(layoutSource).toContain('h-full min-w-0')
    expect(layoutSource).toContain('OVERVIEW_RUNTIME_SCAN_BODY_CLASS')
    expect(layoutSource).toContain('OVERVIEW_RUNTIME_DATABASE_BODY_CLASS')
    expect(layoutSource).toContain('flex flex-1 flex-col justify-between gap-3')
    expect(layoutSource).toContain('grid grid-cols-2 gap-3 border-t pt-3')
    expect(layoutSource).toContain('flex min-w-0 items-baseline justify-between gap-3 border-b pb-4')
    expect(source).toContain("OVERVIEW_RUNTIME_SCAN_BODY_CLASS")
    expect(source).toContain("OVERVIEW_RUNTIME_DATABASE_BODY_CLASS")
    expect(source).toContain("OVERVIEW_RUNTIME_SCAN_STATUS_GRID_CLASS")
    expect(source).toContain("OVERVIEW_RUNTIME_SCAN_STATUS_ITEM_CLASS")
    expect(source).toContain("OVERVIEW_RUNTIME_SCAN_RECENT_CLASS")
    expect(source).toContain("OVERVIEW_RUNTIME_SCAN_RECENT_LIST_CLASS")
    expect(source).toContain("OVERVIEW_RUNTIME_SCAN_RECENT_META_CLASS")
    expect(source).toContain("OVERVIEW_RUNTIME_SCAN_RECENT_ROWS_CLASS")
    expect(source).toContain("OVERVIEW_RUNTIME_SCAN_RECENT_ROW_CLASS")
    expect(source).toContain("<ScrollArea")
    expect(source).toContain("OVERVIEW_RUNTIME_DATABASE_METRIC_GRID_CLASS")
    expect(source).toContain("OVERVIEW_RUNTIME_DATABASE_DIAGNOSTIC_GRID_CLASS")
    expect(source).toContain("OVERVIEW_RUNTIME_RESOURCE_LIST_CLASS")
    expect(source).toContain('Skeleton className="size-4 rounded-none"')
    expect(source).toContain('Skeleton className="ml-auto h-[19.5px] w-24 rounded-none"')
    expect(layoutSource).toContain("OVERVIEW_RUNTIME_SCAN_STATUS_GRID_CLASS")
    expect(layoutSource).toContain("OVERVIEW_RUNTIME_SCAN_STATUS_ITEM_CLASS")
    expect(layoutSource).toContain("OVERVIEW_RUNTIME_SCAN_RECENT_CLASS")
    expect(layoutSource).toContain("OVERVIEW_RUNTIME_SCAN_RECENT_LIST_CLASS")
    expect(layoutSource).toContain("OVERVIEW_RUNTIME_SCAN_RECENT_META_CLASS")
    expect(layoutSource).toContain("OVERVIEW_RUNTIME_SCAN_RECENT_ROWS_CLASS")
    expect(layoutSource).toContain("OVERVIEW_RUNTIME_SCAN_RECENT_ROW_CLASS")
    expect(layoutSource).toContain("OVERVIEW_RUNTIME_DATABASE_METRIC_GRID_CLASS")
    expect(layoutSource).toContain("OVERVIEW_RUNTIME_DATABASE_DIAGNOSTIC_GRID_CLASS")
    expect(layoutSource).toContain("OVERVIEW_RUNTIME_RESOURCE_LIST_CLASS")
    expect(source).not.toContain("OVERVIEW_RUNTIME_AGENT")
    expect(layoutSource).not.toContain("OVERVIEW_RUNTIME_AGENT")
    expect(layoutSource).toContain("grid min-w-0 grid-cols-1 gap-0 sm:grid-cols-2")
    expect(source).toContain('cn(OVERVIEW_RUNTIME_RESOURCE_ROW_CLASS, "space-y-1")')
    expect(source).toContain('className="shrink-0 text-right tabular-nums"')
    expect(source).toContain("Array.from({ length: 18 })")
  })

  it("reserves responsive risk text geometry with the resolved structure", () => {
    expect(source).toContain('useTranslations("overview.operational.risk")')
    expect(source).toContain('reserveText={t("title")}')
    expect(source).toContain('reserveText={t("trendTitle")}')
    expect(source).toContain("import { ActionSkeleton } from \"@/components/shared/loading/action-skeleton\"")
    expect(source).toContain('action={<OverviewRiskDetailsActionSkeleton />}')
    expect(source).toContain('<ActionSkeleton size="content" widthClassName="w-24" className="h-6" />')
    expect(source).toContain("OVERVIEW_RISK_RING_LEGEND_CLASS")
    expect(source).toContain("OVERVIEW_RISK_TREND_CHART_CLASS")
    expect(source).toContain('featured roleClassName={textRole.metricValueDisplay} className="w-12"')
    expect(source).toContain('const Element = inline ? "span" : "div"')
  })
})

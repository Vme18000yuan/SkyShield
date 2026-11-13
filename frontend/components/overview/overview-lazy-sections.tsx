"use client"

import { OverviewAssetOverview } from "@/components/overview/overview-asset-overview"
import { OverviewRiskSummary } from "@/components/overview/overview-risk-summary"
import { OverviewScanQueue, OverviewRuntimeDetails } from "@/components/overview/overview-runtime-details"
import { OverviewTokenUsage } from "@/components/overview/overview-token-usage"
import {
  OVERVIEW_ASSET_REGION_LOADING_SLOT,
  OVERVIEW_OPERATIONAL_REGION_LOADING_SLOT,
  OVERVIEW_RUNTIME_REGION_LOADING_SLOT,
  OVERVIEW_SECTIONS_SHELL_CLASS,
  OverviewOperationalGrid,
} from "@/components/overview/overview-section-layouts"
import { cn } from "@/lib/utils"

export type OverviewLazySectionsProps = {
  className?: string
}

export function OverviewLazySections({ className }: OverviewLazySectionsProps) {
  return (
    <div className={cn(OVERVIEW_SECTIONS_SHELL_CLASS, className)}>
      <OverviewOperationalGrid data-loading-slot={OVERVIEW_RUNTIME_REGION_LOADING_SLOT}>
        <OverviewRiskSummary />
        <OverviewAssetOverview part="metrics" className="pt-4 xl:pt-0" />
      </OverviewOperationalGrid>

      <OverviewOperationalGrid data-loading-slot={OVERVIEW_OPERATIONAL_REGION_LOADING_SLOT}>
        <OverviewTokenUsage />
        <OverviewScanQueue />
      </OverviewOperationalGrid>

      <OverviewOperationalGrid data-loading-slot={OVERVIEW_ASSET_REGION_LOADING_SLOT}>
        <section className="min-w-0">
          <OverviewRuntimeDetails />
        </section>
        <OverviewAssetOverview part="trend" className="h-full pt-4 xl:pt-0" />
      </OverviewOperationalGrid>
    </div>
  )
}

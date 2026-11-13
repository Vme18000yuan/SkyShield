import { OverviewSectionPanel } from "@/components/overview/overview-section-layouts"
import { Skeleton } from "@/components/ui/skeleton"

export function OverviewTokenUsageSkeleton() {
  return (
    <OverviewSectionPanel className="h-full">
      <div aria-hidden="true" className="space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <Skeleton className="h-4 w-32" />
          <Skeleton className="h-7 w-28" />
        </div>
        <div className="grid gap-4 @2xl/panel:grid-cols-[minmax(140px,0.7fr)_minmax(0,1.3fr)]">
          <div className="space-y-3">
            <Skeleton className="h-12 w-32" />
            {Array.from({ length: 3 }, (_, index) => <Skeleton key={index} className="h-7 w-full" />)}
          </div>
          <div className="space-y-3"><Skeleton className="h-7 w-48" /><Skeleton className="h-44 w-full" /></div>
        </div>
      </div>
    </OverviewSectionPanel>
  )
}

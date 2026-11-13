import { Suspense } from "react"
import { AssetMonitorScreen, MonitorScreenLoading } from "@/components/asset-monitor/monitor-screen"

export default function AssetMonitorScreenPage() {
  return <Suspense fallback={<MonitorScreenLoading />}><AssetMonitorScreen /></Suspense>
}

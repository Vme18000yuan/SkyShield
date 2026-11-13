"use client"

import { AssetCoverageGraph } from "@/components/shared/asset-coverage/asset-coverage-graph"

export function TaskCoverageGraph({ taskId, coverageEnabled, sourceNames }: { taskId: string; coverageEnabled: boolean; sourceNames: Record<string, string> }) {
  return <AssetCoverageGraph scope={{ kind: "task", id: taskId }} coverageEnabled={coverageEnabled} sourceNames={sourceNames} />
}

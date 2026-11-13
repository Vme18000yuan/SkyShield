import { api } from "@/lib/api-client"
import type { CoverageGraph, CoverageRefs, CoverageScope } from "@/types/asset-coverage.types"

function coveragePath(scope: CoverageScope) {
  if (!/^[1-9]\d*$/.test(scope.id)) throw new Error("Invalid coverage resource ID")
  if (scope.kind === "root" && scope.organizationId !== undefined && (!Number.isSafeInteger(scope.organizationId) || scope.organizationId <= 0 || scope.organizationId > 2147483647)) throw new Error("Invalid organization ID")
  return scope.kind === "task" ? `/aiTasks/${scope.id}` : `/assetEntities/${scope.id}`
}

export const AssetCoverageService = {
  async graph(scope: CoverageScope, signal?: AbortSignal): Promise<CoverageGraph> {
    return (await api.get<CoverageGraph>(`${coveragePath(scope)}/coverageGraph`, { signal, params: scope.kind === "root" ? { organizationId: scope.organizationId } : undefined })).data
  },
  async refs(scope: CoverageScope, assetId: string, signal?: AbortSignal): Promise<CoverageRefs> {
    if (!/^[1-9]\d*$/.test(assetId)) throw new Error("Invalid asset ID")
    const path = coveragePath(scope)
    return (await api.get<CoverageRefs>(`${path}/${scope.kind === "task" ? "assetRefs" : "coverageRefs"}`, { signal, params: scope.kind === "task" ? { asset_id: assetId } : { assetId, organizationId: scope.organizationId } })).data
  },
}

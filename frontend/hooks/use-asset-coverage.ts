"use client"

import { useQuery } from "@tanstack/react-query"
import { useAuth } from "@/hooks/use-auth"
import { AssetCoverageService } from "@/services/asset-coverage.service"
import type { CoverageScope } from "@/types/asset-coverage.types"

export function useCoverageGraph(scope: CoverageScope, hideCdn = true) {
  const user = useAuth().data?.user?.id
  return useQuery({ queryKey: ["asset-coverage-graph", "default-tenant", user, scope, hideCdn], enabled: !!user, gcTime: 0, staleTime: 0, queryFn: ({ signal }) => AssetCoverageService.graph(scope, signal, hideCdn) })
}

export function useCoverageRefs(scope: CoverageScope, assetId?: string, enabled = true) {
  const user = useAuth().data?.user?.id
  return useQuery({ queryKey: ["asset-coverage-refs", "default-tenant", user, scope, assetId], enabled: !!user && !!assetId && enabled, gcTime: 0, staleTime: 0, queryFn: ({ signal }) => AssetCoverageService.refs(scope, assetId!, signal) })
}

"use client"

import { useQuery } from "@tanstack/react-query"
import { useAuth } from "@/hooks/use-auth"
import { AssetMonitorService } from "@/services/asset-monitor.service"

export function useAssetMonitor(organizationIds: number[] | null, autoRefresh = true) {
  const user = useAuth().data?.user?.id
  const selection = organizationIds?.join(",") ?? ""
  return useQuery({
    queryKey: ["asset-monitor", "default-tenant", user, selection],
    queryFn: () => AssetMonitorService.current(selection.split(",").map(Number)),
    enabled: Boolean(user && selection),
    staleTime: 10_000,
    refetchInterval: autoRefresh ? 30_000 : false,
    refetchOnWindowFocus: autoRefresh,
  })
}

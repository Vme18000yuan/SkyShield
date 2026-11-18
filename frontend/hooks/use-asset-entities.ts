"use client"

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useAuth } from "@/hooks/use-auth"
import { AssetEntityService } from "@/services/asset-entity.service"
import type { AssetEntityListParams, AssetRegistrationInput } from "@/types/asset-entity.types"

export function useAssetEntities(params: AssetEntityListParams, enabled = true) {
  const auth = useAuth()
  const userId = auth.data?.user?.id
  return useQuery({
    queryKey: ["asset-entities", "default-tenant", userId, params],
    queryFn: () => AssetEntityService.list(params),
    enabled: Boolean(userId) && enabled,
    // Root domains and application candidates are projected asynchronously.
    refetchInterval: 5000,
  })
}

export function useAssetPorts(params: Pick<AssetEntityListParams, "q" | "page" | "pageSize" | "organizationId" | "rootDomainId" | "hideCdn">, enabled = true) {
  const auth = useAuth()
  return useQuery({ queryKey: ["asset-ports", auth.data?.user?.id, params], queryFn: () => AssetEntityService.listPorts(params), enabled: Boolean(auth.data?.user?.id) && enabled, refetchInterval: 5000 })
}

export function useAssetInventory(params: Pick<AssetEntityListParams, "q" | "page" | "pageSize" | "organizationId" | "rootDomainId" | "hideCdn">, enabled = true) {
  const auth = useAuth()
  return useQuery({ queryKey: ["asset-inventory", auth.data?.user?.id, params], queryFn: () => AssetEntityService.listInventory(params), enabled: Boolean(auth.data?.user?.id) && enabled, refetchInterval: 5000 })
}

export function useOrganizationAssetOverview(id: number, hideCdn: boolean) {
  const user = useAuth().data?.user?.id
  return useQuery({ queryKey: ["organization-asset-overview", user, id, hideCdn], enabled: !!user,
    queryFn: ({ signal }) => AssetEntityService.organizationOverview(id, hideCdn, signal), staleTime: 10000, refetchInterval: 30000 })
}

export function useRootDomainOverview(id: string, organizationId?: number, hideCdn?: boolean) {
  const auth = useAuth()
  return useQuery({ queryKey: ["root-domain-overview", auth.data?.user?.id, id, organizationId, hideCdn], queryFn: () => AssetEntityService.rootOverview(id, organizationId, hideCdn), enabled: Boolean(auth.data?.user?.id) && /^[1-9]\d*$/.test(id), refetchInterval: 15000 })
}

export function useApplicationOverview(id: string, organizationId: number | undefined, page: number) {
  const auth = useAuth()
  return useQuery({ queryKey: ["application-overview", auth.data?.user?.id, id, organizationId, page], queryFn: () => AssetEntityService.applicationOverview(id, { organizationId, page, pageSize: 50 }), enabled: Boolean(auth.data?.user?.id) && /^[1-9]\d*$/.test(id), refetchInterval: 15000 })
}

export function useRegisterAssets() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ assets, requestId }: { assets: AssetRegistrationInput[]; requestId: string }) => AssetEntityService.register(assets, requestId),
    onSuccess: async result => {
      if (!result.results.length) return
      await Promise.all(["organization-asset-overview", "asset-entities", "asset-ports", "asset-inventory", "root-domain-overview", "application-overview", "websites", "endpoints", "subdomains", "targets", "asset-search", "asset-statistics"].map(resource =>
        client.invalidateQueries({ queryKey: [resource] })))
    },
  })
}

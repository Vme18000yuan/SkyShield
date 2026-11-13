import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { HTTPProxySettingsService } from "@/services/http-proxy-settings.service"
import type { UpdateHTTPProxySettingsRequest } from "@/types/http-proxy-settings.types"

export const httpProxySettingsKey = ["system-http-proxy-settings"] as const

export function useHTTPProxySettings() {
  return useQuery({ queryKey: httpProxySettingsKey, queryFn: () => HTTPProxySettingsService.get() })
}

export function useUpdateHTTPProxySettings() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: UpdateHTTPProxySettingsRequest) => HTTPProxySettingsService.update(input),
    retry: false,
    onSuccess: (settings) => queryClient.setQueryData(httpProxySettingsKey, settings),
  })
}

export function useClearHTTPProxySettings() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () => HTTPProxySettingsService.clear(),
    retry: false,
    onSuccess: (settings) => queryClient.setQueryData(httpProxySettingsKey, settings),
  })
}

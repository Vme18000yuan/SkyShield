"use client"

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useAuth } from "@/hooks/use-auth"
import { FofaService } from "@/services/fofa.service"

export function useFofa() {
  const user = useAuth().data?.user?.id
  const client = useQueryClient()
  const key = ["fofa-settings", "default-tenant", user]
  const settings = useQuery({ queryKey: key, queryFn: FofaService.settings, enabled: Boolean(user), retry: false })
  const save = useMutation({ mutationFn: FofaService.save, retry: false, onSuccess: data => client.setQueryData(key, data) })
  const clear = useMutation({ mutationFn: FofaService.clear, retry: false, onSuccess: () => client.invalidateQueries({ queryKey: key }) })
  const test = useMutation({ mutationFn: FofaService.test, retry: false })
  // Search is deliberately a mutation: focus, reconnect and retry must never
  // trigger another provider query or spend FOFA credits implicitly.
  const search = useMutation({ mutationFn: FofaService.search, retry: false })
  const sync = useMutation({ mutationFn: FofaService.sync, retry: false, onSuccess: async result => {
    if (!result.importedRows) return
    await Promise.all(["asset-entities", "websites", "subdomains", "host-ports", "hostPorts", "ip-addresses", "targets", "organizations", "asset-search", "asset-statistics", "overview"].map(resource =>
      client.invalidateQueries({ queryKey: [resource] })))
  } })
  return { userId: user, settings, save, clear, test, search, sync }
}

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { OASTService } from "@/services/oast.service"
import type { OASTCreateInput, OASTEventPage, OASTScope } from "@/types/oast.types"

const scopeKey = (scope: OASTScope) => ["oast", scope.kind, scope.id] as const

export function useOASTProbes(scope: OASTScope, before: string) {
  const client = useQueryClient()
  const refresh = () => client.invalidateQueries({ queryKey: scopeKey(scope) })
  const list = useQuery({
    queryKey: [...scopeKey(scope), "probes", before],
    queryFn: ({ signal }) => OASTService.list(scope, before, signal),
    refetchInterval: 5000,
  })
  const create = useMutation({ mutationFn: (input: OASTCreateInput) => OASTService.create(scope, input), onSuccess: refresh })
  const revoke = useMutation({ mutationFn: (id: string) => OASTService.revoke(scope, id), onSuccess: refresh })
  return { list, create, revoke }
}

export function useOASTEvents(scope: OASTScope, id: string) {
  const client = useQueryClient()
  const key = [...scopeKey(scope), "events", id]
  return useQuery({
    queryKey: key,
    queryFn: async ({ signal }) => {
      const previous = client.getQueryData<OASTEventPage>(key)
      const page = await OASTService.events(scope, id, previous?.nextAfter ?? "", signal)
      // Only fetch new evidence. Retained events are immutable and bounded to
      // 200, so polling never downloads the same request bodies repeatedly.
      return { ...page, items: [...(previous?.items ?? []), ...page.items] }
    },
    refetchInterval: query => query.state.data?.hasMore ? 100 : query.state.data?.probe.state === "active" ? 3000 : false,
  })
}
